//go:build js && wasm

// Command wasm is the browser side of the tunnel: tailcat's client compiled to WebAssembly and
// exposed as window.BunnyTunnel (docs/ARCHITECTURE.md §wasm bridge). connect() brings up one
// tailcat.Client (DERP over WebSocket, WireGuard in netstack) and resolves after the first
// handshake; dial() opens TCP streams over that session with no new handshake.
//
// Adapted from tailcat's web/main_js.go, Copyright (c) Tailscale Inc & contributors,
// SPDX-License-Identifier: BSD-3-Clause (promise helpers, ping retry loop, connection object).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"syscall/js"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/logger"
)

const (
	connectTimeout = 60 * time.Second // whole connect(), handshake retries included
	pingTimeout    = 5 * time.Second  // one handshake attempt
	dialTimeout    = 30 * time.Second
)

func main() {
	js.Global().Set("BunnyTunnel", map[string]any{"connect": js.FuncOf(connect)})
	select {}
}

// connect implements BunnyTunnel.connect(opts): see the TS interface in docs/ARCHITECTURE.md.
func connect(this js.Value, args []js.Value) any {
	if len(args) != 1 || args[0].Type() != js.TypeObject {
		return rejectedPromise(errors.New("connect requires an options object"))
	}
	opts := args[0]
	addr := optString(opts, "addr")
	derpMapURL := optString(opts, "derpMapURL")
	keyJSON := optString(opts, "privateKey")
	verbose := opts.Get("verbose").Truthy()
	onLog := opts.Get("onLog")
	return makePromise(func() (any, error) {
		if addr == "" {
			return nil, errors.New("addr is required")
		}
		ci, err := tailcat.ParseConnBlob(tailcat.ConnBlob(addr))
		if err != nil {
			return nil, fmt.Errorf("invalid address: %w", err)
		}
		pk := tailcat.NewPrivateKey()
		if keyJSON != "" {
			pk = new(tailcat.PrivateKey)
			if err := json.Unmarshal([]byte(keyJSON), pk); err != nil || pk.Private.IsZero() {
				return nil, fmt.Errorf("parsing privateKey: %v", err)
			}
		}
		keyOut, err := json.Marshal(pk)
		if err != nil {
			return nil, err
		}
		// say reports the bridge's own progress to onLog (and the console when verbose);
		// tailcat's internal logging goes to the console only when verbose.
		say := func(format string, a ...any) {
			line := fmt.Sprintf(format, a...)
			if verbose {
				log.Print(line)
			}
			if onLog.Type() == js.TypeFunction {
				onLog.Invoke(line)
			}
		}
		logf := logger.Discard
		if verbose {
			logf = log.Printf
		}
		cl := &tailcat.Client{Server: tailcat.ConnBlob(addr), Key: pk.Private, Logf: logf, DERPMapURL: derpMapURL}
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		t0 := time.Now()
		if err := pingUntil(ctx, cl, say); err != nil {
			cl.Close()
			return nil, err
		}
		say("handshake up after %v", time.Since(t0).Round(time.Millisecond))
		relay := "DERP(" + relayName(ctx, ci, derpMapURL) + ")"
		say("relayed via %s", relay)
		return newSession(cl, relay, addr, string(keyOut)), nil
	})
}

// pingUntil retries the meow/meowed handshake until it succeeds or ctx expires; the first pings
// can be lost while either side's DERP connection is still coming up.
func pingUntil(ctx context.Context, cl *tailcat.Client, say func(string, ...any)) error {
	for attempt := 1; ; attempt++ {
		pctx, cancel := context.WithTimeout(ctx, pingTimeout)
		res, err := cl.Ping(pctx)
		cancel()
		if err == nil {
			say("handshake attempt %d: ok, %v", attempt, res.Latency.Round(time.Millisecond))
			return nil
		}
		say("handshake attempt %d: %v", attempt, err)
		if ctx.Err() != nil {
			return fmt.Errorf("connect timed out after %v: %w", connectTimeout, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// newSession is the Session object: addr, privateKeyJSON, dial(port?), ping(), close().
func newSession(cl *tailcat.Client, relay, addr, keyJSON string) js.Value {
	return js.ValueOf(map[string]any{
		"addr":           addr,
		"privateKeyJSON": keyJSON,
		"dial": js.FuncOf(func(this js.Value, args []js.Value) any {
			port := 80
			if len(args) > 0 && args[0].Type() == js.TypeNumber {
				port = args[0].Int()
			}
			if port < 1 || port > 65535 {
				return rejectedPromise(fmt.Errorf("invalid port %d", port))
			}
			return makePromise(func() (any, error) {
				ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
				defer cancel()
				c, err := cl.DialTCPPort(ctx, uint16(port))
				if err != nil {
					return nil, fmt.Errorf("dial port %d: %w", port, err)
				}
				return makeJSConn(c), nil
			})
		}),
		"ping": js.FuncOf(func(this js.Value, args []js.Value) any {
			return makePromise(func() (any, error) { return ping(cl, relay) })
		}),
		"close": js.FuncOf(func(this js.Value, args []js.Value) any {
			cl.Close()
			return nil
		}),
	})
}

// ping reports {rttMs, via, direct}. rttMs is a TCP connect to the tunnel port through the
// relay, the round trip every request pays; via names the relay; direct is false because
// browsers have no UDP path (tailcat issue #4). tailcat's own DiscoPing never gets its pong under
// js/wasm (v0.4.0) and Client.Ping returns instantly after the first handshake, so neither is used.
func ping(cl *tailcat.Client, relay string) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	t0 := time.Now()
	c, err := cl.DialTCPPort(ctx, 80)
	if err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	rtt := time.Since(t0)
	c.Close()
	return map[string]any{"rttMs": float64(rtt) / float64(time.Millisecond), "via": relay, "direct": false}, nil
}

// relayName is the relay's region code from the DERP map (already fetched and cached in-process
// by the client's own startup), or the relay's hostname for an address that embeds its relay
// (ConnBlob strips the code), or the bare region ID.
func relayName(ctx context.Context, ci tailcat.ConnInfo, derpMapURL string) string {
	for _, r := range ci.Region {
		if len(r.Nodes) > 0 && r.Nodes[0].HostName != "" {
			return r.Nodes[0].HostName
		}
	}
	if derpMapURL == "" {
		derpMapURL = tailcat.DefaultDERPMapURL
	}
	if dm, err := tailcat.FetchDERPMap(ctx, tailcat.DERPMapURL(derpMapURL)); err == nil {
		if r := dm.Regions[ci.RegionID]; r != nil && r.RegionCode != "" {
			return r.RegionCode
		}
	}
	return strconv.Itoa(int(ci.RegionID))
}

// makeJSConn wraps a tunneled TCP connection as the Conn object: read() (null on EOF; no
// concurrent reads), write(Uint8Array), closeWrite(), close(). read is pull-based, so a fast
// sender stalls on TCP backpressure rather than filling browser memory.
func makeJSConn(c net.Conn) js.Value {
	buf := make([]byte, 64<<10)
	return js.ValueOf(map[string]any{
		"read": js.FuncOf(func(this js.Value, args []js.Value) any {
			return makePromise(func() (any, error) {
				n, err := c.Read(buf)
				if n > 0 {
					u8 := js.Global().Get("Uint8Array").New(n)
					js.CopyBytesToJS(u8, buf[:n])
					return u8, nil
				}
				if err == nil || errors.Is(err, io.EOF) {
					return js.Null(), nil
				}
				return nil, err
			})
		}),
		"write": js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) != 1 || args[0].Type() != js.TypeObject {
				return rejectedPromise(errors.New("write requires a Uint8Array"))
			}
			b := make([]byte, args[0].Get("length").Int())
			js.CopyBytesToGo(b, args[0])
			return makePromise(func() (any, error) {
				if _, err := c.Write(b); err != nil {
					return nil, err
				}
				return js.Undefined(), nil
			})
		}),
		"closeWrite": js.FuncOf(func(this js.Value, args []js.Value) any {
			return makePromise(func() (any, error) {
				cw, ok := c.(interface{ CloseWrite() error })
				if !ok {
					return nil, errors.New("connection does not support half-close")
				}
				if err := cw.CloseWrite(); err != nil {
					return nil, err
				}
				return js.Undefined(), nil
			})
		}),
		"close": js.FuncOf(func(this js.Value, args []js.Value) any {
			c.Close()
			return nil
		}),
	})
}

func optString(v js.Value, name string) string {
	if p := v.Get(name); p.Type() == js.TypeString {
		return p.String()
	}
	return ""
}

// makePromise runs f on a new goroutine and returns a Promise of its result (rejected on error).
func makePromise(f func() (any, error)) js.Value {
	handler := js.FuncOf(func(this js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			if res, err := f(); err == nil {
				resolve.Invoke(res)
			} else {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
			}
		}()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}

func rejectedPromise(err error) js.Value {
	return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New(err.Error()))
}
