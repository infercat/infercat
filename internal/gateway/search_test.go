package gateway

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
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/usage"
)

func testSearch(t *testing.T, handler http.HandlerFunc) *Search {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "search.key")
	if err := os.WriteFile(path, []byte("fixture-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSearch(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.client.Timeout != 10*time.Second {
		t.Fatal(s.client.Timeout)
	}
	if err = os.WriteFile(path, []byte("rotated"), 0600); err != nil {
		t.Fatal(err)
	}
	s.endpoint = server.URL + "/search"
	return s
}
func TestSearchProviderBoundary(t *testing.T) {
	var seen atomic.Int32
	s := testSearch(t, func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if r.Method != "POST" || r.URL.Path != "/search" || r.URL.RawQuery != "" || r.Header.Get("x-api-key") != "fixture-secret" {
			t.Error("provider request shape/credential changed")
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 3 || body["query"] == nil || body["numResults"] == nil || body["contents"] == nil {
			t.Error("unexpected provider metadata", body)
		}
		if string(body["query"]) != `"sensitive query"` || string(body["numResults"]) != "3" || string(body["contents"]) != `{"highlights":{"maxCharacters":1600}}` {
			t.Error("wrong bounded provider payload", body)
		}
		io.WriteString(w, `{"results":[{"title":"Release","url":"https://example.test/release","highlights":["First snippet","Second snippet"]}]}`)
	})
	text, valid := s.query(context.Background(), "sensitive query", 3)
	if !valid || text != "1. Release\nhttps://example.test/release\nFirst snippet Second snippet" || seen.Load() != 1 {
		t.Fatal(valid, text, seen.Load())
	}
}
func TestSearchProviderFailuresEmptyAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		valid            bool
	}{
		{"empty", `{"results":[]}`, "no results", 200, true},
		{"error", `{"query":"secret","error":"fixture-secret"}`, "search unavailable", 503, false},
		{"invalid", `not json`, "search unavailable", 200, false},
		{"missing", `{}`, "search unavailable", 200, false},
		{"oversize", strings.Repeat("x", 129<<10), "search unavailable", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testSearch(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) })
			text, valid := s.query(context.Background(), "query", 3)
			if text != tc.want || valid != tc.valid {
				t.Fatal(text, valid)
			}
		})
	}
	s := testSearch(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"title": "界", "url": "https://example.test", "highlights": []string{strings.Repeat("界", 9000)}}}})
	})
	text, valid := s.query(context.Background(), "q", 1)
	if !valid || len(text) > searchOutputLimit || !utf8.ValidString(text) {
		t.Fatal(valid, len(text))
	}
}
func TestSearchNoRedirectOrRetryAndTimeout(t *testing.T) {
	var redirected, attempts atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	s := testSearch(t, func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); http.Redirect(w, r, target.URL, 302) })
	if text, ok := s.query(context.Background(), "q", 1); text != "search unavailable" || ok {
		t.Fatal(text, ok)
	}
	if redirected.Load() != 0 || attempts.Load() != 1 {
		t.Fatal(redirected.Load(), attempts.Load())
	}
	release := make(chan struct{})
	defer close(release)
	s = testSearch(t, func(w http.ResponseWriter, r *http.Request) { <-release })
	s.client.Timeout = 20 * time.Millisecond
	if text, ok := s.query(context.Background(), "q", 1); text != "search unavailable" || ok {
		t.Fatal(text, ok)
	}
}
func TestSearchBudgetAtomicCancellationAndRestart(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.SearchPerDay = 1 })
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	h.gw.cfg.Search = testSearch(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"results":[]}`)
	})
	now := time.Date(2026, 9, 11, 23, 59, 58, 0, time.UTC)
	h.gw.lim.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := h.gw.search(ctx, h.key, "never log this query", 3); done <- err }()
	<-entered
	if _, err := h.gw.search(context.Background(), h.key, "second", 3); err == nil {
		t.Fatal("parallel budget admission")
	} else if e, ok := err.(*gwError); !ok || e.Code != CodeBudgetExhausted || e.RetryAfter != 2 {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	event := h.rec.last(t)
	raw, _ := json.Marshal(event)
	if bytes.Contains(raw, []byte("query")) || bytes.Contains(raw, []byte("fixture-secret")) || event.PromptTokens != 0 || event.CompletionTokens != 0 || len(event.Meters) != 1 || event.Meters[0].Charged != 1 || event.Meters[0].Measured != 0 {
		t.Fatal(string(raw))
	}
	rep, err := usage.Aggregate(bytes.NewReader(raw), usage.Filter{Since: now.UTC().Truncate(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	restored := newLimiter()
	restored.now = h.gw.lim.now
	restored.seedToday(rep, now.UTC().Truncate(24*time.Hour))
	if c := restored.counters(h.key.ID); c.TodaySearches != 1 || c.TodayTokens != 0 || c.RPMUsed != 0 {
		t.Fatal(c)
	}
}
func TestSearchCancelBeforeDispatchAndDefaults(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	var calls atomic.Int32
	h.gw.cfg.Search = testSearch(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, `{"results":[]}`) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.gw.search(ctx, h.key, "q", 3); err != context.Canceled {
		t.Fatal(err)
	}
	if calls.Load() != 0 || h.gw.Counters(h.key.ID).TodaySearches != 0 {
		t.Fatal("cancel charged or dispatched")
	}
	for _, limit := range []int{0, -1, 2} {
		if got := (keys.Limits{SearchPerDay: limit}).Budgets().Amount("search", "day"); got != map[int]int{0: 50, -1: -1, 2: 2}[limit] {
			t.Fatal(got)
		}
	}
}
