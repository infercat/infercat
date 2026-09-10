package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

func TestResponsesFinishReasonAndReportedUsage(t *testing.T) {
	for _, tc := range []struct {
		reason string
		want   string
		detail string
	}{{"stop", "completed", ""}, {"tool_calls", "completed", ""}, {"", "incomplete", "missing_finish_reason"}, {"other", "incomplete", "unknown_finish_reason"}} {
		t.Run(tc.reason, func(t *testing.T) {
			h := fastResponses(t)
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "partial"}, "finish_reason": tc.reason}}})
			h.up.set("sse", string(raw))
			r := h.post(string(responsesEndpoint), responseBody(`"stream":true`))
			events := responseEvents(t, r.body)
			last := events[len(events)-1]
			if last["type"] != "response."+tc.want {
				t.Fatal(last)
			}
			response := last["response"].(map[string]any)
			if _, ok := response["usage"]; ok {
				t.Fatal("fallback usage leaked")
			}
			if tc.detail != "" && response["incomplete_details"].(map[string]any)["reason"] != tc.detail {
				t.Fatal(response)
			}
			if ev := h.rec.last(t); ev.CompletionTokens != 1 || ev.Meters[0].Charged != 2 {
				t.Fatal(ev)
			}
		})
	}
	h := fastResponses(t)
	h.up.set("json", `{"choices":[{"message":{"content":"answer"},"finish_reason":"stop"}]}`)
	r := h.post(string(responsesEndpoint), responseBody(""))
	var body map[string]any
	json.Unmarshal(r.body, &body)
	if _, ok := body["usage"]; ok {
		t.Fatal("nonstream fallback usage leaked")
	}
}

func TestResponsesFailureHasIdentityAndSentence(t *testing.T) {
	h := fastResponses(t)
	h.gw.idleTimeout = 50 * time.Millisecond
	h.up.set("sse", `{"choices":[{"delta":{"content":"partial"}}]}`, `{"choices":[]}`)
	h.up.mu.Lock()
	h.up.stallAfter = 1
	h.up.mu.Unlock()
	r := h.post(string(responsesEndpoint), responseBody(`"stream":true`))
	events := responseEvents(t, r.body)
	last := events[len(events)-1]
	if last["type"] != "response.failed" || last["response_id"] != events[0]["response_id"] || last["sequence_number"] != float64(len(events)-1) {
		t.Fatal(last)
	}
	errorValue := last["response"].(map[string]any)["error"].(map[string]any)
	if errorValue["code"] != "upstream_error" || !strings.Contains(errorValue["message"].(string), "stopped answering") {
		t.Fatal(errorValue)
	}
	if bytes.Contains(r.body, []byte("[DONE]")) || bytes.Contains(r.body, []byte(`data: {"error"`)) {
		t.Fatal("chat framing leaked")
	}
}

type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadlineCalls, writes int
	fail                  bool
}

func (w *deadlineWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *deadlineWriter) SetWriteDeadline(time.Time) error  { w.deadlineCalls++; return nil }
func (w *deadlineWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.fail {
		return 0, os.ErrDeadlineExceeded
	}
	return w.ResponseRecorder.Write(p)
}
func TestResponsesFailureBeforeOutputAndFailedWriter(t *testing.T) {
	for _, fail := range []bool{false, true} {
		h := fastResponses(t)
		w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder(), fail: fail}
		q := h.gw.newRequest(w, httptest.NewRequest("POST", string(responsesEndpoint), nil))
		q.streamHead()
		before := w.deadlineCalls
		q.fail(errf(CodeClientClosed, 0, "client stopped reading"))
		if q.ev.Code != string(CodeClientClosed) || w.writes != 1 || w.deadlineCalls != before {
			t.Fatalf("failure extended deadline or retried: %+v", w)
		}
		if !fail {
			events := responseEvents(t, w.Body.Bytes())
			if len(events) != 1 || events[0]["type"] != "response.failed" || events[0]["sequence_number"] != float64(0) {
				t.Fatal(events)
			}
		}
	}
}

func TestResponsesToleratesChatToolShapes(t *testing.T) {
	h := fastResponses(t)
	h.up.set("sse",
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"undeclared","arguments":"{"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"undeclared","arguments":"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":null}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`)
	r := h.post(string(responsesEndpoint), responseBody(`"stream":true`))
	final := finalResponse(t, responseEvents(t, r.body))
	call := final["output"].([]any)[0].(map[string]any)
	if call["name"] != "undeclared" || call["arguments"] != "{}" || call["call_id"] != "c" {
		t.Fatal(call)
	}
	if ev := h.rec.last(t); ev.CompletionTokens != 2 || ev.Meters[0].Charged != 9 {
		t.Fatal(ev)
	}
}

func TestResponsesHistoryDoesNotDeclareTools(t *testing.T) {
	// Codex 0.154.0 protocol/models.rs serializes FunctionCall's optional namespace
	// separately from name; stream_events_utils records the original response item.
	for _, namespace := range []string{"ns", ""} {
		body, _ := decodeObject([]byte(`{"tools":[{"type":"namespace","name":"ns","tools":[{"type":"function","name":"f"}]}],"input":[{"type":"function_call","call_id":"c","name":"f","arguments":"{}"}]}`))
		item := body["input"].([]any)[0].(map[string]any)
		if namespace != "" {
			item["namespace"] = namespace
		}
		chat, a, err := translateResponses(body)
		if err != nil {
			t.Fatal(err)
		}
		alias, _ := chatToolName("ns", "f")
		name := chat["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)["name"]
		if name != alias || len(a.tools) != 1 {
			t.Fatalf("history registered an alias: %v %v", name, a.tools)
		}
		for _, name := range []string{"missing", "ns.f"} {
			if _, err := a.historyName("", name); err == nil || !strings.Contains(err.Message, name) || !strings.Contains(err.Message, "namespace") {
				t.Fatal(err)
			}
		}
		a.name("other", "f")
		if _, err := a.historyName("", "f"); err == nil {
			t.Fatal("ambiguous bare name accepted")
		}
		if got, err := a.historyName("ns", "f"); err != nil || got != alias {
			t.Fatal(got, err)
		}
	}
}

func TestResponsesDeveloperAndNonstreamReasoning(t *testing.T) {
	h := fastResponses(t)
	h.up.set("json", `{"choices":[{"message":{"reasoning":"actual thought","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`)
	r := h.post(string(responsesEndpoint), `{"model":"m1","instructions":"first","input":[{"role":"developer","content":"second"},{"role":"user","content":"hello"}]}`)
	var response map[string]any
	json.Unmarshal(r.body, &response)
	items := response["output"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["type"] != "reasoning" || items[1].(map[string]any)["type"] != "message" || bytes.Contains(r.body, []byte("encrypted_content")) {
		t.Fatal(response)
	}
	if items[0].(map[string]any)["summary"].([]any)[0].(map[string]any)["text"] != "actual thought" {
		t.Fatal(items)
	}
	msgs := h.up.body(t)["messages"].([]any)
	for _, m := range msgs[:2] {
		if m.(map[string]any)["role"] != "system" {
			t.Fatal(msgs)
		}
	}
	h.up.set("json", `{"choices":[{"message":{"content":"a"}},{"message":{"content":"b"}}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`)
	h.expectErr(h.post(string(responsesEndpoint), responseBody("")), CodeUpstreamError)
	ev := h.rec.waitFor(t, 2)[1]
	if ev.PromptTokens != 9 || ev.CompletionTokens != 4 || ev.Meters[0].Charged != 0 {
		t.Fatal(ev)
	}
}

func TestModelEndpointClassifierMatchesRouter(t *testing.T) {
	audio, _ := audioEngine(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{}`) })
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"data":[{"id":"image"}]}`) }))
	defer imageServer.Close()
	images, err := upstream.OpenImages(context.Background(), imageServer.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, Config{Transcribe: audio, Speech: audio, Images: images}, nil)
	routes := h.gw.EngineRoutes()
	for _, path := range routes {
		word, class := usage.ModelEndpoint(path)
		want := path != string(modelsEndpoint)
		if (class != "") != want || usage.ModelCall(&usage.Event{Endpoint: path}) != want || endpoint(path).countsAgainstRPM() != want {
			t.Fatalf("classification drift: %s %s %s", path, word, class)
		}
		if want {
			meters := (usage.Event{Endpoint: path}).ResourceMeters()
			if class == "images" {
				if len(meters) != 0 {
					t.Fatal("synthetic image meter", meters)
				}
				explicit := usage.Event{Endpoint: path, Meters: []usage.Meter{{Class: "images", Unit: "images", Measured: 1, Charged: 1}}}
				if got := explicit.ResourceMeters(); len(got) != 1 || got[0].Class != "images" || got[0].Charged != 1 {
					t.Fatal(got)
				}
				continue
			}
			if len(meters) != 1 || meters[0].Class != class {
				t.Fatal(path, meters)
			}
		}
	}
	for _, path := range []string{"/me", "/v1/runs", "/v1/completions", "/v1/responses/extra"} {
		if _, class := usage.ModelEndpoint(path); class != "" {
			t.Fatal(path)
		}
	}
}

func TestResponsesZeroObservedChargeSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	rec, err := usage.NewFileRecorder(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour)
	lim := newLimiter()
	lim.now = func() time.Time { return now }
	adm, e := lim.admit("k", keys.DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = lim.reserve(adm, keys.DefaultLimits(), 0, 100); e != nil {
		t.Fatal(e)
	}
	q := request{g: &Gateway{lim: lim, rec: rec}, r: httptest.NewRequest("POST", string(responsesEndpoint), nil), start: now, kind: chatEndpoint, adm: adm, outcome: outcomeCut, ev: usage.Event{TS: now, KeyID: "k", Endpoint: string(responsesEndpoint), Status: 499}}
	q.finish()
	if err = rec.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := usage.AggregateFile(dir, usage.Filter{Since: day})
	if err != nil {
		t.Fatal(err)
	}
	restarted := newLimiter()
	restarted.now = func() time.Time { return now }
	restarted.seedToday(report, day)
	if got := restarted.counters("k").TodayTokens; got != 100 || lim.counters("k").TodayTokens != got {
		t.Fatal(got)
	}
	if m := report.Total.Meters; m[0].Measured != 0 || m[0].Charged != 100 {
		t.Fatal(m)
	}
}

func TestResponsesUsageAggregatesAsModelCall(t *testing.T) {
	dir := t.TempDir()
	rec, err := usage.NewFileRecorder(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec.Record(context.Background(), usage.Event{TS: now, KeyID: "k", Endpoint: string(responsesEndpoint), Model: "m", Status: 200, TTFTMS: 20, TotalMS: 100, PromptTokens: 9, CompletionTokens: 4})
	rec.Close()
	report, err := usage.AggregateFile(dir, usage.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	s := report.Total
	if s.ModelCalls != 1 || s.AppPolls != 0 || s.Models["m"] != 1 || s.TTFTMedianMS != 20 || s.TotalP95MS != 100 || !s.LastCall.Equal(now) {
		t.Fatalf("Responses lost in aggregate: %+v", s)
	}
}

func TestResponsesRepeatedTerminalMetadataIsEmpty(t *testing.T) {
	h := fastResponses(t)
	h.up.set("sse", `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`)
	r := h.post(string(responsesEndpoint), responseBody(`"stream":true`))
	final := finalResponse(t, responseEvents(t, r.body))
	if final["usage"].(map[string]any)["input_tokens"] != float64(7) {
		t.Fatal(final)
	}
}

func TestImageRouteKeepsRPMAndImageMeter(t *testing.T) {
	h := imagesHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"data":[{"b64_json":"`+tinyImage()+`"}]}`)
	})
	h.post("/v1/images/generations", `{"prompt":"a fox"}`)
	events := h.rec.waitFor(t, 2)
	for _, e := range events {
		if e.Kind == "image" {
			if !usage.ModelCall(&e) || len(e.ResourceMeters()) != 1 || e.ResourceMeters()[0].Class != "images" {
				t.Fatal(e)
			}
			if got := h.gw.Counters(h.key.ID); got.RPMUsed != 1 || got.TodayImages != 1 || got.TodayTokens != 0 {
				t.Fatal(got)
			}
			return
		}
	}
	t.Fatal("image execution event missing")
}
