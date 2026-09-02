package tunnel

import (
	"net"
	"net/netip"
	"sync"
)

// maxParked bounds connections that netstack has handed over but nobody has accepted yet; past
// it new ones are closed rather than queued (an acceptor that stalls should not grow memory).
const maxParked = 1024

// listener turns tailcat's per-connection OnTCP callbacks into a net.Listener: deliver parks a
// connection until Accept hands it out wrapped (open ones are counted per client for Status).
// Every state change happens under mu, so a connection delivered after Close is closed, never parked.
type listener struct {
	s    *Server
	done chan struct{} // closed by Close
	wake chan struct{} // nudges Accept when parked grows
	once sync.Once

	mu       sync.Mutex
	isClosed bool
	parked   []net.Conn
	open     map[netip.Addr]int // open accepted connections per client tunnel address
}

func newListener(s *Server) *listener {
	return &listener{s: s, done: make(chan struct{}), wake: make(chan struct{}, 1), open: map[netip.Addr]int{}}
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
			tc := &trackedConn{Conn: c, l: l, key: peerKey(c)}
			l.open[tc.key]++
			l.mu.Unlock()
			return tc, nil
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
	return len(l.open)
}

func (l *listener) release(key netip.Addr) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open[key]--; l.open[key] <= 0 {
		delete(l.open, key)
	}
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
	key  netip.Addr
	once sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { c.l.release(c.key) })
	return c.Conn.Close()
}

// CloseWrite keeps TCP half-close available through the wrapper (net/http and tailcat use it).
func (c *trackedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return net.ErrClosed
}
