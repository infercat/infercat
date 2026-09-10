package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/dirlock"
	"github.com/infercat/infercat/internal/invite"
	"github.com/infercat/infercat/internal/tunnel"
	"github.com/tailscale/tailcat"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RETIRE after 2026-09-25 with identity_upgrade.go.
func legacyIdentity(t *testing.T, dir string) []byte {
	t.Helper()
	pk := tailcat.NewPrivateKey()
	pk.Public.RegionID = 302
	raw, err := json.Marshal(pk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tunnel.KeyFile), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestIdentityUpgrade(t *testing.T) {
	dir := t.TempDir()
	raw := legacyIdentity(t, dir)
	old, _ := tunnel.SavedAddr(dir)
	r := exec(t, newPlatform(), "--data-dir", dir, "identity", "upgrade")
	if r.code != 0 || !strings.Contains(r.out, "Every existing invite stops working") {
		t.Fatal(r)
	}
	path := filepath.Join(dir, tunnel.KeyFile)
	backup, err := os.ReadFile(path + ".pre-ic2")
	if err != nil || !bytes.Equal(raw, backup) {
		t.Fatal("backup not byte-identical", err)
	}
	next, err := tunnel.ReadIdentity(path)
	if err != nil || next.Format != 2 {
		t.Fatal("version not upgraded", err)
	}
	addr, err := tunnel.SavedAddr(dir)
	if err != nil || addr == old || !strings.HasPrefix(invite.Encode(addr, "secret"), "ic2.") {
		t.Fatal("identity/address not replaced", err)
	}
	if next.Public.RegionID != 302 {
		t.Fatal("relay changed")
	}
	once, _ := os.ReadFile(path)
	r = exec(t, newPlatform(), "--data-dir", dir, "identity", "upgrade")
	after, _ := os.ReadFile(path)
	if r.code != 0 || !bytes.Equal(once, after) {
		t.Fatal("repeat mutated v2 identity", r)
	}
	r = exec(t, newPlatform(), "--data-dir", dir, "keys", "add", "friend", "--json", "--no-qr")
	var minted struct {
		Invite string `json:"invite"`
	}
	if err := json.Unmarshal([]byte(r.out), &minted); err != nil {
		t.Fatal(err)
	}
	if r.code != 0 || !strings.HasPrefix(minted.Invite, "ic2.") {
		t.Fatal("keys add did not mint ic2", r)
	}
}
func TestUpgradeRefusalsPreserveIdentity(t *testing.T) {
	for _, kind := range []string{"lock", "old-server", "backup", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "ic136-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			raw := legacyIdentity(t, dir)
			path := filepath.Join(dir, tunnel.KeyFile)
			switch kind {
			case "lock":
				f, err := dirlock.Acquire(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
			case "old-server":
				s, err := admin.Serve(dir, func() admin.Status { return admin.Status{} }, func() error { return nil }, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				os.Remove(filepath.Join(dir, admin.TokenName))
			case "backup":
				if err := os.WriteFile(path+".pre-ic2", []byte("existing backup"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(dir, "original")
				os.Rename(path, target)
				if err := os.Symlink(target, path); err != nil {
					t.Skip(err)
				}
			}
			r := exec(t, newPlatform(), "--data-dir", dir, "identity", "upgrade")
			after, err := os.ReadFile(path)
			if r.code == 0 || err != nil || !bytes.Equal(raw, after) {
				t.Fatal("refusal changed identity", r, err)
			}
			if kind == "backup" {
				b, _ := os.ReadFile(path + ".pre-ic2")
				if string(b) != "existing backup" {
					t.Fatal("overwrote backup")
				}
			}
		})
	}
}
func TestServeRefusesLockedDataDirBeforeTunnel(t *testing.T) {
	dir := t.TempDir()
	f, err := dirlock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	plat := testPlatform(fakeAddr, nil)
	called := false
	plat.startTunnel = func(context.Context, tunnelOptions) (tunnelServer, error) { called = true; return nil, nil }
	r := exec(t, plat, "--data-dir", dir, "serve", "--console", "off")
	if r.code == 0 || called || !strings.Contains(r.err, "data directory is in use") {
		t.Fatal(r, called)
	}
}
