package tunnel

import (
	"bytes"
	"encoding/base64"
	"github.com/tailscale/tailcat"
	"os"
	"path/filepath"
	"strings"
	"tailscale.com/tailcfg"
	"testing"
)

func TestDisplayRemovesPSKWithoutChangingIdentity(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "short", true: "embedded"}[full], func(t *testing.T) {
			identity := NewIdentity()
			identity.Public.RegionID = 302
			if full {
				identity.Public.RegionID = 0
				identity.Public.Region = []*tailcfg.DERPRegion{{RegionID: 302, Nodes: []*tailcfg.DERPNode{{Name: "example", HostName: "relay.example.test", DERPPort: 443}}}}
			}
			// A known byte pattern makes both raw and encoded leakage assertions deterministic.
			for i := range identity.Public.PresharedKey {
				identity.Public.PresharedKey[i] = 0xa5
			}
			original := addrFor(identity)
			path := filepath.Join(t.TempDir(), KeyFile)
			if _, err := saveKey(path, identity); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			display := Display(original)
			masked, ok := strings.CutSuffix(display, "…psk:••••")
			if !ok || display == original {
				t.Fatal("PSK address not masked")
			}
			payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(masked, "tc"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(payload, identity.Public.PresharedKey[:]) || strings.Contains(display, base64.RawURLEncoding.EncodeToString(identity.Public.PresharedKey[:])) {
				t.Fatal("display contains PSK bytes")
			}
			ci, err := tailcat.ParseAddr(tailcat.Addr(masked))
			if err != nil || !ci.PresharedKey.IsZero() {
				t.Fatal("masked payload still has a PSK", err)
			}
			if ci.ServerPublic != identity.Public.ServerPublic || ci.ServerDiscoPublic != identity.Public.ServerDiscoPublic {
				t.Fatal("display changed public identity")
			}
			after, _ := os.ReadFile(path)
			saved, err := SavedAddr(filepath.Dir(path))
			if err != nil || saved != original || !bytes.Equal(before, after) || identity.Public.PresharedKey.IsZero() {
				t.Fatal("display mutated identity or persistence", err)
			}
			if Display(masked) != masked {
				t.Fatal("legacy display changed")
			}
			malformed := "tc" + base64.RawURLEncoding.EncodeToString(append([]byte("invalid"), identity.Public.PresharedKey[:]...))
			if Display(malformed) != "[invalid tunnel address]" {
				t.Fatal("unparsed address leaked")
			}
		})
	}
	if Display("") != "" {
		t.Fatal("empty display changed")
	}
}
