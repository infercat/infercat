package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/adminkey"
	"github.com/infercat/infercat/internal/bridge"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/machine"
	runstate "github.com/infercat/infercat/internal/run"
)

type recordedAdmin struct {
	http.Handler
	mu     sync.Mutex
	bodies map[string][]byte
}

func (r *recordedAdmin) Routes() []string { return admin.Routes(r.Handler) }
func (r *recordedAdmin) ServeHTTP(w http.ResponseWriter, q *http.Request) {
	out := httptest.NewRecorder()
	r.Handler.ServeHTTP(out, q)
	r.mu.Lock()
	r.bodies[q.Method+" "+q.URL.Path] = bytes.Clone(out.Body.Bytes())
	r.mu.Unlock()
	for k, vs := range out.Header() {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(out.Code)
	_, _ = w.Write(out.Body.Bytes())
}

type machineFixture struct {
	dir, id, addr string
	store         *keys.FileStore
	server        *admin.Server
	api           *recordedAdmin
	runs          *runstate.Store
	hub           *admin.Events
}

func newMachineFixture(t *testing.T) *machineFixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "ic195-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	addr := savedTestIdentity(t, dir, 2)
	store, err := keys.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	k, _, err := store.Add(context.Background(), "alice", keys.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(dir, config{Name: "test host", Console: "127.0.0.1:9101"}); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Save(dir, bridge.Config{Endpoint: "https://example.test", Host: "test", Token: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	remote, err := adminkey.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := &consoleState{remote: remote, value: consoleSettings{DataDir: dir, Name: "test host", ConsoleAddress: "127.0.0.1:9101", WebURL: "https://example.test"}}
	e := &env{plat: newPlatform(), out: io.Discard, errw: io.Discard}
	rs, err := runstate.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := runstate.New(rs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	h := admin.WithStored(admin.WithRuns(e.consoleAPI(store, addr, displayEngine{}, state.value, state), rs.List), rs.Stored, manager.ClearTerminal)
	recorded := &recordedAdmin{Handler: h, bodies: map[string][]byte{}}
	hub := admin.NewEvents(nil)
	server, err := admin.Serve(dir, func() admin.Status {
		return admin.Status{Name: "test host", Mode: "host", Console: state.snapshot().ConsoleAddress}
	}, store.Reload, hub, recorded)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return &machineFixture{dir, k.ID, addr, store, server, recorded, rs, hub}
}

func operationArgs(name, id, cursor string) []string {
	switch name {
	case "status", "version", "api", "engine":
		return []string{name, "--json"}
	case "settings.get":
		return []string{"settings", "--json"}
	case "settings.set":
		return []string{"settings", "set", "--json", "--", "name=changed"}
	case "keys.list":
		return []string{"keys", "list", "--json"}
	case "keys.get":
		return []string{"keys", "show", id, "--json"}
	case "keys.add":
		return []string{"keys", "add", "--json", "--", "bob"}
	case "keys.limits":
		return []string{"keys", "limits", id, "--rpm", "5", "--json"}
	case "keys.revoke":
		return []string{"keys", "revoke", id, "--yes", "--json"}
	case "usage":
		return []string{"usage", "--window", "week", "--key", id, "--json"}
	case "runs.list":
		return []string{"runs", "list", "--key", id, "--json"}
	case "stored.get":
		return []string{"stored", id, "--json"}
	case "stored.clear":
		return []string{"stored", "clear", id, "--expect", cursor, "--yes", "--json"}
	case "expose.status":
		return []string{"expose", "--json"}
	case "expose.on", "expose.off":
		return []string{"expose", "--" + strings.TrimPrefix(name, "expose."), "--json"}
	case "watch":
		return []string{"watch", "--interval", "10ms", "--json"}
	}
	parts := strings.Split(name, ".")
	args := []string{parts[0], parts[1]}
	if parts[0] == "keys" {
		args = append(args, id)
	}
	return append(args, "--json")
}

func TestMachineAdminRouteAndBodyParity(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	var routes []string
	for _, op := range machineOperations {
		routes = append(routes, op.Routes...)
	}
	slices.Sort(routes)
	routes = slices.Compact(routes)
	if got := f.server.Routes(); !reflect.DeepEqual(got, routes) {
		t.Fatalf("admin/API drift\nadmin: %v\nCLI: %v", got, routes)
	}
	for _, op := range machineOperations {
		if op.Operation == "watch" || len(op.Routes) == 0 {
			continue
		}
		t.Run(op.Operation, func(t *testing.T) {
			t.Parallel()
			f := newMachineFixture(t)
			stored, err := f.runs.Stored(f.id)
			if err != nil {
				t.Fatal(err)
			}
			args := operationArgs(op.Operation, f.id, stored.Cursor)
			r, err := parseMachine(f.dir, func() []string { clean, _, _ := machine.Parse(args); return clean }())
			if err != nil {
				t.Fatal(err)
			}
			// Rotate requires an already-enabled admin identity, without exposing its code.
			if op.Operation == "remote.rotate" {
				client, err := admin.NewClient(f.dir)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = client.Call(context.Background(), "POST", "/remote/enable", nil); err != nil {
					t.Fatal(err)
				}
			}
			code, result, raw := machineCall(t, context.Background(), append([]string{"--data-dir", f.dir}, args...)...)
			if code != 0 {
				t.Fatal(code, raw)
			}
			var expected []byte
			if r.path == "/status" || r.path == "/reload" {
				client, _ := admin.NewClient(f.dir)
				expected, err = client.Call(context.Background(), r.method, r.path, nil)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				method := r.method
				if op.Operation == "stored.clear" {
					method = "DELETE"
				}
				f.api.mu.Lock()
				expected = bytes.Clone(f.api.bodies[method+" "+strings.Split(r.path, "?")[0]])
				f.api.mu.Unlock()
			}
			if !bytes.Equal(result["data"], bytes.TrimSpace(expected)) {
				t.Fatalf("body changed\nroute: %s\nCLI: %s", expected, result["data"])
			}
		})
	}
}

func TestMachineLocalKeyExtensionsRefuseRemote(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	for _, field := range []string{"force", "agent"} {
		for _, value := range []string{"true", "false", "null"} {
			before, err := os.ReadFile(filepath.Join(f.dir, keys.FileName))
			if err != nil {
				t.Fatal(err)
			}
			path := "/keys"
			method := "POST"
			body := `{"name":"bob","` + field + `":` + value + `}`
			for i := 0; i < 2; i++ {
				if i == 1 {
					path = "/keys/" + f.id
					method = "PATCH"
					body = `{"` + field + `":` + value + `}`
				}
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("X-Infercat-Remote", "true")
				out := httptest.NewRecorder()
				f.api.ServeHTTP(out, r)
				if out.Code != 403 {
					t.Fatal(field, value, method, out.Code, out.Body.String())
				}
				after, err := os.ReadFile(filepath.Join(f.dir, keys.FileName))
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("remote refusal mutated the store", err)
				}
			}
		}
	}
	code, _, raw := machineCall(t, context.Background(), "--data-dir", f.dir, "keys", "add", "--json", "--force", "--agent", "--", "alice")
	if code != 0 {
		t.Fatal(code, raw)
	}
	list, err := f.store.List(context.Background())
	if err != nil || len(list) != 2 || !list[1].Agent {
		t.Fatal(list, err)
	}
}

func TestMachineClearRefusesStaleCursor(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	code, result, raw := machineCall(t, context.Background(), "--data-dir", f.dir, "stored", "clear", f.id, "--expect", "stale:0", "--yes", "--json")
	if code != 1 || !strings.Contains(string(result["error"]), `"conflict"`) {
		t.Fatal(code, raw)
	}
	f.api.mu.Lock()
	defer f.api.mu.Unlock()
	if _, ok := f.api.bodies["DELETE /stored"]; ok {
		t.Fatal("stale cursor reached DELETE")
	}
}
