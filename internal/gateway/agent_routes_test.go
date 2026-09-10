package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
)

func TestAgentRoutesScopeCorrelationApprovalAndOutputs(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	m := runManager(t, h, nil)
	var answers atomic.Int32
	m.Register("agent", m.Consumer(func(ctx context.Context, w *runstate.Work) (json.RawMessage, error) {
		release, err := w.Store.Admit(w.Run.KeyID, w.Run.ID, 8192)
		if err != nil {
			return nil, err
		}
		err = w.Store.Retain(w.Run.KeyID, w.Run.ID, nil, nil, map[string]runstate.Captured{"out": {Name: "notes.md", MIME: "text/plain", Data: []byte("captured")}}, runstate.StepEvent{ID: "step", Type: "step", At: time.Now().UTC(), Kind: "write", Status: "done", OutputID: "out"})
		release()
		if err != nil {
			return nil, err
		}
		_, err = w.Approval("p_1", "Allow fixture?")
		if err != nil {
			return nil, err
		}
		answers.Add(1)
		return json.RawMessage(`{"text":"complete"}`), nil
	}), runstate.Policy{Serial: true, JoinCancel: true, ForceStop: func(string) error { return nil }})
	body := `{"kind":"agent","client_request_id":"chat_1","input":{"model":"m1","messages":[{"role":"user","content":"hello"}]}}`
	if got := h.do("POST", "/v1/runs", "Bearer "+testSecret, body); got.status != 400 {
		t.Fatal("key opted in by default", got)
	}
	h.setKey(func(k *keys.Key) { k.Agent = true })
	var me map[string]any
	_ = json.Unmarshal(h.do("GET", "/me", "Bearer "+testSecret, "").body, &me)
	if me["agent"] != true {
		t.Fatal("missing capability")
	}
	for _, id := range []string{"", "not valid", strings.Repeat("x", 129)} {
		invalid := strings.Replace(body, "chat_1", id, 1)
		if got := h.do("POST", "/v1/runs", "Bearer "+testSecret, invalid); got.status != 400 {
			t.Fatal("invalid correlation accepted", got)
		}
	}
	var ids []string
	for i := range 2 {
		got := h.do("POST", "/v1/runs", "Bearer "+testSecret, body)
		if got.status != 202 {
			t.Fatal(got)
		}
		var v struct{ ID string }
		_ = json.Unmarshal(got.body, &v)
		ids = append(ids, v.ID)
		if i == 0 {
			waitRun(t, m, h.key.ID, v.ID, runstate.Waiting)
		}
	}
	if ids[0] == ids[1] {
		t.Fatal("correlation deduplicated submission")
	}
	for _, id := range ids {
		waitRun(t, m, h.key.ID, id, runstate.Waiting)
		got := h.do("GET", "/v1/runs/"+id, "Bearer "+testSecret, "")
		var v struct {
			ClientRequestID string `json:"client_request_id"`
			Steps           []runstate.StepEvent
			Approval        *runstate.Approval
		}
		_ = json.Unmarshal(got.body, &v)
		if got.status != 200 || v.ClientRequestID != "chat_1" || len(v.Steps) != 1 || v.Approval.Status != "pending" {
			t.Fatal(got)
		}
		if output := h.do("GET", "/v1/runs/"+id+"/outputs/out", "Bearer "+testSecret, ""); output.status != 200 || string(output.body) != "captured" {
			t.Fatal(output)
		}
		if answer := h.do("POST", "/v1/runs/"+id+"/approval", "Bearer "+testSecret, `{"id":"p_1","allow":false}`); answer.status != 200 {
			t.Fatal(answer)
		}
		waitRun(t, m, h.key.ID, id, runstate.Done)
		if again := h.do("POST", "/v1/runs/"+id+"/approval", "Bearer "+testSecret, `{"id":"p_1","allow":true}`); again.status != 200 {
			t.Fatal("replayed approval", again)
		}
	}
	if answers.Load() != 2 {
		t.Fatal(answers.Load())
	}
	other := defaultKey()
	other.ID = "k_other"
	h.store.set("OTHER", other)
	if got := h.do("GET", "/v1/runs/"+ids[0]+"/outputs/out", "Bearer OTHER", ""); got.status != 404 {
		t.Fatal("cross-key output", got)
	}
	var listing struct{ Runs []runstate.Summary }
	_ = json.Unmarshal(h.do("GET", "/v1/runs", "Bearer "+testSecret, "").body, &listing)
	if len(listing.Runs) != 2 || listing.Runs[0].ClientRequestID != "chat_1" {
		t.Fatal(listing)
	}
}

func TestStepObserverFailureStillSettlesOnce(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.set("sse", sseEvents(2, true)...)
	step := stepInput(true)
	calls := 0
	step.Observe = func([]byte) error { calls++; return errors.New("reader disconnected") }
	result, err := h.gw.ExecuteStep(context.Background(), h.key.ID, step, func() error { return nil })
	if err == nil || !result.Settled || calls == 0 {
		t.Fatal(result, err, calls)
	}
	if h.gw.Counters(h.key.ID).InFlight != 0 || len(h.rec.waitFor(t, 1)) != 1 {
		t.Fatal("observer escaped settlement")
	}
}

func TestAgentUnavailableAndExplicitCapabilityFalse(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	m := runManager(t, h, nil)
	if err := m.Register("agent", func(context.Context, runstate.Run) (runstate.Decision, error) {
		t.Error("unavailable agent dispatched")
		return runstate.Decision{}, nil
	}, runstate.Policy{Validate: func(json.RawMessage) error { return runstate.ErrAgentUnavailable }}); err != nil {
		t.Fatal(err)
	}
	var me map[string]any
	_ = json.Unmarshal(h.do("GET", "/me", "Bearer "+testSecret, "").body, &me)
	if value, exists := me["agent"]; !exists || value != false {
		t.Fatal("registered capability must be explicit false", me)
	}
	h.setKey(func(k *keys.Key) { k.Agent = true })
	got := h.do("POST", "/v1/runs", "Bearer "+testSecret, `{"kind":"agent","input":{"model":"m1","messages":[{"role":"user","content":"hello"}]}}`)
	if got.status != 503 || !strings.Contains(string(got.body), "agent_unavailable") || strings.Contains(string(got.body), "run store unavailable") {
		t.Fatal(got)
	}
	rows, _ := m.Store.List(h.key.ID)
	if len(rows) != 0 {
		t.Fatal("refusal created a run")
	}
}

func TestAgentAttemptStoresFinalMessageNotSSE(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Agent = true })
	events := sseEvents(2, true)
	events = append(events, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	h.up.set("sse", events...)
	step := stepInput(true)
	step.RequireAgent = true
	result, err := h.gw.ExecuteStep(context.Background(), h.key.ID, step, func() error { return nil })
	if err != nil || !result.Settled {
		t.Fatal(result, err)
	}
	var message struct{ Role, Content string }
	if json.Unmarshal(result.Output, &message) != nil || message.Role != "assistant" || message.Content == "" || strings.Contains(string(result.Output), "data:") {
		t.Fatal("not a compact final message", string(result.Output))
	}
	raw := []byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call:1\",\"type\":\"function\",\"function\":{\"name\":\"write\",\"arguments\":\"{\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
	compact, err := agentMessage(raw)
	if err != nil || !strings.Contains(string(compact), `"arguments":"{}"`) {
		t.Fatal(string(compact), err)
	}
}

func Test116CReadAndApprovalSpendDetachedRPM(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	m := runManager(t, h, nil)
	if err := m.Register("test", func(context.Context, runstate.Run) (runstate.Decision, error) {
		return runstate.Decision{Output: json.RawMessage(`{}`)}, nil
	}, runstate.Policy{}); err != nil {
		t.Fatal(err)
	}
	r, err := m.Submit(h.key.ID, "test", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, m, h.key.ID, r.ID, runstate.Done)
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 3; k.Limits.MaxConcurrent = 1 })
	held, e := h.gw.lim.admit(h.key.ID, h.key.Limits)
	if e != nil {
		t.Fatal(e)
	}
	defer h.gw.lim.settle(held, false, 0)
	for _, path := range []string{"/v1/runs", "/v1/runs/" + r.ID} {
		if got := h.do("GET", path, "Bearer "+testSecret, ""); got.status != 200 {
			t.Fatal(got)
		}
	}
	if got := h.do("POST", "/v1/runs/"+r.ID+"/approval", "Bearer "+testSecret, `{"id":"p","allow":true}`); got.status != 429 {
		t.Fatal("approval escaped RPM", got)
	}
	if c := h.gw.Counters(h.key.ID); c.InFlight != 1 || c.RPMUsed != 3 {
		t.Fatal(c)
	}
}
func Test116CServedModelConversionFallbackNeverFailsCall(t *testing.T) {
	for _, bad := range []string{`{"choices":[{"delta":{"content":"paid"}}]}`, `not-json`, `{"error":{"message":"fixture"}}`} {
		t.Run(bad, func(t *testing.T) {
			h := newHarness(t, Config{}, nil)
			h.setKey(func(k *keys.Key) { k.Agent = true })
			h.up.set("sse", bad)
			step := stepInput(true)
			step.RequireAgent = true
			result, err := h.gw.ExecuteStep(context.Background(), h.key.ID, step, func() error { return nil })
			if err != nil || !result.Settled || !strings.Contains(string(result.Output), `"raw_fallback":true`) {
				t.Fatal(string(result.Output), err)
			}
			if len(h.rec.waitFor(t, 1)) != 1 {
				t.Fatal("settled twice")
			}
		})
	}
}
