// Real wiring of ticket 001's tunnel/invite and ticket 002's gateway into the CLI.

package main

import (
	"context"
	"net"

	"github.com/2185Lab/bunny-network/internal/gateway"
	"github.com/2185Lab/bunny-network/internal/invite"
	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/tunnel"
	"github.com/2185Lab/bunny-network/internal/upstream"
	"github.com/2185Lab/bunny-network/internal/usage"
)

func newPlatform() platform {
	return platform{
		startTunnel: func(ctx context.Context, o tunnelOptions) (tunnelServer, error) {
			s, err := tunnel.Start(ctx, tunnel.Options{
				DataDir:    o.DataDir,
				Ephemeral:  o.Ephemeral,
				DERPMapURL: o.DERPMapURL,
				Region:     o.Region,
				Logf:       o.Logf,
			})
			if err != nil {
				return nil, err
			}
			return realTunnel{s}, nil
		},
		savedAddr:    tunnel.SavedAddr,
		encodeInvite: invite.Encode,
		newGateway: func(o gatewayOptions, up upstream.Upstream, store keys.Store, rec usage.Recorder, logf func(string, ...any)) (gatewayServer, error) {
			return gateway.New(gateway.Config{
				LogPrompts:  o.LogPrompts,
				HostName:    o.HostName,
				RelayRegion: o.RelayRegion,
			}, up, store, rec, logf), nil
		},
	}
}

// realTunnel adapts tunnel.Status to the CLI's own struct so nothing outside this file needs
// the tunnel package.
type realTunnel struct{ s *tunnel.Server }

func (t realTunnel) Listener() net.Listener { return t.s.Listener() }
func (t realTunnel) Addr() string           { return t.s.Addr() }
func (t realTunnel) Close() error           { return t.s.Close() }
func (t realTunnel) Status() tunnelStatus {
	st := t.s.Status()
	return tunnelStatus{Addr: st.Addr, Region: st.RegionName, Started: st.Started, Clients: st.Clients}
}
