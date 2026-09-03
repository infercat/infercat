package tunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/envknob"
)

// Ticket 026 promise 1 at the tunnel level: one Dial is one handshake; Open reuses it for every
// request; Path says how the bytes travel; Redial replaces a dead session under the same identity;
// a host that is not there fails the handshake within the caller's bound, never hangs.
func TestSessionOpenPathRedial(t *testing.T) {
	mapURL := localDERP(t)
	s := startOK(t, Options{DataDir: t.TempDir(), DERPMapURL: mapURL, Region: "1", Logf: mkLogf(t, "server")})
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) })
	go http.Serve(s.Listener(), mux)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := Dial(ctx, "not-an-address", ClientOptions{}); err == nil {
		t.Fatal("Dial accepted a bad address")
	}
	t0 := time.Now()
	cl, err := Dial(ctx, s.Addr(), ClientOptions{Logf: mkLogf(t, "client")})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { cl.Close() })
	t.Logf("Dial (handshake via the local relay): %v", time.Since(t0).Round(time.Millisecond))

	get := func(cl *Session) (string, error) {
		hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return cl.Open(ctx)
		}, DisableKeepAlives: true}, Timeout: 15 * time.Second}
		resp, err := hc.Get("http://tunnel/healthz")
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}
	for i := range 3 {
		t1 := time.Now()
		body, err := get(cl)
		if err != nil || body != `{"ok":true}` {
			t.Fatalf("GET %d through the session = %q, %v", i, body, err)
		}
		t.Logf("GET %d: %v (no re-handshake)", i, time.Since(t1).Round(time.Millisecond))
	}
	// The host's side of the same session (029 promise 3): one peer under the client's tunnel
	// address, bytes both ways, a last byte, a first-seen time that does not move between calls.
	peers := s.Peers()
	if len(peers) != 1 || !peers[0].Addr.IsValid() || peers[0].RxBytes == 0 || peers[0].TxBytes == 0 || peers[0].LastByte.IsZero() {
		t.Fatalf("Peers = %+v; want one with an address, bytes both ways and a last byte", peers)
	}
	if again := s.Peers(); !again[0].Since.Equal(peers[0].Since) || again[0].Addr != peers[0].Addr {
		t.Fatalf("Peers again = %+v; want the same Since and address", again)
	}
	t.Logf("Peer as the host sees it: %+v", peers[0])
	p, err := cl.Path(ctx)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	t.Logf("Path: direct=%v via=%q rtt=%v", p.Direct, p.Via, p.RTT)
	if p.Via == "" || p.RTT <= 0 {
		t.Fatalf("Path = %+v; want a via and an rtt", p)
	}
	if !p.Direct && p.Via != "test" {
		t.Fatalf("relayed path names %q; the local relay's code is \"test\"", p.Via)
	}

	// Redial: same identity, a working session; the old one is closed.
	n, err := cl.Redial(ctx)
	if err != nil {
		t.Fatalf("Redial: %v", err)
	}
	t.Cleanup(func() { n.Close() })
	if !n.key.Equal(cl.key) || n.Addr() != cl.Addr() {
		t.Fatal("Redial changed the client identity or the host")
	}
	if _, err := get(cl); err == nil {
		t.Fatal("the old session still serves after Redial")
	}
	if body, err := get(n); err != nil || body != `{"ok":true}` {
		t.Fatalf("GET through the redialled session = %q, %v", body, err)
	}
	if err := n.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := n.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	// A host that is gone: the handshake fails within the bound, with a sentence about the host.
	s.Close()
	bctx, bcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer bcancel()
	t2 := time.Now()
	_, err = Dial(bctx, s.Addr(), ClientOptions{})
	if err == nil || !strings.Contains(err.Error(), "did not answer") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial to a stopped host = %v; want a handshake error wrapping the deadline", err)
	}
	if took := time.Since(t2); took > 6*time.Second {
		t.Fatalf("Dial to a stopped host took %v; the bound was 3s", took)
	}
	t.Logf("Dial to a stopped host: %v (%v)", err, time.Since(t2).Round(time.Millisecond))
}

// Ticket 035: a relay-only client (UDP off — TS_DEBUG_ALWAYS_USE_DERP, the browser's shape and
// connect's on a network that never yields a direct path) can park for minutes inside tailcat's
// Close. Session.Close is bounded whatever happens underneath: a close that parks answers
// ErrCloseTimeout at the bound and the caller goes on; a second Close answers at once; and a real
// relay-only session against a host on the local relay closes within the bound.
func TestSessionCloseIsBounded(t *testing.T) {
	prev := closeTimeout
	closeTimeout = 300 * time.Millisecond
	parked := &Session{closeFn: func() error { select {} }}
	t0 := time.Now()
	err := parked.Close()
	took := time.Since(t0)
	if !errors.Is(err, ErrCloseTimeout) || took > 2*closeTimeout {
		t.Fatalf("a parked close: %v after %v; want ErrCloseTimeout at ~%v", err, took, closeTimeout)
	}
	t1 := time.Now()
	if err := parked.Close(); !errors.Is(err, ErrCloseTimeout) || time.Since(t1) > 50*time.Millisecond {
		t.Fatalf("second Close = %v after %v; want the same answer at once", err, time.Since(t1))
	}
	t.Logf("parked close returned %q after %v", err, took.Round(time.Millisecond))
	closeTimeout = prev

	// Live: the host has UDP, the client does not (the knob is read at the client's bind).
	mapURL := localDERP(t)
	s := startOK(t, Options{DataDir: t.TempDir(), DERPMapURL: mapURL, Region: "1", Logf: mkLogf(t, "server")})
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) })
	go http.Serve(s.Listener(), mux)
	envknob.SetenvForTest(t, "TS_DEBUG_ALWAYS_USE_DERP", "true")
	var udpOff atomic.Bool
	clientLog := mkLogf(t, "client")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cl, err := Dial(ctx, s.Addr(), ClientOptions{Logf: func(f string, a ...any) {
		if strings.Contains(f, "TS_DEBUG_ALWAYS_USE_DERP") {
			udpOff.Store(true)
		}
		clientLog(f, a...)
	}})
	if err != nil {
		t.Fatalf("Dial relay-only: %v", err)
	}
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return cl.Open(ctx)
	}, DisableKeepAlives: true}, Timeout: 15 * time.Second}
	for i := range 3 {
		resp, err := hc.Get("http://tunnel/healthz")
		if err != nil {
			t.Fatalf("GET %d over the relay-only session: %v", i, err)
		}
		resp.Body.Close()
	}
	if !udpOff.Load() {
		t.Fatal("the client never logged that UDP was disabled; the knob did not take")
	}
	if p, err := cl.Path(ctx); err == nil && p.Direct {
		t.Fatalf("a relay-only client reports a direct path: %+v", p)
	} else {
		t.Logf("relay-only path: %+v (%v)", p, err)
	}
	t2 := time.Now()
	err = cl.Close()
	took = time.Since(t2)
	t.Logf("relay-only Close: %v after %v (bound %v)", err, took.Round(time.Millisecond), closeTimeout)
	if took > closeTimeout+2*time.Second {
		t.Fatalf("relay-only Close took %v; the bound is %v", took, closeTimeout)
	}
	if err != nil && !errors.Is(err, ErrCloseTimeout) {
		t.Fatalf("Close: %v", err)
	}
}
