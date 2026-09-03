package tunnel

import (
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// maxParked bounds connections that netstack has handed over but nobody has accepted yet; past
// it new ones are closed rather than queued (an acceptor that stalls should not grow memory).
const maxParked = 1024

// listener turns tailcat's per-connection OnTCP callbacks into a net.Listener: deliver parks a
// connection until Accept hands it out wrapped, and every accepted connection is counted and
// metered per client address (Status, Peers). Every state change happens under mu, so a
// connection delivered after Close is closed, never parked.
type listener struct {
	s    *Server
	done chan struct{} // closed by Close
	wake chan struct{} // nudges Accept when parked grows
	once sync.Once

	mu       sync.Mutex
	isClosed bool
	parked   []net.Conn
	stats    map[netip.Addr]*peerStat // every client address ever accepted, by tunnel address
}

// peerStat is one client as the listener has met it (029 promise 3, as far as the host can
// see: tailcat 0.4.0 keeps WireGuard peer state — path, handshake — on the client side only).
type peerStat struct {
	open   int // accepted connections not yet closed; under listener.mu
	since  time.Time
	rx, tx atomic.Int64
	last   atomic.Int64 // unix nanoseconds of the last byte either way
}

func newListener(s *Server) *listener {
	return &listener{s: s, done: make(chan struct{}), wake: make(chan struct{}, 1), stats: map[netip.Addr]*peerStat{}}
}

// deliver is the OnTCP handler for Port.
func (l *listener) deliver(c net.Conn) {
	l.mu.Lock()
	if l.isClosed || len(l.parked) >= maxParked {
		l.mu.Unlock()
		c.Close()
		return
	}
	l.parked = append(l.parked, c)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *listener) Accept() (net.Conn, error) {
	for {
		l.mu.Lock()
		if l.isClosed {
			l.mu.Unlock()
			return nil, net.ErrClosed
		}
		if len(l.parked) > 0 {
			c := l.parked[0]
			l.parked = l.parked[1:]
			key := peerKey(c)
			ps := l.stats[key]
			if ps == nil {
				ps = &peerStat{since: time.Now()}
				l.stats[key] = ps
			}
			ps.open++
			l.mu.Unlock()
			return &trackedConn{Conn: c, l: l, ps: ps}, nil
		}
		l.mu.Unlock()
		select {
		case <-l.wake:
		case <-l.done:
		}
	}
}

// Close stops accepting: later connections to Port get a RST from onTCP, parked ones are closed.
func (l *listener) Close() error {
	l.once.Do(func() {
		l.mu.Lock()
		l.isClosed = true
		parked := l.parked
		l.parked = nil
		l.mu.Unlock()
		close(l.done)
		for _, c := range parked {
			c.Close()
		}
	})
	return nil
}

func (l *listener) Addr() net.Addr {
	return &net.TCPAddr{IP: l.s.tc.Addr().AsSlice(), Port: Port}
}

func (l *listener) closed() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

func (l *listener) clients() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, ps := range l.stats {
		if ps.open > 0 {
			n++
		}
	}
	return n
}

// peers is every client address ever accepted, oldest first, with its counters as of now.
func (l *listener) peers() []Peer {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Peer, 0, len(l.stats))
	for a, ps := range l.stats {
		p := Peer{Addr: a, Conns: ps.open, RxBytes: ps.rx.Load(), TxBytes: ps.tx.Load(), Since: ps.since}
		if n := ps.last.Load(); n > 0 {
			p.LastByte = time.Unix(0, n)
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Peer) int { return a.Since.Compare(b.Since) })
	return out
}

func (l *listener) release(ps *peerStat) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ps.open--
}

// peerKey is the client's tunnel address (derived from its node key), or the zero Addr if the
// connection has no remote address yet.
func peerKey(c net.Conn) netip.Addr {
	if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok && ta != nil {
		if a, ok := netip.AddrFromSlice(ta.IP); ok {
			return a.Unmap()
		}
	}
	return netip.Addr{}
}

type trackedConn struct {
	net.Conn
	l    *listener
	ps   *peerStat
	once sync.Once
}

func (c *trackedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.ps.rx.Add(int64(n))
		c.ps.last.Store(time.Now().UnixNano())
	}
	return n, err
}

func (c *trackedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.ps.tx.Add(int64(n))
		c.ps.last.Store(time.Now().UnixNano())
	}
	return n, err
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { c.l.release(c.ps) })
	return c.Conn.Close()
}

// CloseWrite keeps TCP half-close available through the wrapper (net/http and tailcat use it).
func (c *trackedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return net.ErrClosed
}
