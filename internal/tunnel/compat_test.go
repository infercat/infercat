package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
)

func TestOldAndNewKeyFilesKeepOldAddress(t *testing.T) {
	// Generated with v0.4.0 from a public, test-only private key, never a live host identity.
	const oldAddr = "tco2FwWCCk4JKStlHCeLl3LFafX6m7E9kGtGq2jJ353CtECfiiCWFrWCCkw8wWHudKMOe9x_uce97IGTEA8kOJ-LJumvOH2kgcMmFpGQEu"
	pk, err := ReadIdentity("testdata/host-v040.json")
	if err != nil {
		t.Fatal(err)
	}
	if !pk.Public.PresharedKey.IsZero() || addrFor(pk) != oldAddr {
		t.Fatal("v0.4.0 identity/address changed")
	}
	pk.Public.PresharedKey = (&Identity{PrivateKey: *tailcat.NewPrivateKey()}).Public.PresharedKey
	if pk.Public.PresharedKey.IsZero() || addrFor(pk) != oldAddr {
		t.Fatal("stored PSK changed the old address")
	}
	mapURL := localDERP(t)
	pk.Public.RegionID = 1
	want := addrFor(pk)
	for _, withPSK := range []bool{false, true} {
		name := "old"
		if withPSK {
			name = "new"
		}
		t.Run(name, func(t *testing.T) {
			p := *pk
			if !withPSK {
				p.Public.PresharedKey = tailcat.PresharedKey{}
			}
			// Omit the field entirely for an actual pre-upgrade file shape.
			raw := []byte(mustJSON(t, &p))
			if !withPSK {
				var fields map[string]any
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				delete(fields["Public"].(map[string]any), "PresharedKey")
				raw = []byte(mustJSON(t, fields))
			}
			dir := t.TempDir()
			path := filepath.Join(dir, KeyFile)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			stored, err := ReadIdentity(path)
			if err != nil || stored.Public.PresharedKey.IsZero() == withPSK {
				t.Fatal("stored PSK does not match file generation")
			}
			s := startOK(t, Options{DataDir: dir, DERPMapURL: mapURL})
			saved, err := SavedAddr(dir)
			if err != nil || s.Addr() != want || saved != want {
				t.Fatalf("address changed: %s / %s / %v", s.Addr(), saved, err)
			}
			ci, err := tailcat.ParseAddr(tailcat.Addr(s.Addr()))
			if err != nil || !ci.PresharedKey.IsZero() || !s.tc.DisablePresharedKey {
				t.Fatal("host enabled PSK")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("Start rewrote an existing key")
			}
		})
	}
}

func TestSessionConnectsToPSKAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s := startOK(t, Options{DataDir: t.TempDir(), DERPMapURL: localDERP(t), Region: "1"})
	addr := tailcat.Addr(s.Addr())
	ci, err := tailcat.ParseAddr(addr)
	if err != nil || ci.PresharedKey.IsZero() {
		t.Fatal("fixture has no PSK")
	}
	go http.Serve(s.Listener(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "psk-ok") }))
	cl, err := Dial(ctx, string(addr), ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return cl.Open(ctx) }}
	defer transport.CloseIdleConnections()
	hc := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	resp, err := hc.Get("http://tunnel/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 || string(body) != "psk-ok" {
		t.Fatalf("PSK tunnel response: %d %q %v", resp.StatusCode, body, err)
	}
}

func TestVersionTwoIdentityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pk := NewIdentity()
	pk.Public.RegionID = 302
	if _, err := saveKey(filepath.Join(dir, KeyFile), pk); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadIdentity(filepath.Join(dir, KeyFile))
	if err != nil || saved.Format != 2 || saved.Public.PresharedKey != pk.Public.PresharedKey {
		t.Fatalf("identity round trip: %v", err)
	}
	ci, err := tailcat.ParseAddr(tailcat.Addr(addrFor(saved)))
	if err != nil || ci.PresharedKey.IsZero() {
		t.Fatalf("version 2 address: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["infercat_format"] != float64(2) {
		t.Fatal("version marker missing")
	}
}

func TestRefuseInvalidIdentityFormat(t *testing.T) {
	for _, version := range []int{1, 3, -1} {
		pk := NewIdentity()
		pk.Format = version
		p := filepath.Join(t.TempDir(), KeyFile)
		if err := os.WriteFile(p, []byte(mustJSON(t, pk)), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadIdentity(p); err == nil {
			t.Fatalf("accepted format %d", version)
		}
	}
	pk := NewIdentity()
	pk.Public.PresharedKey = tailcat.PresharedKey{}
	p := filepath.Join(t.TempDir(), KeyFile)
	if err := os.WriteFile(p, []byte(mustJSON(t, pk)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadIdentity(p); err == nil {
		t.Fatal("accepted missing v2 PSK")
	}
}
