package admin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/product"
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
	s, err := Serve(dir, sample, nil, nil)
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
	if got.UptimeS != want.UptimeS || got.Tunnel.Addr != want.Tunnel.Addr || got.Tunnel.Clients != want.Tunnel.Clients || got.Upstream != want.Upstream || got.Queue != want.Queue {
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
	s, err := Serve(dir, sample, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if runtime.GOOS == "windows" {
		// os.FileMode on Windows only reports 0666/0444 (read-only or not), so a 0600 assertion
		// can never pass there; existence is what this test can check honestly (005 fix 10k).
		for _, name := range []string{PortName, TokenName} {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		t.Log("file modes are not checked on windows: os reports 0666/0444 only")
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

// A watcher polls once a second for as long as it runs; each poll must leave nothing behind on
// the host. Found live (029): `status --watch` grew the host by one goroutine per poll — a
// kept-alive socket per throwaway client — until the watch was stopped.
func TestFetchLeavesNothingBehind(t *testing.T) {
	dir := shortDir(t)
	s, err := Serve(dir, sample, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	Fetch(ctx, dir)
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()
	for range 30 {
		if _, err := Fetch(ctx, dir); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+3 {
		t.Fatalf("goroutines %d → %d after 30 polls; each poll left a connection behind", before, after)
	}
}

func TestFetchWithoutADaemon(t *testing.T) {
	if _, err := Fetch(context.Background(), t.TempDir()); err != ErrNoDaemon {
		t.Errorf("Fetch with nothing running = %v, want ErrNoDaemon", err)
	}
}

func TestCloseRemovesTheSocket(t *testing.T) {
	dir := t.TempDir()
	s, err := Serve(dir, sample, nil, nil)
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
	if _, err := Serve(dir, sample, nil, nil); err == nil || !strings.Contains(err.Error(), "too long") {
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
	s, err := Serve(dir, sample, nil, nil)
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
	s, err := Serve(dir, sample, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := Serve(dir, sample, nil, nil); err == nil {
		t.Error("a second Serve on the same data dir succeeded")
	}
}

// Ticket 009 promise 9: POST /reload is the CLI telling a running host that keys.json changed.
// It is on the same socket as /status — never on TCP except the documented Windows loopback
// fallback, where it needs the same token (Protection 1).
func TestReloadCallsTheHook(t *testing.T) {
	dir := shortDir(t)
	var called int
	s, err := Serve(dir, sample, func() error { called++; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := Reload(ctx, dir); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if called != 1 {
		t.Fatalf("reload hook called %d times; want 1", called)
	}
	// A hook that fails is reported, not swallowed.
	s.Close()
	s2, err := Serve(dir, sample, func() error { return errors.New("keys.json is corrupt") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := Reload(ctx, dir); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("Reload with a failing hook = %v; want the failure", err)
	}
	// No host at all is ErrNoDaemon, which the CLI treats as "nothing to tell".
	if err := Reload(ctx, shortDir(t)); !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("Reload without a host = %v; want ErrNoDaemon", err)
	}
}
