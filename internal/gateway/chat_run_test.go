package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/usage"
)

func hostChatHarness(t *testing.T, first []string, imageGate <-chan struct{}) *harness {
	h := imagesHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if imageGate != nil {
			select {
			case <-imageGate:
			case <-r.Context().Done():
				return
			}
		}
		io.WriteString(w, `{"data":[{"b64_json":"`+tinyImage()+`"}]}`)
	})
	h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 1 })
	var calls atomic.Int32
	h.up.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			h.up.set("sse", first...)
		} else {
			h.up.set("sse", `{"choices":[{"delta":{"content":"Your image is being made."},"finish_reason":"stop"}]}`, `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}`)
		}
		h.up.mu.Lock()
		h.up.gap = 0
		h.up.mu.Unlock()
		h.up.handle(w, r)
	})
	return h
}
func imageCall(args string) string {
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "make_1", "type": "function", "function": map[string]any{"name": "make_image", "arguments": args}}}}, "finish_reason": "tool_calls"}}})
	return string(raw)
}
func hostChatBody(stream bool) string {
	raw, _ := json.Marshal(map[string]any{"model": "m1", "messages": []any{map[string]any{"role": "user", "content": "draw me a fox"}}, "stream": stream, "host_tools": []string{"make_image"}, "conversation": "chat-1", "client_request_id": "request-1"})
	return string(raw)
}
func chatRunID(t *testing.T, h *harness) string {
	t.Helper()
	rows, err := h.gw.runs.Store.List(h.key.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Kind == "chat" {
			return r.ID
		}
	}
	t.Fatal("no chat run")
	return ""
}
func TestHostChatHandsOffBeforeAttemptsAndKeepsImagesIndependent(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "body", true: "stream"}[stream], func(t *testing.T) {
			gate := make(chan struct{})
			defer close(gate)
			h := hostChatHarness(t, []string{imageCall(`{"prompt":"watercolour fox","count":2}`), `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`}, gate)
			r := h.post(string(chatEndpoint), hostChatBody(stream))
			if r.status != 200 || !bytes.Contains(r.body, []byte("Your image is being made.")) {
				t.Fatalf("%d %s", r.status, r.body)
			}
			id := chatRunID(t, h)
			run, err := h.gw.runs.Store.Get(h.key.ID, id)
			if err != nil || run.State != runstate.Done || len(run.Attempts) != 2 {
				t.Fatal(run, err)
			}
			if !bytes.Contains(r.body, []byte(id)) {
				t.Fatal("run id absent")
			}
			if stream && bytes.Contains(r.body, []byte(`"name":"make_image"`)) {
				t.Fatal("client can execute host tool")
			}
			if run.Attempts[0].Usage.Meters[0].Charged != 7 || run.Attempts[1].Usage.Meters[0].Charged != 10 {
				t.Fatal(run.Attempts)
			}
			retained, err := h.gw.runs.Store.Retained(h.key.ID, id)
			if err != nil || len(retained.Steps) != 1 || retained.Steps[0].Status != "done" {
				t.Fatal(retained, err)
			}
			rows, _ := h.gw.runs.Store.List(h.key.ID)
			count := 0
			for _, row := range rows {
				if row.Kind == "image" {
					count++
					job, _ := h.gw.runs.Store.Get(h.key.ID, row.ID)
					var in runstate.ImageInput
					json.Unmarshal(job.Input, &in)
					if in.ParentRunID != id || in.ToolCallID != retained.Steps[0].ID || in.Conversation != "chat-1" || job.Priority != "interactive" {
						t.Fatal(job, in)
					}
					if job.State == runstate.Cancelled {
						t.Fatal("chat completion cancelled image")
					}
				}
			}
			if count != 2 {
				t.Fatal(count)
			}
			handoff := 0
			for _, ev := range h.rec.waitFor(t, 3) {
				if ev.Code == usage.RunHandoff {
					handoff++
					if usage.ModelCall(&ev) || ev.Meters[0].Charged != 0 {
						t.Fatal(ev)
					}
				}
			}
			if handoff != 1 {
				t.Fatal("handoff not recorded once", handoff)
			}
			second := h.up.body(t)
			if second["tool_choice"] == "none" {
				t.Fatal(second)
			}
			b, _ := json.Marshal(second)
			if bytes.Contains(b, []byte("chat-1")) || bytes.Contains(b, []byte("request-1")) {
				t.Fatal("metadata in prompt")
			}
		})
	}
}
func TestHostChatInvalidToolBecomesSecondCallErrorResult(t *testing.T) {
	h := hostChatHarness(t, []string{imageCall(`{"prompt":"fox","count":99}`)}, nil)
	r := h.post(string(chatEndpoint), hostChatBody(true))
	if r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	id := chatRunID(t, h)
	run, _ := h.gw.runs.Store.Get(h.key.ID, id)
	retained, _ := h.gw.runs.Store.Retained(h.key.ID, id)
	if run.State != runstate.Done || len(run.Attempts) != 2 || retained.Steps[0].Status != "failed" {
		t.Fatal(run, retained)
	}
	rows, _ := h.gw.runs.Store.List(h.key.ID)
	if len(rows) != 1 {
		t.Fatal("invalid tool submitted jobs", rows)
	}
	raw, _ := json.Marshal(h.up.body(t))
	if !bytes.Contains(raw, []byte("count must be between")) {
		t.Fatal(string(raw))
	}
}
func TestHostChatNoOptInAndNoOfferRemainOrdinary(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	r := h.post(string(chatEndpoint), hostChatBody(false))
	if r.status != 200 || bytes.Contains(r.body, []byte("run_id")) {
		t.Fatal(r.status, string(r.body))
	}
	body := h.up.body(t)
	if body["tools"] != nil || body["host_tools"] != nil {
		t.Fatal(body)
	}
	for _, bad := range []string{`"host_tools":null`, `"host_tools":["make_image"],"tools":[]`} {
		h.expectErr(h.post(string(chatEndpoint), chatBody("m1", 1, bad)), CodeInvalidRequest)
	}
}
func TestHostChatDisconnectCancelsChatNotSubmittedImage(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	h := hostChatHarness(t, []string{imageCall(`{"prompt":"fox","count":1}`)}, gate)
	// Stall the second text response after it starts, while the image is generating.
	var n atomic.Int32
	h.up.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			h.up.set("sse", imageCall(`{"prompt":"fox","count":1}`))
		} else {
			h.up.set("sse", `{"choices":[{"delta":{"content":"pending"}}]}`)
			h.up.mu.Lock()
			h.up.stallAfter = 1
			h.up.mu.Unlock()
		}
		h.up.handle(w, r)
	})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", h.srv.URL+string(chatEndpoint), strings.NewReader(hostChatBody(true)))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, "second model call", func() bool { return n.Load() == 2 })
	cancel()
	res.Body.Close()
	id := chatRunID(t, h)
	waitUntil(t, 3*time.Second, "chat cancelled", func() bool { r, _ := h.gw.runs.Store.Get(h.key.ID, id); return r.State == runstate.Cancelled })
	rows, _ := h.gw.runs.Store.List(h.key.ID)
	for _, r := range rows {
		if r.Kind == "image" && r.State == runstate.Cancelled {
			t.Fatal("child cancelled")
		}
	}
}

func TestHostChatFourthToolIsRefusedWithoutFifthAttempt(t *testing.T) {
	h := hostChatHarness(t, []string{imageCall(`{"prompt":"fox","count":1}`)}, nil)
	h.up.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.up.set("sse", `{"choices":[{"delta":{"content":"Visible words."}}]}`, imageCall(`{"prompt":"fox","count":1}`))
		h.up.handle(w, r)
	})
	r := h.post(string(chatEndpoint), hostChatBody(true))
	if !bytes.Contains(r.body, []byte("Visible words.")) || !bytes.Contains(r.body, []byte("upstream_error")) {
		t.Fatal(string(r.body))
	}
	id := chatRunID(t, h)
	run, _ := h.gw.runs.Store.Get(h.key.ID, id)
	data, _ := h.gw.runs.Store.Retained(h.key.ID, id)
	if run.State != runstate.Failed || len(run.Attempts) != 4 || len(data.Steps) != 4 || data.Steps[3].Status != "failed" {
		t.Fatal(run, data)
	}
	rows, _ := h.gw.runs.Store.List(h.key.ID)
	images := 0
	for _, r := range rows {
		if r.Kind == "image" {
			images++
		}
	}
	if images != 3 {
		t.Fatal("fourth image submitted", images)
	}
}
func TestHostChatMultipleCallsExecuteNone(t *testing.T) {
	h := hostChatHarness(t, []string{`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"make_image","arguments":"{\"prompt\":\"fox\",\"count\":1}"}},{"index":1,"id":"b","function":{"name":"make_image","arguments":"{\"prompt\":\"fox\",\"count\":1}"}}]},"finish_reason":"tool_calls"}]}`}, nil)
	h.post(string(chatEndpoint), hostChatBody(true))
	rows, _ := h.gw.runs.Store.List(h.key.ID)
	if len(rows) != 1 {
		t.Fatal("multiple calls executed", rows)
	}
	run, _ := h.gw.runs.Store.Get(h.key.ID, rows[0].ID)
	if len(run.Attempts) != 2 || run.State != runstate.Done {
		t.Fatal(run)
	}
}
func TestHostChatInputCannotClaimAnotherDelivery(t *testing.T) {
	h := hostChatHarness(t, nil, nil)
	d := &chatDelivery{key: h.key.ID, runID: "original", ready: make(chan struct{}), done: make(chan struct{}), deltas: make(chan chatDelta, 1)}
	close(d.ready)
	h.gw.chatDeliveries.Store("claimed", d)
	defer h.gw.chatDeliveries.Delete("claimed")
	raw, _ := json.Marshal(chatRunInput{Delivery: "claimed"})
	_, err := h.gw.chatRun(context.Background(), &runstate.Work{Run: runstate.Run{ID: "copy", KeyID: h.key.ID, Input: raw}})
	if err == nil {
		t.Fatal("copied run claimed delivery")
	}
	select {
	case <-d.done:
		t.Fatal("copied run closed original delivery")
	default:
	}
	if _, ok := h.gw.chatDeliveries.Load("claimed"); !ok {
		t.Fatal("original delivery removed")
	}
}

func TestHostChatIncompleteToolNeverSubmits(t *testing.T) {
	for _, reason := range []string{"", "length", "unknown"} {
		t.Run(reason, func(t *testing.T) {
			first := strings.Replace(imageCall(`{"prompt":"fox","count":1}`), `"finish_reason":"tool_calls"`, `"finish_reason":"`+reason+`"`, 1)
			h := hostChatHarness(t, []string{first}, nil)
			response := h.post(string(chatEndpoint), hostChatBody(true))
			if !bytes.Contains(response.body, []byte(`"error"`)) {
				t.Fatal(string(response.body))
			}
			rows, _ := h.gw.runs.Store.List(h.key.ID)
			if len(rows) != 1 || rows[0].Kind != "chat" || rows[0].State != runstate.Failed {
				t.Fatal(rows)
			}
			r, _ := h.gw.runs.Store.Get(h.key.ID, rows[0].ID)
			if len(r.Attempts) != 1 {
				t.Fatal(r)
			}
		})
	}
}

func TestHostChatAlreadyCancelledDeliveryEnds(t *testing.T) {
	h := hostChatHarness(t, nil, nil)
	r, err := h.gw.runs.Store.Create(h.key.ID, "chat", "interactive", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.gw.runs.Cancel(h.key.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	recorder := httptest.NewRecorder()
	q := h.gw.newRequest(recorder, httptest.NewRequest("POST", string(chatEndpoint), nil).WithContext(ctx))
	q.key = h.key
	q.n.body = map[string]any{"stream": true}
	d := &chatDelivery{ready: make(chan struct{}), done: make(chan struct{}), deltas: make(chan chatDelta)}
	q.followChat(r.ID, d)
	if ctx.Err() != nil || !strings.Contains(recorder.Body.String(), "host-tool chat ended") {
		t.Fatal(ctx.Err(), recorder.Body.String())
	}
}

func TestHostChatAtomicQueueRefusalBecomesToolResult(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	h := hostChatHarness(t, []string{imageCall(`{"prompt":"fox","count":2}`)}, gate)
	h.setKey(func(k *keys.Key) { k.Limits.MaxQueuedImages = 2 })
	first := submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["occupied"]}`))[0]
	waitImageJob(t, h, first.ID, runstate.Running)
	submittedImages(t, h.post("/v1/images/jobs", `{"prompts":["queued"]}`))
	// The advertised cap is two, but only one queued place remains. Neither sibling fits as a batch.
	response := h.post(string(chatEndpoint), hostChatBody(false))
	if response.status != 200 {
		t.Fatal(string(response.body))
	}
	id := chatRunID(t, h)
	r, _ := h.gw.runs.Store.Get(h.key.ID, id)
	data, _ := h.gw.runs.Store.Retained(h.key.ID, id)
	if r.State != runstate.Done || len(r.Attempts) != 2 || data.Steps[0].Status != "failed" {
		t.Fatal(r, data)
	}
	rows, _ := h.gw.runs.Store.List(h.key.ID)
	if len(rows) != 3 {
		t.Fatal("partial tool batch submitted", rows)
	}
	raw, _ := json.Marshal(h.up.body(t))
	if !bytes.Contains(raw, []byte(`job_ids`)) && !bytes.Contains(raw, []byte(`error`)) {
		t.Fatal(string(raw))
	}
}

func TestHostChatHandoffDoesNotSpendAnExtraMessage(t *testing.T) {
	h := hostChatHarness(t, []string{`{"choices":[{"delta":{"content":"No image requested."},"finish_reason":"stop"}]}`, `{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2}}`}, nil)
	response := h.post(string(chatEndpoint), hostChatBody(false))
	if response.status != 200 {
		t.Fatal(string(response.body))
	}
	counters := h.gw.Counters(h.key.ID)
	if counters.RPMUsed != 1 || counters.TodayTokens != 5 || counters.InFlight != 0 {
		t.Fatal(counters)
	}
	id := chatRunID(t, h)
	r, _ := h.gw.runs.Store.Get(h.key.ID, id)
	if r.State != runstate.Done || len(r.Attempts) != 1 {
		t.Fatal(r)
	}
}
