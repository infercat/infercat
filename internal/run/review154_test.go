package run

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// Every retained-payload write publishes a run-state event on the per-key SSE.
func TestAdv4RetainPublishesStateEvents(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_a")
	release, err := s.Admit(r.KeyID, r.ID, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, updates, stop, err := s.Subscribe(r.KeyID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for i := 0; i < 40; i++ {
		if e := s.Retain(r.KeyID, r.ID, json.RawMessage(`{"step":1}`), []json.RawMessage{json.RawMessage(`{"native":"token"}`)}, nil); e != nil {
			t.Log("retain stopped at", i, e)
			break
		}
	}
	n, closed := 0, false
	for {
		select {
		case _, ok := <-updates:
			if !ok {
				closed = true
			} else {
				n++
				continue
			}
		default:
		}
		break
	}
	t.Log("state events published by 40 pure-storage writes:", n, "subscriber closed:", closed)
	if n > 0 {
		t.Errorf("trajectory ingestion emits %d run-state events (ring is 256, subscriber buffer 32)", n)
	}
}

// The consumer's model step flips the public run state to queued and back.
func TestAdv4ConsumerStepFlipsStateToQueued(t *testing.T) {
	s := store(t)
	m := manager(t, s, func(ctx context.Context, _ string, _ Step, acquired func() error) (StepResult, error) {
		if e := acquired(); e != nil {
			return StepResult{Settled: true}, e
		}
		return StepResult{Output: json.RawMessage(`{"ok":1}`), Dispatched: true, Settled: true}, nil
	}, nil)
	var seen []State
	m.Register("test", m.Consumer(func(ctx context.Context, w *Work) (json.RawMessage, error) {
		_, updates, stop, err := s.Subscribe(w.Run.KeyID, "")
		if err != nil {
			return nil, err
		}
		defer stop()
		done := make(chan struct{})
		go func() {
			for e := range updates {
				seen = append(seen, e.State)
			}
			close(done)
		}()
		for i := 0; i < 2; i++ {
			if _, err = w.Step(Step{Input: json.RawMessage(`{}`)}); err != nil {
				return nil, err
			}
		}
		time.Sleep(50 * time.Millisecond)
		stop()
		<-done
		return json.RawMessage(`{"done":1}`), nil
	}), Policy{JoinCancel: true, ForceStop: func(string) {}})
	r := submit(t, m)
	await(t, func() bool { return state(s, r) == Done })
	t.Log("states published to the key's SSE during two model calls:", seen)
}

// Answer distinguishes a foreign live run id from an unknown one.
func TestAdv4AnswerExistenceOracle(t *testing.T) {
	s := store(t)
	m := manager(t, s, nil, nil)
	m.Register("test", m.Consumer(func(ctx context.Context, w *Work) (json.RawMessage, error) {
		return nil, func() error { _, e := w.Approval("p_1", "?"); return e }()
	}), Policy{JoinCancel: true, ForceStop: func(string) {}})
	r := submit(t, m)
	await(t, func() bool { return state(s, r) == Waiting })
	_, live := m.Answer("k_other", r.ID, "p_1", true)
	_, absent := m.Answer("k_other", "r_00000000000000000000000000000000", "p_1", true)
	t.Log("foreign key answering a LIVE run id      :", live)
	t.Log("foreign key answering an UNKNOWN run id  :", absent)
	if !errors.Is(live, absent) {
		t.Errorf("existence oracle: live foreign id -> %v, unknown id -> %v (109 requires both not-found)", live, absent)
	}
}
