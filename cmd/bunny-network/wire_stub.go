//go:build !wire

// Stand-in wiring used until tickets 001 (tunnel, invite) and 002 (gateway) land. It exists so
// the parts this ticket owns — key store, upstream detection, usage, admin status, CLI parsing,
// help, cross-compilation — can be built and exercised for real today. It carries no tunnel and
// no gateway, says so at the top of every `serve`, and hands out a placeholder address that names
// itself. Build with `-tags wire` (wire.go) for the real thing; then delete this file.

package main

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/product"
	"github.com/2185Lab/bunny-network/internal/upstream"
	"github.com/2185Lab/bunny-network/internal/usage"
)

const notWiredWarning = `WARNING: this binary was built without -tags wire, so it has NO tunnel and NO gateway
         (tickets 001 and 002). Nothing your friends hold can reach it and every invite it
         prints carries a placeholder address. For local verification only.`

// placeholderAddr is deliberately not a valid tailcat ConnBlob and says what it is.
const placeholderAddr = "tc-no-tunnel-in-this-build"

func newPlatform() platform {
	return platform{
		warn: notWiredWarning,
		startTunnel: func(ctx context.Context, o tunnelOptions) (tunnelServer, error) {
			return &stubTunnel{l: newIdleListener(), started: time.Now()}, nil
		},
		savedAddr: func(dataDir string) (string, error) { return placeholderAddr, nil },
		// docs/ARCHITECTURE.md §Invite format; ticket 001's invite.Encode replaces this.
		encodeInvite: func(addr, secret string) string {
			return product.InvitePrefix + "." + addr + "." + secret
		},
		newGateway: func(o gatewayOptions, up upstream.Upstream, store keys.Store, rec usage.Recorder, logf func(string, ...any)) (gatewayServer, error) {
			return &stubGateway{done: make(chan struct{})}, nil
		},
	}
}

type stubTunnel struct {
	l       *idleListener
	started time.Time
}

func (t *stubTunnel) Listener() net.Listener { return t.l }
func (t *stubTunnel) Addr() string           { return placeholderAddr }
func (t *stubTunnel) Close() error           { return t.l.Close() }
func (t *stubTunnel) Status() tunnelStatus {
	return tunnelStatus{Addr: placeholderAddr, Region: "none", Started: t.started}
}

// idleListener accepts nothing and unblocks on Close.
type idleListener struct {
	done chan struct{}
	once sync.Once
}

func newIdleListener() *idleListener { return &idleListener{done: make(chan struct{})} }

func (l *idleListener) Accept() (net.Conn, error) { <-l.done; return nil, net.ErrClosed }
func (l *idleListener) Close() error              { l.once.Do(func() { close(l.done) }); return nil }
func (l *idleListener) Addr() net.Addr            { return stubAddr{} }

type stubAddr struct{}

func (stubAddr) Network() string { return "none" }
func (stubAddr) String() string  { return placeholderAddr }

type stubGateway struct {
	done chan struct{}
	once sync.Once
}

func (g *stubGateway) Serve(net.Listener) error { <-g.done; return nil }
func (g *stubGateway) ServeDev(string) error    { <-g.done; return nil }
func (g *stubGateway) Shutdown(context.Context) error {
	g.once.Do(func() { close(g.done) })
	return nil
}
func (g *stubGateway) Counters(string) usage.KeyCounters         { return usage.KeyCounters{} }
func (g *stubGateway) AllCounters() map[string]usage.KeyCounters { return nil }
func (g *stubGateway) Queue() (int, int)                         { return 0, 0 }
