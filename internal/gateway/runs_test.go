package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/adminkey"
	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
)

func stepInput(stream bool) runstate.Step {
	return runstate.Step{Route: "/v1/chat/completions", Input: json.RawMessage(fmt.Sprintf(`{"model":"m1","messages":[{"role":"user","content":"hello"}],"max_tokens":20,"stream":%t}`, stream))}
}
func TestRunAdapterSettlementRows(t *testing.T) {
	for _, row := range []string{"rejected_after_count", "queue_timeout", "queue_cancel", "engine_error", "served", "served_stream", "served_stream_no_usage", "cut_nonstream", "cut_stream"} {
		t.Run(row, func(t *testing.T) {
			h := newHarness(t, Config{}, nil)
			h.gw.queueTimeout = 20 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream := strings.Contains(row, "stream") && !strings.Contains(row, "nonstream")
			switch row {
			case "rejected_after_count":
				h.setKey(func(k *keys.Key) { k.Limits.DailyTokens = 1 })
			case "queue_timeout", "queue_cancel":
				if _, e := h.gw.router.text.Queue.acquire(context.Background(), time.Second, time.Second, nil); e != nil {
					t.Fatal(e)
				}
				defer h.gw.router.text.Queue.release()
				if row == "queue_cancel" {
					cancel()
				}
			case "engine_error":
				h.up.set("500")
			case "served_stream", "served_stream_no_usage":
				h.up.set("sse", sseEvents(2, row == "served_stream")...)
				h.up.mu.Lock()
				h.up.gap = 0
				h.up.mu.Unlock()
			case "cut_nonstream":
				h.up.mu.Lock()
				h.up.delay = time.Second
				h.up.mu.Unlock()
				go func() { <-h.up.headers; cancel() }()
			case "cut_stream":
				h.up.set("sse", sseEvents(3, false)...)
				h.up.mu.Lock()
				h.up.gap = 0
				h.up.stallAfter = 2
				h.up.mu.Unlock()
				h.gw.idleTimeout = 25 * time.Millisecond
			}
			result, e := h.gw.ExecuteStep(ctx, h.key.ID, stepInput(stream), func() error { return nil })
			if !result.Settled {
				t.Fatal("finish not observed")
			}
			if strings.HasPrefix(row, "served") != (e == nil) {
				t.Fatalf("error %v", e)
			}
			events := h.rec.waitFor(t, 1)
			if len(events) != 1 {
				t.Fatal("double recording", len(events))
			}
			if len(result.Usage.Meters) != 1 || result.Usage.Meters[0].Class != "tokens" || result.Usage.SettledAt.IsZero() {
				t.Fatal(result.Usage)
			}
			meter := result.Usage.Meters[0]
			counter := h.gw.Counters(h.key.ID)
			if counter.TodayTokens != int(meter.Charged) || counter.InFlight != 0 {
				t.Fatal(counter, meter)
			}
			wantRPM := 1
			if row == "rejected_after_count" || row == "queue_cancel" {
				wantRPM = 0
			}
			if counter.RPMUsed != wantRPM {
				t.Fatalf("RPM %d want %d", counter.RPMUsed, wantRPM)
			}
			switch row {
			case "served":
				if meter.Charged != 8 {
					t.Fatal(meter)
				}
			case "served_stream":
				if meter.Charged != 9 {
					t.Fatal(meter)
				}
			case "served_stream_no_usage":
				if meter.Charged != meter.Measured || result.Usage.CompletionTokens != 2 {
					t.Fatal(meter)
				}
			case "cut_nonstream":
				if meter.Charged != meter.Measured+20 {
					t.Fatal("cut did not charge reservation", meter)
				}
			case "cut_stream":
				if meter.Charged != meter.Measured || result.Usage.CompletionTokens != 2 {
					t.Fatal(meter, result.Usage)
				}
			default:
				if meter.Charged != 0 {
					t.Fatal(meter)
				}
			}
			if events[0].Meters[0] != meter || !events[0].SettledAt.Equal(result.Usage.SettledAt) {
				t.Fatal("result differs from sole recorded settlement")
			}
		})
	}
}
func runManager(t *testing.T, h *harness, kind runstate.Kind) *runstate.Manager {
	t.Helper()
	s, e := runstate.NewStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	registry := map[string]runstate.Kind{}
	if kind != nil {
		registry["test"] = kind
	}
	m, e := runstate.New(s, h.gw.ExecuteStep, registry)
	if e != nil {
		t.Fatal(e)
	}
	h.gw.SetRuns(m)
	t.Cleanup(m.Close)
	return m
}
func waitRun(t *testing.T, m *runstate.Manager, key, id string, want runstate.State) runstate.Run {
	t.Helper()
	var r runstate.Run
	if waitUntil(t, 3*time.Second, "", func() bool {
		var e error
		r, e = m.Store.Get(key, id)
		if e != nil {
			t.Fatal(e)
		}
		return r.State == want
	}) {
		return r
	}
	t.Fatal("state wait", want)
	return runstate.Run{}
}
func TestRunRoutesWaitAndOwnership(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	m := runManager(t, h, func(_ context.Context, r runstate.Run) (runstate.Decision, error) {
		if len(r.Attempts) == 0 {
			return runstate.Decision{Step: ptrStep(stepInput(false))}, nil
		}
		return runstate.Decision{Wait: "tool"}, nil
	})
	made := h.do("POST", "/v1/runs", "Bearer "+testSecret, `{"kind":"test","input":{}}`)
	if made.status != 202 {
		t.Fatal(made)
	}
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(made.body, &created)
	r := waitRun(t, m, h.key.ID, created.ID, runstate.Waiting)
	in, waiting := h.gw.Queue()
	if in != 0 || waiting != 0 || h.gw.Counters(h.key.ID).InFlight != 0 {
		t.Fatal("resources held while waiting")
	}
	if !r.Attempts[0].Settled || r.Attempts[0].AccountingUncertain || r.Attempts[0].Usage.Meters[0].Charged != 8 {
		t.Fatal(r)
	}
	other := defaultKey()
	other.ID = "k_other"
	h.store.set("OTHER-KEY", other)
	if foreign := h.do("GET", "/v1/runs/"+r.ID, "Bearer OTHER-KEY", ""); foreign.status != 404 {
		t.Fatal(foreign)
	}
	for i := 0; i < 2; i++ {
		if cancelled := h.do("DELETE", "/v1/runs/"+r.ID, "Bearer "+testSecret, ""); cancelled.status != 200 {
			t.Fatal(cancelled)
		}
	}
	waitRun(t, m, h.key.ID, r.ID, runstate.Cancelled)
	if got := h.do("GET", "/v1/runs/"+r.ID, "Bearer "+testSecret, ""); got.status != 200 || got.header.Get("Cache-Control") != "no-store" {
		t.Fatal(got)
	}
	h.rec.mu.Lock()
	defer h.rec.mu.Unlock()
	for _, e := range h.rec.events {
		if strings.HasPrefix(e.Endpoint, "/v1/runs") && len(e.Meters) != 0 {
			t.Fatal("bookkeeping charged", e)
		}
	}
}
func ptrStep(s runstate.Step) *runstate.Step { return &s }
func TestRunRoutesNoProductionKindAndLimits(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	runManager(t, h, nil)
	if r := h.do("POST", "/v1/runs", "Bearer "+testSecret, `{"kind":"test","input":{}}`); r.status != 400 {
		t.Fatal(r)
	}
	if r := h.do("POST", "/v1/runs", "Bearer "+testSecret, `{"kind":"test","input":{}} {}`); r.status != 400 {
		t.Fatal(r)
	}
	if r := h.do("GET", "/v1/runs/missing", "", ""); r.status != 401 {
		t.Fatal(r)
	}
	if h.up.requests.Load() != 0 {
		t.Fatal("unregistered kind reached engine")
	}
}
func openEvents(t *testing.T, h *harness, cursor string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, _ := http.NewRequest("GET", h.srv.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+testSecret)
	if cursor != "" {
		req.Header.Set("Last-Event-ID", cursor)
	}
	resp, e := h.srv.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, bufio.NewReader(resp.Body)
}
func readRunEvent(t *testing.T, r *bufio.Reader) runstate.Event {
	t.Helper()
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasPrefix(line, "data: ") {
			var event runstate.Event
			if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); e != nil {
				t.Fatal(e)
			}
			return event
		}
	}
}
func TestRunSSEReplayResetAndRevocation(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.queuedEvery = 10 * time.Millisecond
	m := runManager(t, h, nil)
	resp, reader := openEvents(t, h, "")
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(resp.Status)
	}
	initial := readRunEvent(t, reader)
	if !initial.Reset {
		t.Fatal(initial)
	}
	created, e := m.Store.Create(h.key.ID, "fixture", "interactive", json.RawMessage(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	live := readRunEvent(t, reader)
	if live.RunID != created.ID {
		t.Fatal(live)
	}
	resp2, reader2 := openEvents(t, h, initial.Cursor)
	if replay := readRunEvent(t, reader2); replay.Cursor != live.Cursor {
		t.Fatal(replay, live)
	}
	resp3, _ := openEvents(t, h, "")
	if resp3.StatusCode != 429 {
		t.Fatal(resp3.Status)
	}
	resp3.Body.Close()
	revoked := *h.key
	revoked.Status = keys.Revoked
	h.store.set(testSecret, &revoked)
	for _, body := range []io.ReadCloser{resp.Body, resp2.Body} {
		raw, e := io.ReadAll(body)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(raw), "key_revoked") {
			t.Fatal(string(raw))
		}
		body.Close()
	}
}
func TestRunSSEBadCursorAndShutdown(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	m := runManager(t, h, nil)
	for _, cursor := range []string{"broken", "e_bad:not-a-number", "bad!:1"} {
		r, _ := openEvents(t, h, cursor)
		if r.StatusCode != 400 {
			t.Fatal(r.Status)
		}
		r.Body.Close()
	}
	r, reader := openEvents(t, h, "old-epoch:999")
	if e := readRunEvent(t, reader); !e.Reset {
		t.Fatal("unknown epoch did not reset", e)
	}
	m.Close()
	if _, e := io.ReadAll(r.Body); e != nil {
		t.Fatal(e)
	}
}
func TestRunAdapterRejectsPausedKeyAndBoundsOutput(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Status = keys.Paused })
	result, e := h.gw.ExecuteStep(context.Background(), h.key.ID, stepInput(false), nil)
	if e == nil || result.Dispatched || h.up.requests.Load() != 0 {
		t.Fatal(result, e)
	}
	h.setKey(func(k *keys.Key) { k.Status = keys.Active })
	h.up.set("json", `{"choices":[{"message":{"content":"`+strings.Repeat("x", runstate.MaxOutput)+`"}}],"usage":{"prompt_tokens":4,"completion_tokens":4}}`)
	result, e = h.gw.ExecuteStep(context.Background(), h.key.ID, stepInput(false), nil)
	if e == nil || len(result.Output) != 0 || !result.Settled || result.Usage.Meters[0].Charged != 8 {
		t.Fatal("bounded sink lost settlement", e, result.Usage)
	}
}
func TestRunCORSAndSubmissionBodyBound(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	runManager(t, h, nil)
	out := httptest.NewRecorder()
	cors(h.gw.Handler()).ServeHTTP(out, httptest.NewRequest("OPTIONS", "/v1/events", nil))
	if !strings.Contains(out.Header().Get("Access-Control-Allow-Methods"), "DELETE") || !strings.Contains(out.Header().Get("Access-Control-Allow-Headers"), "last-event-id") {
		t.Fatal(out.Header())
	}
	h.gw.bodies.Store(runstate.MaxLiveHost)
	refused := h.do("POST", "/v1/runs", "Bearer "+testSecret, `{"kind":"test","input":{}}`)
	if refused.status != 429 || h.gw.bodies.Load() != runstate.MaxLiveHost {
		t.Fatal("body bound or release", refused, h.gw.bodies.Load())
	}
	h.gw.bodies.Store(0)
}
func TestRunSSEStaleCursorResetsOnWire(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	m := runManager(t, h, func(_ context.Context, r runstate.Run) (runstate.Decision, error) {
		if len(r.Attempts) == 0 {
			return runstate.Decision{Step: ptrStep(stepInput(false))}, nil
		}
		return runstate.Decision{Output: json.RawMessage(`{}`)}, nil
	})
	first, reader := openEvents(t, h, "")
	cursor := readRunEvent(t, reader).Cursor
	first.Body.Close()
	for i := 0; i < 70; i++ {
		r, e := m.Submit(h.key.ID, "test", "", json.RawMessage(`{}`))
		if e != nil {
			t.Fatal(e)
		}
		waitRun(t, m, h.key.ID, r.ID, runstate.Done)
	}
	resp, replay := openEvents(t, h, cursor)
	defer resp.Body.Close()
	event := readRunEvent(t, replay)
	if !event.Reset || len(event.Runs) != 70 {
		t.Fatal(event.Reset, len(event.Runs))
	}
}
func TestRunAdminRemoteFilterIsNarrow(t *testing.T) {
	var path string
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.RequestURI()
		io.WriteString(w, `{"runs":[]}`)
	}))
	defer local.Close()
	store, e := adminkey.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	secret, e := store.Mint(false)
	if e != nil {
		t.Fatal(e)
	}
	handler := Console(store, strings.TrimPrefix(local.URL, "http://"), func() string { return "local-only" }, nil)
	if out := consoleCall(handler, "GET", "/console/api/runs?key_id=k_a", secret, ""); out.Code != 200 || path != "/api/runs?key_id=k_a" {
		t.Fatal(out.Code, path)
	}
	for _, target := range []string{"/console/api/runs?key_id=../x", "/console/api/runs?key_id=k_a&extra=x", "/console/api/runs?key_id=k_a&key_id=k_b"} {
		if out := consoleCall(handler, "GET", target, secret, ""); out.Code != 400 {
			t.Fatal(target, out.Code)
		}
	}
	if out := consoleCall(handler, "POST", "/console/api/runs", secret, "{}"); out.Code != 404 {
		t.Fatal(out.Code)
	}
	if out := consoleCall(handler, "GET", "/console/api/runs", "", ""); out.Code != 401 {
		t.Fatal(out.Code)
	}
}
