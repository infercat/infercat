// Real wiring of ticket 001's tunnel/invite and ticket 002's gateway into the CLI.

package main

import (
	"context"
	"net"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/gateway"
	"github.com/infercat/infercat/internal/invite"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/tunnel"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

func newPlatform() platform {
	return platform{
		startTunnel: func(ctx context.Context, o tunnelOptions) (tunnelServer, error) {
			s, err := tunnel.Start(ctx, o)
			if err != nil {
				return nil, err
			}
			return realTunnel{s}, nil
		},
		dialTunnel: func(ctx context.Context, addr string, logf func(string, ...any)) (session, error) {
			s, err := tunnel.Dial(ctx, addr, tunnel.ClientOptions{Logf: logf})
			if err != nil {
				return nil, err
			}
			return tunnelSession{s}, nil
		},
		savedAddr:    tunnel.SavedAddr,
		encodeInvite: invite.Encode,
		newGateway: func(o gatewayOptions, up upstream.Upstream, store keys.Store, rec usage.Recorder, logf func(string, ...any)) (gatewayServer, error) {
			return gateway.New(o, up, store, rec, logf), nil
		},
	}
}

// realTunnel adapts tunnel.Status to the CLI's status view.
type realTunnel struct{ s *tunnel.Server }

func (t realTunnel) Listener() net.Listener { return t.s.Listener() }
func (t realTunnel) Addr() string           { return t.s.Addr() }
func (t realTunnel) Close() error           { return t.s.Close() }
func (t realTunnel) Status() tunnelStatus {
	st := t.s.Status()
	return tunnelStatus{Addr: st.Addr, Region: st.RegionName, Started: st.Started, Clients: st.Clients}
}
func (t realTunnel) Peers() []admin.Session {
	out := []admin.Session{} // never null on the wire: 028 reads this shape
	for _, p := range t.s.Peers() {
		out = append(out, admin.Session{Key: p.Addr.String(), Path: "unknown", Conns: p.Conns, RxBytes: p.RxBytes, TxBytes: p.TxBytes,
			Since: p.Since, LastByte: p.LastByte, Active: time.Since(p.LastByte) < 2*time.Minute})
	}
	return out
}
