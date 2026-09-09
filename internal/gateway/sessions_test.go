package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
)

type peerListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *peerListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *peerListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *peerListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv6loopback, Port: 80} }

type peerConn struct {
	net.Conn
	peer *net.TCPAddr
}

func (c peerConn) RemoteAddr() net.Addr { return c.peer }

func TestSessionOwnershipExpiryAndPersistence(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, Config{DataDir: dir}, nil)
	var nanos atomic.Int64
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	nanos.Store(base.UnixNano())
	h.gw.lim.now = func() time.Time { return time.Unix(0, nanos.Load()) }
	bob := *h.key
	bob.ID = "k_bob"
	h.store.set("bob-secret", &bob)
	l := &peerListener{conns: make(chan net.Conn), done: make(chan struct{})}
	ended := make(chan error, 1)
	go func() { ended <- h.gw.Serve(l) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.gw.Shutdown(ctx)
		select {
		case err := <-ended:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Error("Serve did not stop")
		}
	})
	const a = "fd7a:115c:a1e0::a"
	const b = "fd7a:115c:a1e0::b"
	var ports atomic.Int32
	request := func(peer, secret string, want int) {
		t.Helper()
		tr := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			client, server := net.Pipe()
			wrapped := peerConn{server, &net.TCPAddr{IP: net.ParseIP(peer), Port: int(ports.Add(1)) + 3000}}
			select {
			case l.conns <- wrapped:
				return client, nil
			case <-ctx.Done():
				client.Close()
				server.Close()
				return nil, ctx.Err()
			}
		}}
		defer tr.CloseIdleConnections()
		req, _ := http.NewRequest("GET", "http://tunnel/me", nil)
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
		req.Header.Set("X-Forwarded-For", "fd7a:115c:a1e0::bad")
		res, err := (&http.Client{Transport: tr, Timeout: 3 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("status %d, want %d", res.StatusCode, want)
		}
	}
	expect := func(want map[string]int) {
		t.Helper()
		if got := h.gw.Sessions(); !reflect.DeepEqual(got, want) {
			t.Fatalf("sessions %v, want %v", got, want)
		}
	}
	request(a, "", 401)
	request(a, "unknown", 401)
	expect(map[string]int{})
	request(a, testSecret, 200)
	request(a, testSecret, 200)
	expect(map[string]int{"k_alice1": 1})
	request(b, testSecret, 200)
	expect(map[string]int{"k_alice1": 2})
	request(a, "bob-secret", 200)
	expect(map[string]int{"k_alice1": 1, "k_bob": 1})
	bob.Status = keys.Paused
	h.store.set("bob-secret", &bob)
	request(a, "bob-secret", 403)
	h.setKey(func(k *keys.Key) { k.Status = keys.Revoked })
	request(b, testSecret, 403)
	expect(map[string]int{"k_alice1": 1, "k_bob": 1})
	snapshot := h.gw.Sessions()
	snapshot["k_bob"] = 99
	expect(map[string]int{"k_alice1": 1, "k_bob": 1})
	nanos.Store(base.Add(59 * time.Second).UnixNano())
	request(a, "unknown", 401)
	request(b, "", 401)
	nanos.Store(base.Add(time.Minute - time.Nanosecond).UnixNano())
	expect(map[string]int{"k_alice1": 1, "k_bob": 1})
	nanos.Store(base.Add(time.Minute).UnixNano())
	expect(map[string]int{})
	request(a, "bob-secret", 403)
	request(b, testSecret, 403)
	nanos.Store(base.Add(2 * time.Minute).UnixNano())
	request(a, "bob-secret", 403)
	h.gw.mu.Lock()
	remaining := len(h.gw.sessions)
	h.gw.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("write retained %d expired sessions", remaining)
	}
	expect(map[string]int{"k_bob": 1})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.gw.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "usage.jsonl"))
	if err != nil || len(data) == 0 {
		t.Fatalf("usage proof: %v", err)
	}
	counters, _ := json.Marshal(h.gw.AllCounters())
	for _, raw := range [][]byte{data, counters} {
		if strings.Contains(string(raw), "fd7a:") || strings.Contains(string(raw), "sessions") {
			t.Fatal("session identity/state reached usage")
		}
	}
	restarted := New(Config{DataDir: dir}, h.up, h.store, h.rec, nil)
	if len(restarted.Sessions()) != 0 {
		t.Fatal("sessions survived restart")
	}
}

func TestDevListenerDoesNotCountSessions(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	ready := make(chan string, 1)
	h.gw.logf = func(f string, a ...any) {
		if strings.HasPrefix(f, "gateway: dev listener on http://") {
			ready <- fmt.Sprintf(f, a...)
		}
	}
	ended := make(chan error, 1)
	go func() { ended <- h.gw.ServeDev("127.0.0.1:0") }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() {
		h.gw.Shutdown(ctx)
		select {
		case err := <-ended:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Error("dev server did not stop")
		}
	}()
	var line string
	select {
	case line = <-ready:
	case <-ctx.Done():
		t.Fatal("dev server did not start")
	}
	base := strings.Fields(line)[4]
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/me", nil)
	req.Header.Set("Authorization", "Bearer "+testSecret)
	req.Header.Set("X-Forwarded-For", "fd7a:115c:a1e0::bad")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || len(h.gw.Sessions()) != 0 {
		t.Fatal("dev request counted as a tunnel session")
	}
}
