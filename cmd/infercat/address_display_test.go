package main

import (
	"bytes"
	"context"
	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/invite"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/tunnel"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/tailscale/tailcat"
	"strings"
	"testing"
)

type displayTunnel struct {
	fakeTunnel
	addr string
}

func (s displayTunnel) Addr() string { return s.addr }

type displayEngine struct{ upstream.Upstream }

func (displayEngine) Info() upstream.Info { return upstream.Info{} }

type displayKeys struct{ keys.Store }

func (displayKeys) List(context.Context) ([]*keys.Key, error) { return nil, nil }

func TestHumanAddressSurfacesAreMaskedButInvitesAreNot(t *testing.T) {
	pk := tailcat.NewPrivateKey()
	pk.Public.RegionID = 302
	addr := string(pk.Public.Addr())
	want := tunnel.Display(addr)
	var startupOut, statusOut, bridgeOut bytes.Buffer
	e := env{out: &startupOut, errw: &startupOut}
	e.printStartup(context.Background(), startup{routes: (&fakeGateway{}).EngineRoutes(), tun: displayTunnel{addr: addr}, up: displayEngine{}, store: displayKeys{}, dataDir: t.TempDir()})
	st := admin.Status{Tunnel: admin.Tunnel{Addr: addr}}
	writeStatus(&statusOut, st)
	st.Mode = "bridge"
	writeStatus(&bridgeOut, st)
	for name, text := range map[string]string{"startup": startupOut.String(), "status": statusOut.String(), "bridge": bridgeOut.String()} {
		if !strings.Contains(text, want) || strings.Contains(text, addr) {
			t.Errorf("%s does not mask the address", name)
		}
	}
	code := invite.Encode(addr, "test-secret")
	decoded, err := invite.Decode(code)
	if err != nil || decoded.Addr != addr || !strings.HasPrefix(code, "ic2.") || st.Tunnel.Addr != addr {
		t.Fatal("minting or status data was altered", err)
	}
}
