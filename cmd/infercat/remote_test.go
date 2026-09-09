package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/adminkey"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
)

func TestRemoteCLIUsesRunningHostAndOnceResults(t *testing.T) {
	dir, dirErr := os.MkdirTemp("", "ic090-")
	if dirErr != nil {
		t.Fatal(dirErr)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.WriteFile(filepath.Join(dir, "admin.json"), []byte("damaged"), 0600)
	remote, _ := adminkey.Open(dir)
	state := &consoleState{remote: remote, value: consoleSettings{Name: "host", DataDir: dir, ConsoleAddress: "127.0.0.1:9101", WebURL: "https://example.com/"}}
	store, _ := keys.NewFileStore(dir)
	up, _ := upstream.Open(context.Background(), fakeEngine(t), "")
	var out bytes.Buffer
	e := &env{out: &out, errw: io.Discard}
	server, err := admin.Serve(dir, func() admin.Status {
		s := remote.State()
		return admin.Status{Console: state.snapshot().ConsoleAddress, Remote: &s}
	}, nil, nil, e.consoleAPI(store, fakeAddr, up, state.value, state))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	call := func(action string, flags ...string) string {
		t.Helper()
		out.Reset()
		args := append([]string{action, "--data-dir", dir}, flags...)
		if err := e.cmdRemote(context.Background(), "", args); err != nil {
			t.Fatal(action, err)
		}
		return out.String()
	}
	if text := call("status"); !strings.Contains(text, "admin.json") || !strings.Contains(text, "remote    off") {
		t.Fatal(text)
	}
	var first admin.RemoteResult
	json.Unmarshal([]byte(call("on", "--json")), &first)
	prefix := "ia1." + fakeAddr + "."
	if !strings.HasPrefix(first.Invite, prefix) || !remote.Authenticate(strings.TrimPrefix(first.Invite, prefix)) {
		t.Fatal("mint")
	}
	if _, err := os.Stat(filepath.Join(dir, "keys.json")); !os.IsNotExist(err) {
		t.Fatal("CLI wrote friend store")
	}
	out.Reset()
	if err := e.cmdRemote(context.Background(), "", []string{"on", "--data-dir", dir}); err == nil || out.Len() != 0 {
		t.Fatal("duplicate on exposed a code")
	}
	if text := call("status", "--json"); strings.Contains(text, first.Invite) || strings.Contains(text, "sha256") || strings.Contains(text, "warning_file") {
		t.Fatal("status secret/warning", text)
	}
	e.tty = true
	text := call("rotate", "--no-qr")
	if strings.Count(text, "ia1.") != 1 || strings.Contains(text, "█") {
		t.Fatal("once output", text)
	}
	if remote.Authenticate(strings.TrimPrefix(first.Invite, prefix)) {
		t.Fatal("old code still valid")
	}
	text = call("rotate")
	if strings.Count(text, "ia1.") != 1 || !strings.Contains(text, "█") {
		t.Fatal("terminal QR")
	}
	call("off", "--json")
	if remote.State().Enabled {
		t.Fatal("off")
	}
	state.mu.Lock()
	state.value.ConsoleAddress = ""
	state.mu.Unlock()
	for _, action := range []string{"on", "rotate", "off", "status"} {
		if err := e.cmdRemote(context.Background(), "", []string{action, "--data-dir", dir}); err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Fatal(action, err)
		}
	}
}
func TestRemoteActionDoesNotReplayAmbiguousResponse(t *testing.T) {
	dir, dirErr := os.MkdirTemp("", "ic090-")
	if dirErr != nil {
		t.Fatal(dirErr)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	calls := 0
	server, err := admin.Serve(dir, func() admin.Status { return admin.Status{} }, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/remote/enable" {
			t.Error(r.URL.Path)
		}
		io.WriteString(w, "{")
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if _, err := admin.RemoteAction(context.Background(), dir, "enable"); err == nil || !strings.Contains(err.Error(), "check remote status") {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("replayed", calls)
	}
}

func TestServeDamagedAdminKeepsHostAvailable(t *testing.T) {
	dir, err := os.MkdirTemp("", "ic090-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "admin.json")
	os.WriteFile(path, []byte("damaged"), 0600)
	result := serveOnce(t, dir, "--upstream", fakeEngine(t))
	if result.code != 0 || strings.Count(result.out, "Remote access is off:") != 1 || !strings.Contains(result.out, path) || !strings.Contains(result.out, "Turn remote access on again") {
		t.Fatal(result)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "damaged" {
		t.Fatal("serve replaced damaged record")
	}
}
