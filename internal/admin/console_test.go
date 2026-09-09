package admin

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestConsoleRequiresTokenForEveryRoute(t *testing.T) {
	dir := shortDir(t)
	s, err := Serve(dir, sample, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) }))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l, err := ListenConsole("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := s.ServeConsole(l, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("console: slice 1 pending")}})
	defer srv.Close()
	hc, base, token, err := dial(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 43 {
		t.Fatalf("token length %d", len(token))
	}
	for _, surface := range []struct {
		client *http.Client
		base   string
	}{{hc, base}, {http.DefaultClient, "http://" + l.Addr().String() + "/api"}} {
		for _, route := range []string{"GET /status", "GET /events", "POST /reload", "GET /keys", "GET /keys/k_1", "POST /keys", "POST /keys/k_1/pause", "POST /keys/k_1/resume", "POST /keys/k_1/revoke", "POST /keys/k_1/rotate", "PATCH /keys/k_1", "GET /usage", "GET /engine", "GET /settings", "GET /unknown"} {
			for _, auth := range []string{"", "Bearer wrong"} {
				parts := strings.Split(route, " ")
				req, _ := http.NewRequest(parts[0], surface.base+parts[1], nil)
				req.Header.Set("Authorization", auth)
				resp, err := surface.client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != 401 {
					t.Fatalf("%s %s: %d", surface.base, route, resp.StatusCode)
				}
			}
		}
	}
	req, _ := http.NewRequest("GET", "http://"+l.Addr().String()+"/api/keys", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("authorized response: %v", resp)
	}
	req.Host = "rebound.invalid"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal(resp.Status)
	}
	resp, err = http.Get("http://" + l.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "slice 1 pending") || strings.Contains(string(body), token) {
		t.Fatal("bundle or token leak")
	}
}

func TestConsoleListenRefusal(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:9101", "[::]:9101", "192.168.1.1:9101", "localhost:9101", "127.0.0.1", "127.0.0.1:99999"} {
		if l, err := ListenConsole(addr); err == nil {
			l.Close()
			t.Fatalf("accepted %s", addr)
		}
	}
	if l, err := ListenConsole("off"); err != nil || l != nil {
		t.Fatalf("off: %v %v", l, err)
	}
}

func TestOldCloseCannotRemoveSuccessorToken(t *testing.T) {
	dir := shortDir(t)
	s, err := Serve(dir, sample, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(filepath.Join(dir, TokenName))
	s.Close()
	next, err := Serve(dir, sample, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	s.Close()
	current, err := os.ReadFile(filepath.Join(dir, TokenName))
	if err != nil || string(old) == string(current) {
		t.Fatal("missing or reused per-run token", err)
	}
	if _, err := Fetch(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
}

// Hold the old Serve goroutine before entry, without scheduler timing or sleeps. Shutdown
// only closes registered HTTP listeners; a delayed Serve must not unlink its successor.
func TestCloseBeforeHTTPServeCannotRemoveSuccessorSocket(t *testing.T) {
	dir := shortDir(t)
	listener, _, clean, err := listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	observed := &closeObservedListener{Listener: listener}
	old := &Server{l: observed, srv: &http.Server{}, clean: clean, done: make(chan struct{})}
	defer old.Close()
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if !observed.closed {
		t.Error("Close returned before closing its unregistered listener")
	}
	next, err := Serve(dir, sample, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	// This is the old goroutine's first opportunity to execute, after replacement is bound.
	if err := old.srv.Serve(observed); !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
	if _, err := Fetch(context.Background(), dir); err != nil {
		t.Fatalf("late old Serve removed the successor socket: %v", err)
	}
}

type closeObservedListener struct {
	net.Listener
	closed bool
}

func (l *closeObservedListener) Close() error {
	l.closed = true
	return l.Listener.Close()
}
