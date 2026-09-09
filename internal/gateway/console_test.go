package gateway

import (
	"context"
	"github.com/infercat/infercat/internal/adminkey"
	"github.com/infercat/infercat/internal/usage"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type consoleEvents struct {
	mu     sync.Mutex
	events []usage.Event
}

func (c *consoleEvents) Record(_ context.Context, e usage.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}
func consoleCall(h http.Handler, method, path, secret, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	r.Header.Set("X-Infercat-Remote", "false")
	r.Header.Set("X-Forwarded-Host", "untrusted")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestConsoleBoundaryAndTokenInjection(t *testing.T) {
	var calls int
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer local-only" || r.Header.Get("X-Infercat-Remote") != "true" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("headers were forwarded")
		}
		w.Header().Set("Authorization", "Bearer local-only")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer local.Close()
	store, _ := adminkey.Open(t.TempDir())
	events := &consoleEvents{}
	h := Console(store, strings.TrimPrefix(local.URL, "http://"), func() string { return "local-only" }, events)
	if w := consoleCall(h, "GET", "/console/", "", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	secret, _ := store.Mint(false)
	for _, s := range []string{"", "wrong"} {
		if w := consoleCall(h, "GET", "/console/", s, ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("unauthorized forwarded")
	}
	for _, path := range []string{"/console/api/events", "/console/api/reload", "/console/api/keys/k_abc/extra", "/console/assets/main.js"} {
		if w := consoleCall(h, "GET", path, secret, ""); w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	w := consoleCall(h, "PATCH", "/console/api/settings", secret, `{"name":"private body"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "local-only") || w.Header().Get("Authorization") != "" {
		t.Fatal(w.Code, w.Body)
	}
	if !store.State().InUse {
		t.Fatal("no usage state")
	}
	for _, e := range events.events {
		if e.Kind != "console" || e.Prompt != "" || e.Completion != "" || strings.Contains(e.Endpoint, "private") {
			t.Fatal(e)
		}
	}
	store.Disable()
	if w = consoleCall(h, "GET", "/console/", secret, ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestConsoleSeparateRateAndConcurrencyBudgets(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) }))
	defer local.Close()
	store, _ := adminkey.Open(t.TempDir())
	secret, _ := store.Mint(false)
	h := Console(store, strings.TrimPrefix(local.URL, "http://"), func() string { return "local" }, nil).(*consoleGateway)
	for i := 0; i < 240; i++ {
		if w := consoleCall(h, "GET", "/console/api/settings", secret, ""); w.Code != 200 {
			t.Fatalf("read %d: %d", i, w.Code)
		}
	}
	if w := consoleCall(h, "GET", "/console/api/settings", secret, ""); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code)
	}
	for i := 0; i < 20; i++ {
		if w := consoleCall(h, "PATCH", "/console/api/settings", secret, `{}`); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if w := consoleCall(h, "PATCH", "/console/api/settings", secret, `{}`); w.Code != 429 {
		t.Fatal(w.Code)
	}
	h.reads = consoleBudget{}
	h.writes = consoleBudget{}
	var releases []func()
	for i := 0; i < 6; i++ {
		releases = append(releases, h.acquire(false))
	}
	if h.acquire(false) != nil {
		t.Fatal("seventh read")
	}
	write := h.acquire(true)
	if write == nil || h.acquire(true) != nil {
		t.Fatal("write budget")
	}
	write()
	for _, r := range releases {
		r()
	}
	h.reads.starts = []time.Time{time.Now().Add(-time.Minute - time.Second)}
	if release := h.acquire(false); release == nil {
		t.Fatal("window expired")
	} else {
		release()
	}
}
func TestConsoleRotationAndBounds(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://example.com/")
		w.WriteHeader(302)
	}))
	defer local.Close()
	store, _ := adminkey.Open(t.TempDir())
	secret, _ := store.Mint(false)
	h := Console(store, strings.TrimPrefix(local.URL, "http://"), func() string { return "local" }, nil)
	next, _ := store.Mint(true)
	if w := consoleCall(h, "GET", "/console/", secret, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := consoleCall(h, "PATCH", "/console/api/settings", next, strings.Repeat("x", 17000)); w.Code != 413 {
		t.Fatal(w.Code)
	}
	if w := consoleCall(h, "GET", "/console/api/settings?unexpected=1", next, ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := consoleCall(h, "GET", "/console/", next, ""); w.Code != 502 {
		t.Fatal("followed redirect", w.Code)
	}
	disabled := Console(store, "", func() string { return "local" }, nil)
	if w := consoleCall(disabled, "GET", "/console/", next, ""); w.Code != 404 {
		t.Fatal("listener off", w.Code)
	}
}
