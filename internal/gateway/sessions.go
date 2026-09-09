package gateway

import (
	"context"
	"net"
	"net/netip"
	"time"
)

type sessionContextKey struct{}
type sessionSeen struct {
	keyID string
	at    time.Time
}

// A tunnel peer's node-derived IP survives its per-request TCP connections; ports do not.
// Only Serve installs this hook. Neither request headers nor the dev listener supply identity.
func sessionContext(ctx context.Context, c net.Conn) context.Context {
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok && a != nil {
		if peer := a.AddrPort().Addr().Unmap(); peer.IsValid() {
			return context.WithValue(ctx, sessionContextKey{}, peer)
		}
	}
	return ctx
}

func (g *Gateway) seeSession(ctx context.Context, keyID string) {
	peer, ok := ctx.Value(sessionContextKey{}).(netip.Addr)
	if !ok {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.lim.now()
	g.pruneSessions(now)
	if g.sessions == nil {
		g.sessions = make(map[netip.Addr]sessionSeen)
	}
	g.sessions[peer] = sessionSeen{keyID, now}
}

// Caller holds g.mu. Pruning on writes also bounds stale memory when nobody asks for status.
func (g *Gateway) pruneSessions(now time.Time) {
	cutoff := now.Add(-time.Minute)
	for peer, seen := range g.sessions {
		if !seen.at.After(cutoff) {
			delete(g.sessions, peer)
		}
	}
}

// Sessions returns counts, never identities. A session belongs to its last matching key.
func (g *Gateway) Sessions() map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneSessions(g.lim.now())
	counts := make(map[string]int)
	for _, seen := range g.sessions {
		counts[seen.keyID]++
	}
	return counts
}
