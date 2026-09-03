package tunnel

// The client side (ticket 026): one Session per host, one relay handshake, many dials — the shape
// the wasm bridge has, and the opposite of tailcat's stock per-dial client (pm/DECLINED.md). The
// secret never comes here: the tunnel carries bytes, the gateway checks keys (Protection 2).

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
	"tailscale.com/types/logger"
)

// ClientOptions is what a client may choose; the address comes from the invite.
type ClientOptions struct {
	Logf func(string, ...any) // nil = silent
}

// Session is a client's tunnel to one host. Open dials the gateway over it; Path measures it;
// Redial replaces it after the host went away, under the same identity.
type Session struct {
	cl   *tailcat.Client
	addr string
	key  key.NodePrivate
	o    ClientOptions
	once sync.Once
}

// Path is how the session reaches the host right now.
type Path struct {
	Direct bool
	Via    string // the relay's region code when relayed; the host's ip:port when direct
	RTT    time.Duration
}

// Dial brings a session to the host at addr (the tc… part of an invite) up under a fresh client
// identity; ctx bounds the relay handshake.
func Dial(ctx context.Context, addr string, o ClientOptions) (*Session, error) {
	return dial(ctx, addr, key.NewNode(), o)
}

func dial(ctx context.Context, addr string, k key.NodePrivate, o ClientOptions) (*Session, error) {
	if _, err := tailcat.ParseConnBlob(tailcat.ConnBlob(addr)); err != nil {
		return nil, fmt.Errorf("tunnel: %w", err)
	}
	logf := logger.Discard
	if o.Logf != nil {
		logf = quiet(o.Logf)
	}
	s := &Session{cl: &tailcat.Client{Server: tailcat.ConnBlob(addr), Key: k, Logf: logf}, addr: addr, key: k, o: o}
	// The first meows can be lost while either side's relay connection is still coming up
	// (tunnel_test.go pingUntil, the wasm bridge): retry until ctx says stop.
	for {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := s.cl.Ping(pctx)
		cancel()
		if err == nil {
			return s, nil
		}
		if ctx.Err() != nil {
			s.Close()
			return nil, fmt.Errorf("tunnel: the host did not answer the handshake: %w", err)
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
		}
	}
}

// Open dials the host's gateway (Port) over the session; ctx bounds it. The handshake is never
// redone: a second Open costs one TCP round trip.
func (s *Session) Open(ctx context.Context) (net.Conn, error) { return s.cl.DialTCPPort(ctx, Port) }

// Path measures the path with a disco ping, which also nudges NAT traversal along: repeated
// calls upgrade a relayed session to direct when the network allows (tailcat ping --until-direct).
func (s *Session) Path(ctx context.Context) (Path, error) {
	res, err := s.cl.DiscoPing(ctx)
	if err != nil {
		return Path{}, err
	}
	p := Path{Direct: res.Endpoint != "", Via: res.Endpoint, RTT: time.Duration(res.LatencySeconds * float64(time.Second))}
	if !p.Direct {
		p.Via = cmp.Or(res.DERPRegionCode, res.DERPRegionID.String())
	}
	return p, nil
}

// Redial closes this session and brings up a new one to the same host under the same identity
// (the host keeps seeing one client). tailcat registers a client with the host once, at the
// first meowed ack, so a host that restarted needs a new client, not a new ping.
func (s *Session) Redial(ctx context.Context) (*Session, error) {
	s.Close()
	return dial(ctx, s.addr, s.key, s.o)
}

func (s *Session) Addr() string { return s.addr }

// Close shuts the client down; every open connection breaks. Idempotent.
func (s *Session) Close() error {
	var err error
	s.once.Do(func() { err = s.cl.Close() })
	return err
}
