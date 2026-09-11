package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
)

func TestPinnedAdapterAcknowledgesBeforeModelAndCapturesNativeWrite(t *testing.T) {
	installed := os.Getenv("INFERCAT_AGENT_TEST_INSTALL")
	if installed == "" {
		t.Skip("explicit pinned runtime fixture")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(runtimeDir(installed), runtimeDir(dir)); err != nil {
		t.Fatal(err)
	}
	ks, err := keys.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	k, _, err := ks.Add(context.Background(), "fixture", keys.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	if err = ks.SetLimitsAndAgent(context.Background(), k.ID, k.Limits, &enabled); err != nil {
		t.Fatal(err)
	}
	s, err := runstate.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	m, err := runstate.New(s, func(ctx context.Context, key string, step runstate.Step, acquired func() error) (runstate.StepResult, error) {
		rows, _ := s.List(key)
		d, e := s.Retained(key, rows[0].ID)
		if e != nil || len(d.Trajectory) == 0 {
			t.Error("model reached before raw-prefix commit", e)
		}
		if e = acquired(); e != nil {
			return runstate.StepResult{Settled: true}, e
		}
		var request struct{ Tools []any }
		_ = json.Unmarshal(step.Input, &request)
		n := int32(0)
		if len(request.Tools) > 0 {
			n = calls.Add(1)
		}
		var event string
		if n == 1 || n == 2 {
			event = `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call:write","type":"function","function":{"name":"write","arguments":"{\"file_path\":\"notes.md\",\"content\":\"Line one\\nLine two\\n\"}"}}]},"finish_reason":"tool_calls"}]}`
			if n == 2 {
				event = strings.ReplaceAll(event, "notes.md", "other.md")
			}
		} else {
			event = `{"choices":[{"delta":{"content":"Created notes.md."},"finish_reason":"stop"}]}`
		}
		body := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Planning the fixture.\"}}]}\n\ndata: " + event + "\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4,\"total_tokens\":7}}\n\ndata: [DONE]\n\n"
		if step.Observe == nil {
			t.Error("no shared observer")
		} else if e = step.Observe([]byte(body)); e != nil {
			return runstate.StepResult{Settled: true}, e
		}
		output, _ := json.Marshal(body)
		return runstate.StepResult{Output: output, Dispatched: true, Settled: true}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := StartAdapter(context.Background(), dir, m, ks)
	defer a.Runtime.Close()
	defer m.Close()
	until := time.Now().Add(10 * time.Second)
	for a.Runtime.Status().State != "healthy" && time.Now().Before(until) {
		time.Sleep(20 * time.Millisecond)
	}
	if a.Runtime.Status().State != "healthy" {
		t.Fatal(a.Runtime.Status())
	}
	r, err := m.Submit(k.ID, "agent", "", json.RawMessage(`{"model":"fixture","messages":[{"role":"user","content":"Write notes.md with two lines, then confirm."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(15 * time.Second)
	for time.Now().Before(until) {
		r, err = s.Get(k.ID, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == runstate.Done || r.State == runstate.Failed || r.State == runstate.Cancelled {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	d, err := s.Retained(k.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != runstate.Done {
		log, _ := os.ReadFile(filepath.Join(dir, "agent/host/runtime.log"))
		t.Fatalf("run=%+v\nretained=%s\nlog=%s", r, formatJSON(d), log)
	}
	if calls.Load() != 3 || len(r.Attempts) < 3 {
		t.Fatal("wrong model attempts", calls.Load(), r.Attempts)
	}
	if len(d.Steps) < 2 || d.Steps[0].Kind != "think" || d.Steps[0].Status != "done" {
		t.Fatalf("native thinking identity/status: %+v", d.Steps)
	}
	var file runstate.Captured
	ok := false
	for _, value := range d.Outputs {
		if value.Name == "notes.md" {
			file = value
			ok = true
		}
	}
	if !ok || string(file.Data) != "Line one\nLine two\n" {
		t.Fatalf("native file not captured: %s", formatJSON(d))
	}
	if len(d.Outputs) != 4 {
		t.Fatal("repeated native id lost a capture", len(d.Outputs))
	}
	if !strings.Contains(string(r.Output), "Created notes.md.") {
		t.Fatal("final output missing", string(r.Output))
	}
	t.Logf("native write: %d model attempts, %d raw events, %d steps, %d captured outputs; final text %s", len(r.Attempts), len(d.Trajectory), len(d.Steps), len(d.Outputs), r.Output)
}
func formatJSON(v any) string {
	raw, e := json.Marshal(v)
	if e != nil {
		return fmt.Sprint(e)
	}
	return string(raw)
}

func TestPinnedAdapterCancellationRetainsFinalPrefix(t *testing.T) {
	installed := os.Getenv("INFERCAT_AGENT_TEST_INSTALL")
	if installed == "" {
		t.Skip("explicit pinned runtime fixture")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(runtimeDir(installed), runtimeDir(dir)); err != nil {
		t.Fatal(err)
	}
	ks, _ := keys.NewFileStore(dir)
	k, _, _ := ks.Add(context.Background(), "fixture", keys.Limits{})
	s, _ := runstate.NewStore(dir)
	entered := make(chan struct{}, 1)
	m, err := runstate.New(s, func(ctx context.Context, _ string, _ runstate.Step, acquired func() error) (runstate.StepResult, error) {
		if e := acquired(); e != nil {
			return runstate.StepResult{Settled: true}, e
		}
		entered <- struct{}{}
		<-ctx.Done()
		return runstate.StepResult{Dispatched: true, Settled: true}, ctx.Err()
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := StartAdapter(context.Background(), dir, m, ks)
	defer a.Runtime.Close()
	defer m.Close()
	until := time.Now().Add(10 * time.Second)
	for a.Runtime.Status().State != "healthy" && time.Now().Before(until) {
		time.Sleep(20 * time.Millisecond)
	}
	r, err := m.Submit(k.ID, "agent", "", json.RawMessage(`{"model":"fixture","messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not enter")
	}
	start := time.Now()
	if _, err = m.Cancel(k.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		r, err = s.Get(k.ID, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == runstate.Cancelled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if r.State != runstate.Cancelled {
		t.Fatal("native cancellation did not settle", r.State)
	}
	if r.Reason != "cancelled" {
		t.Fatal("native cancel cause lost", r.Reason)
	}
	d, err := s.Retained(k.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	ended := false
	var types []string
	for _, raw := range d.Trajectory {
		var ev struct{ Type string }
		_ = json.Unmarshal(raw, &ev)
		types = append(types, ev.Type)
		if ev.Type == "turn/end" {
			ended = true
		}
	}
	if !ended || a.Runtime.Status().Restarts != 0 {
		t.Fatal("lost final evidence or restarted runtime", ended, a.Runtime.Status(), types)
	}
	t.Logf("native cancel settled in %s; final turn/end retained; runtime reused", time.Since(start))
}

func TestAdapterInputRefusesUnsupportedBeforeCreation(t *testing.T) {
	for _, raw := range []string{
		`{"model":"m","messages":[{"role":"user","content":[{"type":"audio","data":"x"}]}]}`,
		`{"model":"m","messages":[{"role":"user","content":"hi"}],"unsupported":true}`,
		`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/image"}}]}]}`,
	} {
		if err := ValidateInput(json.RawMessage(raw)); err == nil {
			t.Fatal("unsupported input accepted", raw)
		}
	}
}

func TestPinnedNativeApprovalAllowAndDeny(t *testing.T) {
	installed := os.Getenv("INFERCAT_AGENT_TEST_INSTALL")
	if installed == "" {
		t.Skip("explicit pinned approval fixture")
	}
	for _, allow := range []bool{true, false} {
		t.Run(fmt.Sprint(allow), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "agent"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(runtimeDir(installed), runtimeDir(dir)); err != nil {
				t.Fatal(err)
			}
			cache, err := os.UserCacheDir()
			if err != nil {
				t.Fatal(err)
			}
			proof, err := os.MkdirTemp(cache, "infercat-approval-proof-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(proof)
			target := filepath.Join(proof, "outside.txt")
			ks, _ := keys.NewFileStore(dir)
			k, _, _ := ks.Add(context.Background(), "fixture", keys.Limits{})
			enabled := true
			if err = ks.SetLimitsAndAgent(context.Background(), k.ID, k.Limits, &enabled); err != nil {
				t.Fatal(err)
			}
			store, _ := runstate.NewStore(dir)
			var calls atomic.Int32
			m, _ := runstate.New(store, func(ctx context.Context, _ string, step runstate.Step, acquired func() error) (runstate.StepResult, error) {
				if err := acquired(); err != nil {
					return runstate.StepResult{Settled: true}, err
				}
				var req struct{ Tools []any }
				_ = json.Unmarshal(step.Input, &req)
				delta := map[string]any{"content": "Finished the approval fixture."}
				reason := "stop"
				if len(req.Tools) > 0 && calls.Add(1) == 1 {
					args, _ := json.Marshal(map[string]string{"file_path": target, "content": "approved write", "sandbox_permissions": "danger-full-access", "justification": "Write the isolated proof file."})
					delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call:approval", "type": "function", "function": map[string]string{"name": "write", "arguments": string(args)}}}}
					reason = "tool_calls"
				}
				event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
				raw := []byte("data: " + string(event) + "\n\ndata: [DONE]\n\n")
				if err := step.Observe(raw); err != nil {
					return runstate.StepResult{Settled: true}, err
				}
				return runstate.StepResult{Output: json.RawMessage(`{}`), Dispatched: true, Settled: true}, nil
			}, nil)
			a := StartAdapter(context.Background(), dir, m, ks)
			defer a.Runtime.Close()
			defer m.Close()
			until := time.Now().Add(10 * time.Second)
			for a.Runtime.Status().State != "healthy" && time.Now().Before(until) {
				time.Sleep(20 * time.Millisecond)
			}
			r, err := m.Submit(k.ID, "agent", "", json.RawMessage(`{"model":"fixture","messages":[{"role":"user","content":"Run the isolated approval proof."}]}`))
			if err != nil {
				t.Fatal(err)
			}
			until = time.Now().Add(10 * time.Second)
			var data runstate.Retained
			for time.Now().Before(until) {
				r, _ = store.Get(k.ID, r.ID)
				data, _ = store.Retained(k.ID, r.ID)
				if r.State == runstate.Waiting {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			question := "escalate sandbox to danger-full-access: Write the isolated proof file."
			if r.State != runstate.Waiting || data.Approval == nil || data.Approval.Request != question {
				t.Fatalf("real native question not forwarded: %s %s", r.State, formatJSON(data.Approval))
			}
			found := false
			for _, step := range data.Steps {
				if step.Kind == "wait" && step.Status == "waiting" && step.Text == question {
					found = true
				}
			}
			if !found {
				t.Fatal("waiting step omitted verbatim text")
			}
			if _, err = m.Answer(k.ID, r.ID, data.Approval.ID, allow); err != nil {
				t.Fatal(err)
			}
			until = time.Now().Add(10 * time.Second)
			for time.Now().Before(until) {
				r, _ = store.Get(k.ID, r.ID)
				if r.State == runstate.Done || r.State == runstate.Failed {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if r.State != runstate.Done {
				t.Fatal("native approval did not settle", r.State, r.Reason)
			}
			raw, readErr := os.ReadFile(target)
			if allow && (readErr != nil || string(raw) != "approved write") {
				t.Fatal("allow did not continue", readErr)
			}
			if !allow && !os.IsNotExist(readErr) {
				t.Fatal("deny wrote output", readErr)
			}
			data, _ = store.Retained(k.ID, r.ID)
			outcome := "rejected"
			if allow {
				outcome = "allowed-once"
			}
			audit := false
			note := false
			for _, event := range data.Trajectory {
				var e struct {
					Type string
					Data struct{ Outcome string }
				}
				_ = json.Unmarshal(event, &e)
				if e.Type == "approval/decided" && e.Data.Outcome == outcome {
					audit = true
				}
			}
			for _, step := range data.Steps {
				if step.Kind == "write" && ((allow && strings.Contains(step.Result, "not captured")) || (!allow && step.Status == "failed")) {
					note = true
				}
			}
			if !audit || !note {
				t.Fatal("missing native decision/capture-refusal evidence", audit, note)
			}
			t.Logf("native ask %q -> %s; file exists=%v; run=%s", question, outcome, readErr == nil, r.State)
		})
	}
}

func TestPinnedForceStopRestartsSupervisor(t *testing.T) {
	installed := os.Getenv("INFERCAT_AGENT_TEST_INSTALL")
	if installed == "" {
		t.Skip("explicit pinned generation fixture")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(runtimeDir(installed), runtimeDir(dir)); err != nil {
		t.Fatal(err)
	}
	ks, _ := keys.NewFileStore(dir)
	k, _, _ := ks.Add(context.Background(), "fixture", keys.Limits{})
	store, _ := runstate.NewStore(dir)
	entered := make(chan struct{}, 2)
	var block atomic.Bool
	block.Store(true)
	m, _ := runstate.New(store, func(ctx context.Context, _ string, step runstate.Step, acquired func() error) (runstate.StepResult, error) {
		if err := acquired(); err != nil {
			return runstate.StepResult{Settled: true}, err
		}
		if block.Load() {
			entered <- struct{}{}
			<-ctx.Done()
			return runstate.StepResult{Dispatched: true, Settled: true}, ctx.Err()
		}
		raw := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"ready\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		err := step.Observe(raw)
		return runstate.StepResult{Output: json.RawMessage(`{}`), Dispatched: true, Settled: true}, err
	}, nil)
	a := StartAdapter(context.Background(), dir, m, ks)
	defer a.Runtime.Close()
	defer m.Close()
	until := time.Now().Add(10 * time.Second)
	for a.Runtime.Status().State != "healthy" && time.Now().Before(until) {
		time.Sleep(20 * time.Millisecond)
	}
	input := json.RawMessage(`{"model":"fixture","messages":[{"role":"user","content":"Say ready."}]}`)
	first, err := m.Submit(k.ID, "agent", "", input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Submit(k.ID, "agent", "", input)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first model not entered")
	}
	second, _ = store.Get(k.ID, second.ID)
	if second.State != runstate.Queued {
		t.Fatal("agent kind not serial", second.State)
	}
	old := a.Runtime.Generation()
	block.Store(false)
	if err := m.Policies["agent"].ForceStop(first.ID); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		first, _ = store.Get(k.ID, first.ID)
		if first.State == runstate.Failed && a.Runtime.Status().State == "healthy" && a.Runtime.Generation() != old {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if first.State != runstate.Failed || a.Runtime.Status().State != "healthy" || a.Runtime.Generation() == old {
		t.Fatal("generation did not recover", first.State, a.Runtime.Status())
	}
	until = time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		second, _ = store.Get(k.ID, second.ID)
		if second.State == runstate.Done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if second.State != runstate.Done {
		t.Fatal("queued run did not wait for healthy replacement", second.State, second.Reason)
	}
	third, err := m.Submit(k.ID, "agent", "", input)
	if err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		third, _ = store.Get(k.ID, third.ID)
		if third.State == runstate.Done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if third.State != runstate.Done {
		t.Fatal("replacement did not serve", third.State, third.Reason)
	}
	t.Logf("generation %d stopped; one active session failed without replay; serial successor on generation %d", old, a.Runtime.Generation())
}
