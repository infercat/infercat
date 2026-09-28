package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/infercat/infercat/internal/machine"
)

func TestServiceInstallRefusesProtectedLocationsBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, root := range strings.Split(macOSBackgroundProtectedRoots, "\n") {
		t.Run(root, func(t *testing.T) {
			home := t.TempDir()
			base := root
			if !filepath.IsAbs(base) {
				base = filepath.Join(home, base)
			}
			fake := newLaunchdFake(t)
			host := fake.host(home)
			host.binary = filepath.Join(base, "infercat")
			svc := launchdService{host}
			if err := os.MkdirAll(filepath.Dir(svc.plistPath()), 0755); err != nil {
				t.Fatal(err)
			}
			original := []byte("existing plist must remain untouched")
			if err := os.WriteFile(svc.plistPath(), original, 0644); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			e := &env{out: &out, errw: io.Discard, svcHost: &host}
			code, handled := e.machineService(context.Background(), []string{"service", "install", "--json"})
			if !handled || code != 1 || !strings.Contains(out.String(), `"code":"binary_in_protected_directory"`) || !strings.Contains(out.String(), "/usr/local/bin or /Applications") {
				t.Fatal(code, out.String())
			}
			got, err := os.ReadFile(svc.plistPath())
			if err != nil || !bytes.Equal(got, original) {
				t.Fatal("refusal changed plist", err)
			}
			if fake.tool.log() != "" {
				t.Fatal("refusal called supervisor", fake.tool.log())
			}
			if _, err := os.Stat(filepath.Dir(svc.logPath())); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refusal created logs", err)
			}
		})
	}
}

func TestServiceProtectedPathResolutionAndStablePlist(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	file := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	link := func(path, target string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	private := filepath.Join(home, "Documents", "binary")
	safe := filepath.Join(home, "Applications", "binary")
	file(private)
	file(safe)
	into := filepath.Join(home, "bin", "into")
	out := filepath.Join(home, "Desktop", "out")
	stable := filepath.Join(home, "bin", "stable")
	link(into, private)
	link(out, safe)
	link(stable, safe)
	for _, path := range []string{into, out, filepath.Join(home, "Downloads", "..", "Desktop", "binary")} {
		err := refuseProtectedServiceBinary(home, path)
		if err == nil || machine.Classify("service.install", err).Code != "binary_in_protected_directory" {
			t.Fatal(path, err)
		}
	}
	for _, path := range []string{stable, filepath.Join(home, "Desktop-old", "binary"), "/Volumes-old/binary"} {
		if err := refuseProtectedServiceBinary(home, path); err != nil {
			t.Fatal(path, err)
		}
	}
	homeLink := filepath.Join(t.TempDir(), "home")
	link(homeLink, home)
	if err := refuseProtectedServiceBinary(homeLink, private); err == nil {
		t.Fatal("resolved home bypassed guard")
	}
	fake := newLaunchdFake(t)
	host := fake.host(home)
	host.binary = stable
	svc := launchdService{host}
	if err := svc.install(context.Background(), "", true); err != nil {
		t.Fatal(err)
	}
	installed, err := svc.installed()
	if err != nil || installed.args[0] != stable {
		t.Fatal("plist lost stable invoked path", err)
	}
}

func TestServiceStatusStuckIsBoundedAndMacOnly(t *testing.T) {
	for _, mode := range []string{"stuck", "starts", "cancelled", "linux"} {
		t.Run(mode, func(t *testing.T) {
			home, data := t.TempDir(), t.TempDir()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				start := time.Now()
				host := serviceHost{goos: "darwin", home: home, uid: "501", binary: filepath.Join(home, "bin", "infercat"), settle: 2 * time.Second}
				host.run = func(context.Context, string, ...string) (string, error) {
					if mode == "linux" {
						return "LoadState=loaded\nActiveState=inactive\nMainPID=0\nFragmentPath=/fixture/unit\n", nil
					}
					if mode == "starts" && time.Since(start) >= time.Second {
						return launchctlPrintRunning, nil
					}
					return launchctlPrintWaiting, nil
				}
				if mode == "linux" {
					host.goos = "linux"
				} else if err := (launchdService{host}).install(ctx, data, true); err != nil {
					t.Fatal(err)
				}
				if mode == "cancelled" {
					time.AfterFunc(time.Second, cancel)
				}
				e := &env{svcHost: &host}
				st, err := e.serviceAction(ctx, "status", data, false)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "stuck" {
					if !st.Stuck || time.Since(start) != 2*time.Second || st.PID != 0 || st.Running || serviceStateLine(st) != "loaded · stuck (no pid after 2s)" {
						t.Fatal(st, time.Since(start))
					}
				} else {
					if st.Stuck {
						t.Fatal("claimed stuck without full observation", mode)
					}
					if mode == "linux" && (time.Since(start) != 0 || serviceStateLine(st) != "installed · loaded · not running") {
						t.Fatal(st, time.Since(start))
					}
					if mode == "starts" && !st.Running {
						t.Fatal(st)
					}
					if mode == "cancelled" && time.Since(start) >= 2*time.Second {
						t.Fatal("cancel did not interrupt settling")
					}
				}
			})
		})
	}
}
