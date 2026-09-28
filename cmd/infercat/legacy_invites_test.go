package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/tunnel"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

func savedTestIdentity(t *testing.T, dir string, format int) string {
	t.Helper()
	identity := tunnel.NewIdentity()
	identity.Format = format // Legacy files can contain a PSK; the saved address must still be legacy.
	identity.Public.RegionID = 302
	raw, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, tunnel.KeyFile), raw, 0600); err != nil {
		t.Fatal(err)
	}
	addr, err := tunnel.SavedAddr(dir)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestLegacyInviteMintingRefusesWithoutMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	addr := savedTestIdentity(t, dir, 0)
	store, err := keys.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	old, secret, err := store.Add(ctx, "existing", keys.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	before := readFile(t, filepath.Join(dir, keys.FileName))
	identity := readFile(t, filepath.Join(dir, tunnel.KeyFile))
	for _, sub := range [][]string{{"add", "new"}, {"rotate", old.ID}} {
		r := exec(t, newPlatform(), append([]string{"--data-dir", dir, "keys"}, append(sub, "--json", "--no-qr")...)...)
		if r.code == 0 || r.out != "" || !strings.Contains(r.err, errLegacyIdentity.Error()) || strings.Count(strings.TrimSpace(r.err), "\n") != 0 {
			t.Fatal(r)
		}
	}
	e := &env{plat: newPlatform(), out: io.Discard, errw: io.Discard}
	handler := e.consoleAPI(store, addr, displayEngine{}, consoleSettings{DataDir: dir})
	for _, path := range []string{"/keys", "/keys/" + old.ID + "/rotate"} {
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"new"}`)))
		var body map[string]string
		if json.Unmarshal(out.Body.Bytes(), &body) != nil || out.Code != 400 || body["error"] != errLegacyIdentity.Error() {
			t.Fatal(out.Code, out.Body.String())
		}
	}
	if !bytes.Equal(before, readFile(t, filepath.Join(dir, keys.FileName))) || !bytes.Equal(identity, readFile(t, filepath.Join(dir, tunnel.KeyFile))) {
		t.Fatal("refusal mutated keys or identity")
	}
	if _, ok, err := store.Lookup(ctx, secret); err != nil || !ok {
		t.Fatal("existing friend's key invalidated", err)
	}
}

func TestVersionTwoKeysStillMintAndRotate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	savedTestIdentity(t, dir, 2)
	var first inviteJSON
	for i, sub := range [][]string{{"add", "friend"}, {"rotate", "friend"}} {
		r := exec(t, newPlatform(), append([]string{"--data-dir", dir, "keys"}, append(sub, "--json", "--no-qr")...)...)
		var got inviteJSON
		if r.code != 0 || json.Unmarshal([]byte(r.out), &got) != nil || !strings.HasPrefix(got.Invite, "ic2.") {
			t.Fatal(r)
		}
		if i == 0 {
			first = got
		} else if got.KeyID != first.KeyID || got.Invite == first.Invite {
			t.Fatal("rotation did not replace the invite", got)
		}
	}
}

func TestIdentityCommandRetired(t *testing.T) {
	t.Parallel()
	if strings.Contains(rootHelp, "identity") {
		t.Fatal("retired command still in help")
	}
	r := exec(t, newPlatform(), "identity", "upgrade")
	if r.code != 2 || !strings.Contains(r.err, `unknown command "identity"`) {
		t.Fatal(r)
	}
}

func TestLegacyHostStillServes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	addr := savedTestIdentity(t, dir, 0)
	before := readFile(t, filepath.Join(dir, tunnel.KeyFile))
	gw := newFakeGateway()
	plat := testPlatform(addr, nil)
	plat.startTunnel = func(context.Context, tunnelOptions) (tunnelServer, error) { return displayTunnel{addr: addr}, nil }
	plat.newGateway = func(gatewayOptions, upstream.Upstream, keys.Store, usage.Recorder, func(string, ...any)) (gatewayServer, error) {
		return gw, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errw lockedBuffer
	done := make(chan int, 1)
	engine := fakeEngine(t)
	go func() {
		done <- run(ctx, []string{"serve", "--data-dir", dir, "--upstream", engine, "--console", "off"}, &out, &errw, nil, false, plat)
	}()
	select {
	case <-gw.serving:
	case code := <-done:
		t.Fatalf("serve exited %d: %s", code, errw.String())
	case <-time.After(10 * time.Second):
		t.Fatal("legacy host did not serve")
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal(code, errw.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("legacy host did not stop")
	}
	if strings.Count(out.String(), errLegacyIdentity.Error()) != 1 || strings.Contains(out.String(), "Mint a friend:") {
		t.Fatal(out.String())
	}
	if !bytes.Equal(before, readFile(t, filepath.Join(dir, tunnel.KeyFile))) {
		t.Fatal("serve rewrote legacy identity")
	}
}
