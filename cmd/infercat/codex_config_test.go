package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/product"
)

func TestConnectCodexMixedCleanExit(t *testing.T) {
	cfg, original := agentFixture(t)
	home := filepath.Join(filepath.Dir(cfg.DSH), "codex")
	t.Setenv("CODEX_HOME", home)
	os.MkdirAll(home, 0700)
	base := []byte("# personal\nweb_search='cached'\n")
	os.WriteFile(filepath.Join(home, "config.toml"), base, 0600)
	g := newFakeHost(t)
	plat := testPlatform(fakeAddr, nil)
	plat.dialTunnel = func(context.Context, string, func(string, ...any)) (session, error) {
		return &fakeSession{addr: g.Listener.Addr().String()}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errw lockedBuffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"connect", product.InvitePrefix + "." + fakeAddr + "." + testSecret, "--listen", "127.0.0.1:0", "--configure", "opencode,dsh,codex"}, &out, &errw, nil, false, plat)
	}()
	waitUntil(t, "Codex profile", func() bool { return strings.Contains(out.String(), "codex --profile infercat") })
	a, err := cfg.List()
	if err != nil || strings.Join(a, ",") != "opencode,dsh,codex" {
		t.Fatal(a, err)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal(code, errw.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("exit hung")
	}
	if _, err = os.Stat(filepath.Join(home, "infercat.config.toml")); !os.IsNotExist(err) {
		t.Fatal("profile remains")
	}
	b, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if string(b) != string(base) {
		t.Fatal("base changed")
	}
	b, _ = os.ReadFile(cfg.DSH)
	if string(b) != original {
		t.Fatal("DSH changed")
	}
}
func TestConnectMixedCodexRollback(t *testing.T) {
	cfg, _ := agentFixture(t)
	home := filepath.Join(filepath.Dir(cfg.DSH), "codex")
	t.Setenv("CODEX_HOME", home)
	// Codex is written first, then unsafe DSH insertion fails. The command removes its profile.
	os.WriteFile(cfg.DSH, []byte("llm-pi-ai: {}\n"), 0600)
	g := newFakeHost(t)
	plat := testPlatform(fakeAddr, nil)
	plat.dialTunnel = func(context.Context, string, func(string, ...any)) (session, error) {
		return &fakeSession{addr: g.Listener.Addr().String()}, nil
	}
	r := exec(t, plat, "connect", product.InvitePrefix+"."+fakeAddr+"."+testSecret, "--listen", "127.0.0.1:0", "--configure", "codex,dsh")
	if r.code == 0 {
		t.Fatal("unsafe DSH accepted")
	}
	if a, err := cfg.List(); err != nil || len(a) != 0 {
		t.Fatal(a, err)
	}
	if _, err := os.Stat(filepath.Join(home, "infercat.config.toml")); !os.IsNotExist(err) {
		t.Fatal("partial profile left behind")
	}
}
