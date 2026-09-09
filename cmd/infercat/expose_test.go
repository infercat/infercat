package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/bridge"
	"github.com/infercat/infercat/internal/usage"
)

func TestExposeDispatchReloadAndOff(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ic072-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	c := bridge.Config{Endpoint: "https://gateway.infercat.ai", Host: "fixture", Token: strings.Repeat("a", 64)}
	if err := bridge.Save(dir, c); err != nil {
		t.Fatal(err)
	}
	var reloads atomic.Int32
	srv, err := admin.Serve(dir, func() admin.Status { return admin.Status{} }, func() error { reloads.Add(1); return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	var out, errw bytes.Buffer
	if code := run(context.Background(), []string{"expose", "--data-dir", dir}, &out, &errw, nil, false, testPlatform("", nil)); code != 0 {
		t.Fatalf("%d: %s", code, errw.String())
	}
	if !strings.Contains(out.String(), c.URL()) || !strings.Contains(out.String(), bridge.TrustLine) {
		t.Fatal("missing URL/trust line")
	}
	if code := run(context.Background(), []string{"expose", "--data-dir", dir, "--off"}, &out, &errw, nil, false, testPlatform("", nil)); code != 0 {
		t.Fatalf("%d: %s", code, errw.String())
	}
	if _, err = os.Stat(filepath.Join(dir, bridge.FileName)); !os.IsNotExist(err) {
		t.Fatal("off retained token")
	}
	if reloads.Load() != 2 {
		t.Fatal("reload count")
	}
}
func TestExposeFlagsRefuseBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	c := bridge.Config{Endpoint: "https://gateway.infercat.ai", Host: "fixture", Token: strings.Repeat("a", 64)}
	if err := bridge.Save(dir, c); err != nil {
		t.Fatal(err)
	}
	e := &env{out: &bytes.Buffer{}, errw: &bytes.Buffer{}}
	for _, args := range [][]string{{"--off", "--register", "code"}, {"unexpected"}, {"--register", "code"}} {
		if e.cmdExpose(context.Background(), dir, args) == nil {
			t.Fatal("accepted invalid invocation")
		}
		if got, err := bridge.Load(dir); err != nil || got != c {
			t.Fatal("refusal mutated identity")
		}
	}
}
func TestBridgeRequestLine(t *testing.T) {
	if !strings.Contains(requestLine(usage.Event{Via: "bridge"}, "friend"), "via bridge") {
		t.Fatal("missing bridge provenance")
	}
	if strings.Contains(requestLine(usage.Event{}, "friend"), "via bridge") {
		t.Fatal("direct request mislabeled")
	}
}
