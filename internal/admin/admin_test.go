package admin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/2185Lab/bunny-network/internal/product"
)

// shortDir keeps the socket path under the kernel's sun_path limit; t.TempDir() names itself
// after the test, which for a long test name overflows it.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "bnadm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func sample() Status {
	return Status{
		UptimeS:  42,
		Tunnel:   Tunnel{Addr: "tcABC", Region: "sfo", Clients: 1},
		Upstream: Upstream{Kind: "llama.cpp", URL: "http://127.0.0.1:8080", Healthy: true, ModelContext: 4096, Slots: 2},
		Queue:    Queue{InFlight: 1, Waiting: 2},
		Keys:     []Key{{ID: "k_1", Name: "alice", Status: "active", InFlight: 1, RPMUsed: 3, TodayTokens: 12004, LastSeen: time.Unix(1, 0).UTC()}},
	}
}

func TestServeAndFetchRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Serve(dir, sample)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer s.Close()

	got, err := Fetch(context.Background(), dir)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Product != product.Name || got.Version != product.Version {
		t.Errorf("product/version = %q %q, want the constants", got.Product, got.Version)
	}
	want := sample()
	if got.UptimeS != want.UptimeS || got.Tunnel != want.Tunnel || got.Upstream != want.Upstream || got.Queue != want.Queue {
		t.Errorf("status = %+v", got)
	}
	if len(got.Keys) != 1 || got.Keys[0].ID != "k_1" || got.Keys[0].TodayTokens != 12004 {
		t.Errorf("keys = %+v", got.Keys)
	}
	if !got.Keys[0].LastSeen.Equal(want.Keys[0].LastSeen) {
		t.Errorf("last seen = %v", got.Keys[0].LastSeen)
	}
}

// Protection 1: the admin API is never a TCP port a friend could reach. On unix that means a
// socket only the host's own user can open.
func TestSocketIsPrivate(t *testing.T) {
	dir := t.TempDir()
	s, err := Serve(dir, sample)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if runtime.GOOS == "windows" {
		port, err := os.Stat(filepath.Join(dir, PortName))
		if err != nil {
			t.Fatalf("admin.port: %v", err)
		}
		if port.Mode().Perm() != 0o600 {
			t.Errorf("admin.port mode = %v, want 0600", port.Mode().Perm())
		}
		tok, err := os.Stat(filepath.Join(dir, TokenName))
		if err != nil {
			t.Fatalf("admin.token: %v", err)
		}
		if tok.Mode().Perm() != 0o600 {
			t.Errorf("admin.token mode = %v, want 0600", tok.Mode().Perm())
		}
		return
	}
	fi, err := os.Stat(filepath.Join(dir, SockName))
	if err != nil {
		t.Fatalf("admin.sock: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("admin.sock mode = %v, want 0600", fi.Mode().Perm())
	}
	if fi.Mode()&os.ModeSocket == 0 {
		t.Errorf("admin.sock is not a socket: %v", fi.Mode())
	}
}

func TestFetchWithoutADaemon(t *testing.T) {
	if _, err := Fetch(context.Background(), t.TempDir()); err != ErrNoDaemon {
		t.Errorf("Fetch with nothing running = %v, want ErrNoDaemon", err)
	}
}

func TestCloseRemovesTheSocket(t *testing.T) {
	dir := t.TempDir()
	s, err := Serve(dir, sample)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, n := range []string{SockName, PortName, TokenName} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s survived Close", n)
		}
	}
	if _, err := Fetch(context.Background(), dir); err != ErrNoDaemon {
		t.Errorf("Fetch after Close = %v, want ErrNoDaemon", err)
	}
}

func TestTooLongDataDirSaysWhy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sun_path limit on the windows fallback")
	}
	dir := filepath.Join(shortDir(t), strings.Repeat("x", 120))
	if _, err := Serve(dir, sample); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("err = %v, want a clear complaint about the path length", err)
	}
}

// A socket left behind by a killed host must not block the next start.
func TestStaleSocketIsReplaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no socket file on windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SockName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Serve(dir, sample)
	if err != nil {
		t.Fatalf("Serve over a stale socket: %v", err)
	}
	defer s.Close()
	if _, err := Fetch(context.Background(), dir); err != nil {
		t.Errorf("Fetch: %v", err)
	}
}

// Two hosts on one data dir would fight over keys.json and the tunnel; the second must refuse.
func TestSecondHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the windows fallback has no socket to collide on")
	}
	dir := shortDir(t)
	s, err := Serve(dir, sample)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := Serve(dir, sample); err == nil {
		t.Error("a second Serve on the same data dir succeeded")
	}
}
