//go:build js && wasm

// Command wasm is the browser side of the tunnel: tailcat's client compiled to WebAssembly and
// exposed as window.InfercatTunnel (docs/ARCHITECTURE.md §wasm bridge). connect() brings up one
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
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
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

// liveFuncs counts js.FuncOf handles not yet Released: every per-call promise handler and every
// method of a live Session or Conn. It exists so a harness can prove nothing leaks (005 fix 10l).
var liveFuncs atomic.Int32

// fn is js.FuncOf with the leak counter; release is the matching Release.
func fn(f func(this js.Value, args []js.Value) any) js.Func {
	liveFuncs.Add(1)
	return js.FuncOf(f)
}

func release(funcs ...js.Func) {
	for _, f := range funcs {
		f.Release()
		liveFuncs.Add(-1)
	}
}

// Inert method stubs installed on a Conn/Session object when it closes, so a post-close call from
// JS is a clean no-op instead of "call to released function" once the real handles are Released
// (005 fix 10l). Created once, never released, never counted; a bounded fixed cost.
var (
	stubNull   = js.FuncOf(func(js.Value, []js.Value) any { return resolved(js.Null()) })        // read() → EOF
	stubClosed = js.FuncOf(func(js.Value, []js.Value) any { return rejectedPromise(errClosed) }) // write/ping/dial
	stubNoop   = js.FuncOf(func(js.Value, []js.Value) any { return js.Undefined() })             // close()
)

var errClosed = errors.New("the connection is closed")

func resolved(v js.Value) js.Value { return js.Global().Get("Promise").Call("resolve", v) }

func main() {
	js.Global().Set("InfercatTunnel", map[string]any{
		"connect": fn(connect),
		// stats is a debug hook for leak checks: live js.Func handles and the Go heap after a GC.
		"stats": fn(func(this js.Value, args []js.Value) any {
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			return map[string]any{"liveFuncs": int(liveFuncs.Load()), "heapAllocBytes": int(m.HeapAlloc)}
		}),
	})
	select {}
}

// connect implements InfercatTunnel.connect(opts): see the TS interface in docs/ARCHITECTURE.md.
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
		ci, err := tailcat.ParseAddr(tailcat.Addr(addr))
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
		cl := &tailcat.Client{Server: tailcat.Addr(addr), Key: pk.Private, Logf: logf, DERPMapURL: derpMapURL}
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

// newSession is the Session object: addr, privateKeyJSON, dial(port?), ping(), close(). close()
// also Releases the three method handles (once), so a session must be closed to be collected.
func newSession(cl *tailcat.Client, relay, addr, keyJSON string) js.Value {
	var funcs = make([]js.Func, 3)
	var closeOnce sync.Once
	funcs[0] = fn(func(this js.Value, args []js.Value) any {
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
	})
	funcs[1] = fn(func(this js.Value, args []js.Value) any {
		return makePromise(func() (any, error) { return ping(cl, relay) })
	})
	obj := js.ValueOf(map[string]any{
		"addr":           addr,
		"privateKeyJSON": keyJSON,
		"dial":           funcs[0],
		"ping":           funcs[1],
	})
	funcs[2] = fn(func(this js.Value, args []js.Value) any {
		closeOnce.Do(func() {
			obj.Set("dial", stubClosed)
			obj.Set("ping", stubClosed)
			obj.Set("close", stubNoop)
			cl.Close()
			release(funcs...)
		})
		return nil
	})
	obj.Set("close", funcs[2])
	return obj
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
// (the address encoding strips the code), or the bare region ID.
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
// sender stalls on TCP backpressure rather than filling browser memory. close() (once) also
// Releases the four method handles and drops the read buffer, so every Conn must be closed —
// the web client does so after EOF and on every error path (005 fix 10l).
func makeJSConn(c net.Conn) js.Value {
	buf := make([]byte, 64<<10)
	var funcs = make([]js.Func, 4)
	var closeOnce sync.Once
	funcs[0] = fn(func(this js.Value, args []js.Value) any {
		return makePromise(func() (any, error) {
			if buf == nil {
				return js.Null(), nil // closed: clean EOF
			}
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
	})
	funcs[1] = fn(func(this js.Value, args []js.Value) any {
		// Anything but a Uint8Array (ArrayBuffer, DataView, {}, an array) would panic in
		// CopyBytesToGo and take the whole program down (005 fix 10m).
		if len(args) != 1 || !args[0].InstanceOf(js.Global().Get("Uint8Array")) {
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
	})
	funcs[2] = fn(func(this js.Value, args []js.Value) any {
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
	})
	obj := js.ValueOf(map[string]any{
		"read":       funcs[0],
		"write":      funcs[1],
		"closeWrite": funcs[2],
	})
	funcs[3] = fn(func(this js.Value, args []js.Value) any {
		closeOnce.Do(func() {
			obj.Set("read", stubNull) // a post-close read is a clean EOF; write/closeWrite reject
			obj.Set("write", stubClosed)
			obj.Set("closeWrite", stubClosed)
			obj.Set("close", stubNoop)
			c.Close()
			release(funcs...)
			buf = nil
		})
		return nil
	})
	obj.Set("close", funcs[3])
	return obj
}

func optString(v js.Value, name string) string {
	if p := v.Get(name); p.Type() == js.TypeString {
		return p.String()
	}
	return ""
}

// makePromise runs f on a new goroutine and returns a Promise of its result (rejected on error).
// The executor handle is Released once the promise has settled (005 fix 10l).
func makePromise(f func() (any, error)) js.Value {
	var handler js.Func
	handler = fn(func(this js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			defer release(handler)
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
