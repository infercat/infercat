package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
)

func searchCall(args string) string {
	return strings.ReplaceAll(imageCall(args), `make_image`, `web_search`)
}
func searchBody(stream bool, tools ...string) string {
	var body map[string]any
	json.Unmarshal([]byte(hostChatBody(stream)), &body)
	body["host_tools"] = tools
	b, _ := json.Marshal(body)
	return string(b)
}
func scriptedSearchChat(t *testing.T, replies []string, provider http.HandlerFunc) (*harness, *atomic.Int32) {
	t.Helper()
	h := hostChatHarness(t, nil, nil)
	h.gw.cfg.Search = testSearch(t, provider)
	calls := new(atomic.Int32)
	h.up.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		if n >= len(replies) {
			t.Error("extra model call", n)
			http.Error(w, "extra call", 500)
			return
		}
		h.up.set("sse", replies[n], `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`)
		h.up.handle(w, r)
	})
	return h, calls
}

const searchAnswer = `{"choices":[{"delta":{"content":"Here is the answer."},"finish_reason":"stop"}]}`

func TestSearchChatRoundsAndExactCapturedResults(t *testing.T) {
	for _, tc := range []struct {
		name             string
		rounds           []string
		searches, images int
	}{
		{"search then image", []string{searchCall(`{"query":"release news"}`), imageCall(`{"prompt":"fox","count":1}`)}, 1, 1},
		{"two searches then image", []string{searchCall(`{"query":"first","count":1}`), searchCall(`{"query":"second","count":5}`), imageCall(`{"prompt":"fox","count":1}`)}, 2, 1},
		{"third search refused", []string{searchCall(`{"query":"first"}`), searchCall(`{"query":"second"}`), searchCall(`{"query":"third"}`)}, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var providers atomic.Int32
			h, calls := scriptedSearchChat(t, append(tc.rounds, searchAnswer), func(w http.ResponseWriter, r *http.Request) {
				providers.Add(1)
				io.WriteString(w, `{"results":[{"title":"Release","url":"https://example.test/release","highlights":["source text"]}]}`)
			})
			res := h.post(string(chatEndpoint), searchBody(true, "web_search", "make_image"))
			if !bytes.Contains(res.body, []byte("Here is the answer.")) || bytes.Contains(res.body, []byte(`"error"`)) {
				t.Fatal(string(res.body))
			}
			id := chatRunID(t, h)
			row, _ := h.gw.runs.Store.Get(h.key.ID, id)
			retained, _ := h.gw.runs.Store.Retained(h.key.ID, id)
			if row.State != runstate.Done || len(row.Attempts) != len(tc.rounds)+1 || int(calls.Load()) != len(tc.rounds)+1 || int(providers.Load()) != tc.searches {
				t.Fatal(row, calls.Load(), providers.Load())
			}
			last := h.up.body(t)
			messages := last["messages"].([]any)
			for _, step := range retained.Steps {
				if step.Tool != "web_search" {
					continue
				}
				out := retained.Outputs[step.OutputID]
				found := false
				for _, m := range messages {
					v := m.(map[string]any)
					if v["role"] == "tool" && v["content"] == string(out.Data) {
						found = true
					}
				}
				if !found || len(out.Data) > searchOutputLimit || step.Kind != "search" {
					t.Fatal("capture differs from model result", step, out)
				}
				got := h.get("/v1/runs/" + id + "/outputs/" + step.OutputID)
				if got.status != 200 || !bytes.Equal(got.body, out.Data) {
					t.Fatal(got.status, string(got.body))
				}
			}
			rows, _ := h.gw.runs.Store.List(h.key.ID)
			images := 0
			for _, r := range rows {
				if r.Kind == "image" {
					images++
				}
			}
			if images != tc.images || h.gw.Counters(h.key.ID).TodaySearches != tc.searches {
				t.Fatal(images, h.gw.Counters(h.key.ID))
			}
			if len(tc.rounds) == 3 && last["tool_choice"] != "none" {
				t.Fatal("last call may request tools")
			}
		})
	}
}
func TestSearchChatFailuresContinueAndMalformedForcesProse(t *testing.T) {
	for _, tc := range []struct {
		name, args, body, want string
		status, calls          int
		measured               float64
	}{
		{"empty", `{"query":"q"}`, `{"results":[]}`, "no results", 200, 1, 1},
		{"error", `{"query":"q"}`, `provider secret`, "search unavailable", 503, 1, 0},
		{"timeout", `{"query":"q"}`, `{"results":[]}`, "search unavailable", 200, 1, 0},
		{"malformed", `{"query":"q","count":null}`, `{}`, "search needs", 200, 0, 0},
		{"fraction", `{"query":"q","count":1.5}`, `{}`, "search needs", 200, 0, 0},
		{"too many", `{"query":"q","count":6}`, `{}`, "search needs", 200, 0, 0},
		{"unknown field", `{"query":"q","key":"secret"}`, `{}`, "search needs", 200, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var providers atomic.Int32
			h, calls := scriptedSearchChat(t, []string{searchCall(tc.args), searchAnswer}, func(w http.ResponseWriter, r *http.Request) {
				providers.Add(1)
				if tc.name == "timeout" {
					time.Sleep(30 * time.Millisecond)
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			if tc.name == "timeout" {
				h.gw.cfg.Search.client.Timeout = 5 * time.Millisecond
			}
			res := h.post(string(chatEndpoint), searchBody(false, "web_search"))
			if res.status != 200 || !bytes.Contains(res.body, []byte("Here is the answer.")) || calls.Load() != 2 || int(providers.Load()) != tc.calls {
				t.Fatal(res.status, string(res.body), calls.Load(), providers.Load())
			}
			b, _ := json.Marshal(h.up.body(t))
			if !bytes.Contains(b, []byte(tc.want)) {
				t.Fatal(string(b))
			}
			if tc.calls == 0 && h.up.body(t)["tool_choice"] != "none" {
				t.Fatal("malformed request did not force prose")
			}
			h.rec.mu.Lock()
			defer h.rec.mu.Unlock()
			n := 0
			for _, e := range h.rec.events {
				if e.Kind == "search" {
					n++
					if e.Meters[0].Measured != tc.measured || e.Meters[0].Charged != 1 || e.Prompt != "" || e.Completion != "" {
						t.Fatal(e)
					}
				}
			}
			if n != tc.calls {
				t.Fatal(n)
			}
		})
	}
}
func TestSearchOnlyHostAndOptInIntersection(t *testing.T) {
	h, _ := scriptedSearchChat(t, []string{searchCall(`{"query":"q"}`), searchAnswer}, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"results":[]}`) })
	h.gw.cfg.Images = nil
	// Remove the image destination too: the offer uses the router's configured destination.
	delete(h.gw.router.routes, string(imagesEndpoint))
	res := h.post(string(chatEndpoint), searchBody(false, "make_image", "web_search"))
	if res.status != 200 || !bytes.Contains(res.body, []byte("Here is the answer.")) {
		t.Fatal(res.status, string(res.body))
	}
	tools := h.up.body(t)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["function"].(map[string]any)["name"] != "web_search" {
		t.Fatal(tools)
	}
	var me map[string]any
	json.Unmarshal(h.get("/me").body, &me)
	if b, _ := json.Marshal(me["host_tools"]); string(b) != `["web_search"]` {
		t.Fatal(me)
	}
	for _, bad := range []string{`["web_search","web_search"]`, `["unknown"]`, `[1]`, `null`} {
		h.expectErr(h.post(string(chatEndpoint), chatBody("m1", 1, `"host_tools":`+bad)), CodeInvalidRequest)
	}
	h.expectErr(h.post(string(chatEndpoint), chatBody("m1", 1, `"host_tools":["web_search"],"tool_choice":"none"`)), CodeInvalidRequest)
}
func TestSearchChatBudgetEnvelopeAndNoExtraRPM(t *testing.T) {
	var provider atomic.Int32
	h, calls := scriptedSearchChat(t, []string{searchCall(`{"query":"first"}`), searchCall(`{"query":"second"}`)}, func(w http.ResponseWriter, r *http.Request) { provider.Add(1); io.WriteString(w, `{"results":[]}`) })
	h.setKey(func(k *keys.Key) { k.Limits.SearchPerDay = 1 })
	now := time.Date(2026, 9, 11, 23, 59, 0, 0, time.UTC)
	h.gw.lim.now = func() time.Time { return now }
	res := h.post(string(chatEndpoint), searchBody(true, "web_search"))
	if !bytes.Contains(res.body, []byte(`"code":"budget_exhausted"`)) || !bytes.Contains(res.body, []byte(`"retry_after":60`)) || !bytes.Contains(res.body, []byte("search budget")) {
		t.Fatal(string(res.body))
	}
	row, _ := h.gw.runs.Store.Get(h.key.ID, chatRunID(t, h))
	if row.Reason != "budget_exhausted retry_after=60" || provider.Load() != 1 || calls.Load() != 2 {
		t.Fatal(row, provider.Load(), calls.Load())
	}
	if !bytes.Contains(h.get("/me").body, []byte(`"host_tools":["make_image","web_search"]`)) {
		t.Fatal("budget hid capability")
	}
	if c := h.gw.Counters(h.key.ID); c.RPMUsed != 2 || c.TodaySearches != 1 {
		t.Fatal(c)
	}
}

func TestSearchSettlementUsesSettlementDay(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	var mu sync.Mutex
	now := time.Date(2026, 9, 11, 23, 59, 59, 0, time.UTC)
	h.gw.lim.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	h.gw.cfg.Search = testSearch(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		now = now.Add(2 * time.Second)
		mu.Unlock()
		io.WriteString(w, `{"results":[]}`)
	})
	if _, err := h.gw.search(t.Context(), h.key, "q", 3); err != nil {
		t.Fatal(err)
	}
	e := h.rec.last(t)
	if e.TS.Day() != 11 || e.SettledAt.Day() != 12 || e.Meters[0].Measured != 1 || h.gw.Counters(h.key.ID).TodaySearches != 1 {
		t.Fatal(e)
	}
}

func TestSearchLoggingKeepsTheExistingExplicitOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "explicit"}[enabled], func(t *testing.T) {
			h, _ := scriptedSearchChat(t, []string{searchCall(`{"query":"private query"}`), searchAnswer}, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"results":[{"title":"Private result","url":"https://example.test","highlights":["private snippet"]}]}`)
			})
			h.gw.cfg.LogPrompts = enabled
			res := h.post(string(chatEndpoint), searchBody(false, "web_search"))
			if res.status != 200 {
				t.Fatal(res.status, string(res.body))
			}
			h.rec.mu.Lock()
			defer h.rec.mu.Unlock()
			logged := false
			for _, e := range h.rec.events {
				if e.Kind == "search" && (e.Prompt != "" || e.Completion != "") {
					t.Fatal("search telemetry contains text")
				}
				if !enabled && (e.Prompt != "" || e.Completion != "") {
					t.Fatal("default logging contains text")
				}
				if strings.Contains(e.Prompt, "private snippet") {
					logged = true
				}
			}
			if logged != enabled {
				t.Fatal("changed explicit prompt logging", logged)
			}
		})
	}
}
