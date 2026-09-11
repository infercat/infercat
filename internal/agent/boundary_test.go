//go:build darwin || linux

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
)

func TestAdapterBoundaryProcess(t *testing.T) {
	if os.Getenv("INFERCAT_BOUNDARY_FIXTURE") == "" {
		return
	}
	listener, err := net.FileListener(os.NewFile(3, "health"))
	if err != nil {
		os.Exit(3)
	}
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ready") }))
	send := func(v any) { raw, _ := json.Marshal(v); fmt.Println(string(raw)) }
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), MaxFrame)
	var id string
	for scan.Scan() {
		var f struct{ Type, ID, Error string }
		_ = json.Unmarshal(scan.Bytes(), &f)
		switch f.Type {
		case "start":
			id = f.ID
			if os.Getenv("INFERCAT_STEP_FIXTURE") == "1" {
				tool := "bash"
				if os.Getenv("INFERCAT_INVALID_STEP") == "true" {
					tool = strings.Repeat("x", 260)
				}
				events := []any{map[string]any{"type": "tool/call", "time": 1000, "seq": 1, "data": map[string]any{"callId": "call", "name": tool}}}
				if os.Getenv("INFERCAT_BATCH_STEP") == "true" {
					events = append(events, map[string]any{"type": "tool/call", "time": 1001, "seq": 2, "data": map[string]any{"callId": "second", "name": "read"}})
				}
				send(map[string]any{"type": "checkpoint", "id": "tool", "run_id": id, "stage": "tool", "events": events})
				continue
			}
			if os.Getenv("INFERCAT_APPROVAL_FIXTURE") == "1" {
				send(map[string]any{"type": "checkpoint", "id": "checkpoint", "run_id": id, "stage": "approval", "events": []any{map[string]string{"type": "turn/start"}}})
			} else {
				send(map[string]any{"type": "checkpoint", "id": "checkpoint", "run_id": id, "stage": "model", "events": []any{map[string]string{"type": "native", "text": strings.Repeat("x", runstate.MaxOutput)}}})
			}
		case "reply":
			if os.Getenv("INFERCAT_STEP_FIXTURE") == "1" {
				if f.Error != "" {
					os.Exit(4)
				}
				if f.ID == "tool" {
					blocks := []any{map[string]any{"type": "tool-result", "toolCallId": "call", "content": []any{map[string]string{"text": "HTTP/1.1 200 OK\r\nheader: value"}}}}
					if os.Getenv("INFERCAT_BATCH_STEP") == "true" {
						blocks = append(blocks, map[string]any{"type": "tool-result", "toolCallId": "second", "content": []any{map[string]string{"text": "second result"}}})
					}
					send(map[string]any{"type": "checkpoint", "id": "terminal", "run_id": id, "stage": "terminal", "events": []any{map[string]any{"type": "tool/result", "time": 2000, "seq": 3, "data": map[string]any{"message": map[string]any{"content": blocks}}}}})
				} else {
					send(map[string]any{"type": "settled", "run_id": id})
				}
				continue
			}
			if os.Getenv("INFERCAT_APPROVAL_FIXTURE") == "1" {
				switch f.ID {
				case "checkpoint":
					send(map[string]any{"type": "approval", "id": "approval", "run_id": id, "request": "Allow fixture?"})
				case "approval":
					send(map[string]any{"type": "probe", "run_id": id})
					send(map[string]any{"type": "checkpoint", "id": "terminal", "run_id": id, "stage": "terminal", "events": []any{map[string]string{"type": "turn/end"}}})
				case "terminal":
					send(map[string]any{"type": "settled", "run_id": id})
				}
			} else {
				if f.Error == "" {
					os.Exit(4)
				}
				send(map[string]any{"type": "settled", "run_id": id, "failed": true})
			}

		}
	}
}

func TestAdapterRefusesDispatchWhenPrefixCannotBeRetained(t *testing.T) {
	dir := t.TempDir()
	store, _ := runstate.NewStore(dir)
	ks, _ := keys.NewFileStore(dir)
	var calls atomic.Int32
	m, _ := runstate.New(store, func(context.Context, string, runstate.Step, func() error) (runstate.StepResult, error) {
		calls.Add(1)
		return runstate.StepResult{}, nil
	}, nil)
	a := &Adapter{manager: m, keys: ks, dir: dir, live: map[string]*session{}}
	binary, _ := os.Executable()
	a.Runtime = StartRuntime(context.Background(), RuntimeOptions{Command: []string{binary, "-test.run=^TestAdapterBoundaryProcess$"}, Dir: dir, Env: []string{"INFERCAT_BOUNDARY_FIXTURE=1"}, Frame: func(raw json.RawMessage) {
		var f frame
		_ = json.Unmarshal(raw, &f)
		a.mu.Lock()
		s := a.live[f.RunID]
		a.mu.Unlock()
		if s != nil {
			s.bytes.Add(int64(len(raw)))
			s.frames <- raw
		}
	}})
	defer a.Runtime.Close()
	defer m.Close()
	waitRuntime(t, a.Runtime, "healthy")
	if err := m.Register("agent", m.Consumer(a.run), runstate.Policy{Serial: true, JoinCancel: true, ForceStop: a.stopRun}); err != nil {
		t.Fatal(err)
	}
	r, err := m.Submit("k_fixture", "agent", "", json.RawMessage(`{"model":"fixture","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		r, _ = store.Get(r.KeyID, r.ID)
		if r.State == runstate.Failed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if r.State != runstate.Failed || calls.Load() != 0 {
		t.Fatal("unacknowledged dispatch", r, calls.Load())
	}
	data, _ := store.Retained(r.KeyID, r.ID)
	if len(data.Trajectory) != 0 {
		t.Fatal("partially retained oversized prefix")
	}
}

func TestApprovalIPCCommitsOnceAndKeepsToolReservation(t *testing.T) {
	dir := t.TempDir()
	store, _ := runstate.NewStore(dir)
	ks, _ := keys.NewFileStore(dir)
	m, _ := runstate.New(store, nil, nil)
	a := &Adapter{manager: m, keys: ks, dir: dir, live: map[string]*session{}}
	binary, _ := os.Executable()
	observed := make(chan error, 1)
	a.Runtime = StartRuntime(context.Background(), RuntimeOptions{Command: []string{binary, "-test.run=^TestAdapterBoundaryProcess$"}, Dir: dir, Env: []string{"INFERCAT_BOUNDARY_FIXTURE=1", "INFERCAT_APPROVAL_FIXTURE=1"}, Frame: func(raw json.RawMessage) {
		var f frame
		_ = json.Unmarshal(raw, &f)
		if f.Type == "probe" {
			d, e := store.Retained("k_fixture", f.RunID)
			if e == nil && (d.Approval == nil || d.Approval.Status != "answered" || !(*d.Approval.Allow)) {
				e = errors.New("answer sent before commit")
			}
			release, admitErr := store.Admit("k_fixture", f.RunID, 8192)
			if release != nil {
				release()
			}
			if !errors.Is(admitErr, runstate.ErrConflict) {
				e = errors.New("resumed tool has no reservation")
			}
			observed <- e
			return
		}
		a.mu.Lock()
		s := a.live[f.RunID]
		a.mu.Unlock()
		if s != nil {
			s.bytes.Add(int64(len(raw)))
			s.frames <- raw
		}
	}})
	defer a.Runtime.Close()
	defer m.Close()
	waitRuntime(t, a.Runtime, "healthy")
	if err := m.Register("agent", m.Consumer(a.run), runstate.Policy{Serial: true, JoinCancel: true, ForceStop: a.stopRun}); err != nil {
		t.Fatal(err)
	}
	r, err := m.Submit("k_fixture", "agent", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		r, _ = store.Get(r.KeyID, r.ID)
		if r.State == runstate.Waiting {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = m.Answer(r.KeyID, r.ID, "approval", true); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-observed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no resumed tool")
	}
	until = time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		r, _ = store.Get(r.KeyID, r.ID)
		if r.State == runstate.Done {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if r.State != runstate.Done {
		t.Fatal(r)
	}
	var ended *runstate.CommittedTerminal
	if _, err = m.Answer(r.KeyID, r.ID, "approval", true); !errors.As(err, &ended) {
		t.Fatal("answer replayed", err)
	}
}

func Test116CNativeCRLFResultSettlesWithoutAbort(t *testing.T) {
	for _, mode := range []string{"crlf", "invalid", "batch"} {
		t.Run(mode, func(t *testing.T) {
			invalid := mode == "invalid"
			dir := t.TempDir()
			store, _ := runstate.NewStore(dir)
			ks, _ := keys.NewFileStore(dir)
			k, _, err := ks.Add(context.Background(), "fixture", keys.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			yes := true
			if err = ks.SetLimitsAndAgent(context.Background(), k.ID, k.Limits, &yes); err != nil {
				t.Fatal(err)
			}
			m, _ := runstate.New(store, nil, nil)
			a := &Adapter{manager: m, keys: ks, dir: dir, live: map[string]*session{}}
			binary, _ := os.Executable()
			a.Runtime = StartRuntime(context.Background(), RuntimeOptions{Command: []string{binary, "-test.run=^TestAdapterBoundaryProcess$"}, Dir: dir, Env: []string{"INFERCAT_BOUNDARY_FIXTURE=1", "INFERCAT_STEP_FIXTURE=1", "INFERCAT_INVALID_STEP=" + fmt.Sprint(invalid), "INFERCAT_BATCH_STEP=" + fmt.Sprint(mode == "batch")}, Frame: func(raw json.RawMessage) {
				var f frame
				_ = json.Unmarshal(raw, &f)
				a.mu.Lock()
				s := a.live[f.RunID]
				a.mu.Unlock()
				if s != nil {
					s.bytes.Add(int64(len(raw)))
					s.frames <- raw
				}
			}})
			defer a.Runtime.Close()
			defer m.Close()
			waitRuntime(t, a.Runtime, "healthy")
			var stops atomic.Int32
			if err := m.Register("agent", m.Consumer(a.run), runstate.Policy{Serial: true, JoinCancel: true, ForceStop: func(id string) error { stops.Add(1); return a.stopRun(id) }}); err != nil {
				t.Fatal(err)
			}
			_, events, unsubscribe, err := store.Subscribe(k.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			defer unsubscribe()
			r, err := m.Submit(k.ID, "agent", "", json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			until := time.Now().Add(5 * time.Second)
			for time.Now().Before(until) {
				r, _ = store.Get(k.ID, r.ID)
				if r.State == runstate.Done || r.State == runstate.Failed {
					break
				}
				time.Sleep(time.Millisecond)
			}
			d, _ := store.Detail(k.ID, r.ID)
			want := 1
			if mode == "batch" {
				want = 2
			}
			if r.State != runstate.Done || stops.Load() != 0 || len(d.Steps) != want {
				t.Fatal(r.State, r.Reason, d.Steps, stops.Load())
			}
			if invalid {
				if d.Steps[0].Status != "failed" || !strings.Contains(d.Steps[0].Result, "step_invalid") || len(d.Outputs) != 0 {
					t.Fatal(d)
				}
				return
			}
			if mode == "batch" {
				published := map[string]string{}
			drain:
				for {
					select {
					case ev := <-events:
						if ev.Step != nil {
							published[ev.Step.ID] = ev.Step.Status
						}
					default:
						break drain
					}
				}
				for _, step := range d.Steps {
					if published[step.ID] != "done" {
						t.Fatal("missing final SSE replacement", published)
					}
					if step.Status != "done" || step.OutputID == "" {
						t.Fatal("batched step left running", d.Steps)
					}
				}
				second, e := store.CapturedOutput(k.ID, r.ID, d.Steps[1].OutputID)
				if e != nil || string(second.Data) != "second result" {
					t.Fatal(second, e)
				}
			}
			if d.Steps[0].Result != "HTTP/1.1 200 OK" {
				t.Fatal(d.Steps)
			}
			o, err := store.CapturedOutput(k.ID, r.ID, d.Steps[0].OutputID)
			if err != nil || string(o.Data) != "HTTP/1.1 200 OK\r\nheader: value" {
				t.Fatal(string(o.Data), err)
			}
		})
	}
}
