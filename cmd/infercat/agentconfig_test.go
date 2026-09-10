package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/agentconfig"
	"github.com/infercat/infercat/internal/product"
)

func agentFixture(t *testing.T) (agentconfig.Config, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("AppData", root) // Windows UserConfigDir; never use the real user config in fixtures.
	t.Setenv("DSH_HOME", filepath.Join(root, "dsh"))
	cfg, err := agentconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	original := "# personal config\nllm-pi-ai:\n  providers:\n    retained: {}\n"
	os.MkdirAll(filepath.Dir(cfg.DSH), 0700)
	os.WriteFile(cfg.DSH, []byte(original), 0600)
	return cfg, original
}
func TestConnectConfigureLifecycle(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean-exit", true: "unconfigure"}[explicit], func(t *testing.T) {
			cfg, original := agentFixture(t)
			g := newFakeHost(t)
			s := &fakeSession{addr: g.Listener.Addr().String()}
			plat := testPlatform(fakeAddr, nil)
			plat.dialTunnel = func(context.Context, string, func(string, ...any)) (session, error) { return s, nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out, errw lockedBuffer
			done := make(chan int, 1)
			go func() {
				done <- run(ctx, []string{"connect", product.InvitePrefix + "." + fakeAddr + "." + testSecret, "--listen", "127.0.0.1:0", "--configure", "opencode,dsh"}, &out, &errw, nil, false, plat)
			}()
			waitUntil(t, "configured bridge banner", func() bool { return strings.Contains(out.String(), "set your app's base URL") })
			if !strings.Contains(out.String(), "OPENCODE_CONFIG=") || !strings.Contains(out.String(), "/model → infercat →") {
				t.Fatal(out.String())
			}
			r := exec(t, plat, "status", "--data-dir", t.TempDir())
			if r.code != 0 || !strings.Contains(r.out, "agents    opencode, dsh") {
				t.Fatal(r)
			}
			if explicit {
				r = exec(t, plat, "connect", "--unconfigure")
				if r.code != 0 {
					t.Fatal(r)
				}
			}
			cancel()
			select {
			case code := <-done:
				if code != 0 {
					t.Fatal(code, errw.String())
				}
			case <-time.After(8 * time.Second):
				t.Fatal("shutdown hung")
			}
			b, err := os.ReadFile(cfg.DSH)
			if err != nil || string(b) != original {
				t.Fatal(string(b), err)
			}
			if a, err := cfg.List(); err != nil || len(a) != 0 {
				t.Fatal(a, err)
			}
		})
	}
}
func TestConfigureUnknownDoesNotWrite(t *testing.T) {
	cfg, original := agentFixture(t)
	r := exec(t, testPlatform(fakeAddr, nil), "connect", "--configure", "opencode,unknown")
	if r.code == 0 || !strings.Contains(r.err, "unknown agent") {
		t.Fatal(r)
	}
	if _, err := os.Stat(cfg.Dir); !os.IsNotExist(err) {
		t.Fatal("partial config written")
	}
	b, _ := os.ReadFile(cfg.DSH)
	if string(b) != original {
		t.Fatal("DSH modified")
	}
}
func TestConfigurePartialFailureCleansOwnedWrite(t *testing.T) {
	cfg, _ := agentFixture(t)
	os.WriteFile(cfg.DSH, []byte("llm-pi-ai: {}\n"), 0600)
	g := newFakeHost(t)
	plat := testPlatform(fakeAddr, nil)
	plat.dialTunnel = func(context.Context, string, func(string, ...any)) (session, error) {
		return &fakeSession{addr: g.Listener.Addr().String()}, nil
	}
	r := exec(t, plat, "connect", product.InvitePrefix+"."+fakeAddr+"."+testSecret, "--listen", "127.0.0.1:0", "--configure", "opencode,dsh")
	if r.code == 0 {
		t.Fatal("unsafe YAML accepted")
	}
	if a, err := cfg.List(); err != nil || len(a) != 0 {
		t.Fatal(a, err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Dir, "opencode.jsonc")); !os.IsNotExist(err) {
		t.Fatal("partial OpenCode write left behind")
	}
}

func TestExistingOpenCodePathRefusesBeforeAnyWrite(t *testing.T) {
	cfg, original := agentFixture(t)
	current := filepath.Join(filepath.Dir(cfg.Dir), "person.json")
	t.Setenv("OPENCODE_CONFIG", current)
	r := exec(t, testPlatform(fakeAddr, nil), "connect", "--configure", "dsh,opencode")
	if r.code == 0 || !strings.Contains(r.err, "OPENCODE_CONFIG") || !strings.Contains(r.err, current) || !strings.Contains(r.err, "not written") {
		t.Fatal(r)
	}
	if _, err := os.Stat(cfg.Dir); !os.IsNotExist(err) {
		t.Fatal("refusal wrote receipt/lock/config")
	}
	b, _ := os.ReadFile(cfg.DSH)
	if string(b) != original {
		t.Fatal("partial DSH write")
	}
}
