package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

func TestConsoleKeyLifecycleAndCounts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := keys.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	up, err := upstream.Open(ctx, fakeEngine(t), "")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{plat: testPlatform(fakeAddr, nil), out: io.Discard, errw: io.Discard}
	h := e.consoleAPI(store, fakeAddr, up, consoleSettings{Name: "test host", DataDir: dir, LogRequests: true, LogPrompts: true, Upstream: "http://localhost:8080", DERPMapURL: "https://relay.example/map", Region: "nyc", ConfiguredWebURL: "", WebURL: "https://infercat.ai"})
	call := func(method, path, body string, code int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != code {
			t.Fatalf("%s %s = %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var minted map[string]string
	json.Unmarshal(call("POST", "/keys", `{"name":"alice","limits":{"rpm":42}}`, 200), &minted)
	if len(minted) != 4 || minted["name"] != "alice" || minted["invite"] == "" {
		t.Fatal(minted)
	}
	id := minted["key_id"]
	secret := strings.TrimPrefix(minted["invite"], "ic1."+fakeAddr+".")
	k, ok, err := store.Lookup(ctx, secret)
	if err != nil || !ok || k.Limits.RPM != 42 {
		t.Fatal(k, ok, err)
	}
	raw := readFile(t, filepath.Join(dir, keys.FileName))
	if strings.Contains(string(raw), secret) {
		t.Fatal("secret persisted")
	}
	call("POST", "/keys", `{"name":"alice"}`, 400)
	call("POST", "/keys/"+id+"/pause", "", 200)
	call("POST", "/keys", `{"name":"alice"}`, 400)
	k, _ = store.Find(ctx, id)
	if k.Status != keys.Paused {
		t.Fatal(k)
	}
	call("POST", "/keys/"+id+"/resume", "", 200)
	call("PATCH", "/keys/"+id, `{"rpm":0}`, 200)
	k, _ = store.Find(ctx, id)
	if k.Limits.RPM != 0 || k.Limits.TPM != keys.DefaultLimits().TPM {
		t.Fatal(k)
	}
	before := readFile(t, filepath.Join(dir, keys.FileName))
	for _, body := range []string{`{"rpm":1,"unknown":2}`, `{"rpm":1} {}`, `{`, strings.Repeat("x", 17000)} {
		call("PATCH", "/keys/"+id, body, 400)
		if string(readFile(t, filepath.Join(dir, keys.FileName))) != string(before) {
			t.Fatal("refusal mutated keys")
		}
	}
	var rotated map[string]string
	json.Unmarshal(call("POST", "/keys/"+id+"/rotate", "", 200), &rotated)
	if rotated["invite"] == minted["invite"] {
		t.Fatal("rotation reused secret")
	}
	if _, ok, _ := store.Lookup(ctx, secret); ok {
		t.Fatal("old invite still resolves")
	}
	call("POST", "/keys/"+id+"/revoke", "", 200)
	k, _ = store.Find(ctx, id)
	if k.Status != keys.Revoked {
		t.Fatal(k)
	}
	rec, err := usage.NewFileRecorder(dir, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Truncate(24 * time.Hour)
	rec.Record(ctx, usage.Event{TS: day.Add(time.Hour), KeyID: id, Endpoint: "/v1/chat/completions", Status: 200, PromptTokens: 3, CompletionTokens: 7, TotalMS: 10, Prompt: "PRIVATE TEXT"})
	rec.Record(ctx, usage.Event{TS: day.Add(-time.Hour), KeyID: id, Endpoint: "/v1/chat/completions", Status: 403, Code: "key_revoked"})
	rec.Close()
	var rep usage.Report
	json.Unmarshal(call("GET", "/usage?window=today", "", 200), &rep)
	if rep.Total.Requests != 1 || rep.Total.PromptTokens != 3 || rep.Total.CompletionTokens != 7 || rep.Total.TotalP95MS != 10 {
		t.Fatalf("today: %+v", rep)
	}
	json.Unmarshal(call("GET", "/usage?window=week", "", 200), &rep)
	if rep.Total.Requests != 2 || rep.Total.ErrorsByCode["key_revoked"] != 1 || len(rep.Daily) != 7 || rep.Daily[6].Total.PromptTokens != 3 {
		t.Fatalf("week: %+v", rep)
	}
	var detail consoleKey
	body := call("GET", "/keys/"+id, "", 200)
	json.Unmarshal(body, &detail)
	if detail.TodayTokens != 10 || len(detail.Daily) != 7 || detail.LastSeen.IsZero() {
		t.Fatalf("detail: %+v", detail)
	}
	if detail.Daily[6].Counts.PromptTokens != 3 || detail.Daily[5].Counts.Errors != 1 || detail.Daily[6].Date != day.Format("2006-01-02") {
		t.Fatalf("daily partition: %+v", detail.Daily)
	}
	for _, path := range []string{"/keys", "/keys/" + id, "/usage?window=week", "/engine", "/settings"} {
		b := call("GET", path, "", 200)
		if strings.Contains(string(b), "PRIVATE TEXT") || strings.Contains(string(b), "secret_hash") || strings.Contains(string(b), secret) {
			t.Fatalf("leak at %s", path)
		}
	}
	call("GET", "/usage?window=forever", "", 400)
	for _, route := range []string{"GET /keys/absent", "PATCH /keys/absent", "POST /keys/absent/pause", "POST /keys/absent/resume", "POST /keys/absent/revoke", "POST /keys/absent/rotate", "GET /keys/alice"} {
		parts := strings.Split(route, " ")
		call(parts[0], parts[1], `{}`, 404)
	}
	var settings consoleSettings
	json.Unmarshal(call("GET", "/settings", "", 200), &settings)
	if !settings.LogRequests || settings.LogRequestsRemembered || !settings.LogPrompts || settings.Region != "nyc" || settings.ConfiguredWebURL != "" || settings.WebURL != "https://infercat.ai" {
		t.Fatal(settings)
	}
}

func TestConsoleTokenNeverReachesServeOutput(t *testing.T) {
	dir, err := os.MkdirTemp("", "ic069-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	gw := newFakeGateway()
	plat := testPlatform(fakeAddr, nil)
	plat.startTunnel = func(context.Context, tunnelOptions) (tunnelServer, error) { return fakeTunnel{}, nil }
	plat.newGateway = func(gatewayOptions, upstream.Upstream, keys.Store, usage.Recorder, func(string, ...any)) (gatewayServer, error) {
		return gw, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errw lockedBuffer
	done := make(chan int, 1)
	engine := fakeEngine(t)
	go func() {
		done <- run(ctx, []string{"serve", "--data-dir", dir, "--upstream", engine, "--console", "127.0.0.1:0"}, &out, &errw, nil, false, plat)
	}()
	select {
	case <-gw.serving:
	case code := <-done:
		t.Fatalf("serve %d: %s", code, errw.String())
	case <-time.After(10 * time.Second):
		t.Fatal("serve timed out")
	}
	got := exec(t, plat, "console", "--data-dir", dir, "--print")
	token := strings.TrimSpace(string(readFile(t, filepath.Join(dir, admin.TokenName))))
	if got.code != 0 || !strings.HasSuffix(strings.TrimSpace(got.out), "/#token="+token) {
		t.Fatal("console --print did not supply the token URL")
	}
	for _, output := range []string{out.String(), errw.String()} {
		if strings.Contains(output, token) || strings.Contains(output, "#token=") {
			t.Fatal("serve leaked the admin token into captured stdout/stderr")
		}
	}
	st, err := admin.Fetch(ctx, dir)
	if err != nil || st.Console == "" {
		t.Fatal(st, err)
	}
	if !strings.Contains(out.String(), "console   http://"+st.Console+"  (open it with: infercat console)") || strings.TrimSpace(got.out) != "http://"+st.Console+"/#token="+token {
		t.Fatal("banner and console --print do not identify the same listener")
	}
	cfg, err := loadConfig(dir)
	if err != nil || cfg.Console != "127.0.0.1:0" {
		t.Fatal(cfg, err)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("serve exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, admin.TokenName)); !os.IsNotExist(err) {
		t.Fatal("token survived stop")
	}
}

func TestConsoleInvalidAddressNeverPersists(t *testing.T) {
	dir := t.TempDir()
	r := exec(t, testPlatform(fakeAddr, nil), "serve", "--data-dir", dir, "--console", "0.0.0.0:9101")
	if r.code == 0 {
		t.Fatal("non-loopback accepted")
	}
	if _, err := os.Stat(configPath(dir)); !os.IsNotExist(err) {
		t.Fatal("refusal persisted config")
	}
}
