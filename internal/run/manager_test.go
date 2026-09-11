package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/infercat/infercat/internal/usage"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func await(t *testing.T, fn func() bool) {
	t.Helper()
	awaitFor(t, 3*time.Second, "condition timed out", fn)
}

// Empty what lets the caller retain its detailed timeout assertion.
func awaitFor(t *testing.T, d time.Duration, what string, fn func() bool) bool {
	t.Helper()
	until := time.Now().Add(d)
	for time.Now().Before(until) {
		if fn() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	if what != "" {
		t.Fatal(what)
	}
	return false
}
func manager(t *testing.T, s *Store, ex Executor, k Kind) *Manager {
	t.Helper()
	m, e := New(s, ex, map[string]Kind{"test": k})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	return m
}
func submit(t *testing.T, m *Manager) Run {
	t.Helper()
	r, e := m.Submit("k_a", "test", "", json.RawMessage(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func state(s *Store, r Run) State { v, _ := s.Get(r.KeyID, r.ID); return v.State }
func TestWaitReleasesStepAndResume(t *testing.T) {
	s := store(t)
	var slots atomic.Int32
	var resume atomic.Bool
	ex := func(ctx context.Context, key string, step Step, acquired func() error) (StepResult, error) {
		slots.Add(1)
		defer slots.Add(-1)
		if e := acquired(); e != nil {
			return StepResult{Settled: true}, e
		}
		return StepResult{Output: json.RawMessage(`{"answer":1}`), Dispatched: true, Settled: true}, nil
	}
	k := func(_ context.Context, r Run) (Decision, error) {
		if len(r.Attempts) == 0 {
			return Decision{Step: &Step{Route: "/v1/chat/completions", Input: json.RawMessage(`{}`)}}, nil
		}
		if !resume.Load() {
			return Decision{Wait: "tool"}, nil
		}
		return Decision{Output: r.Attempts[0].Output}, nil
	}
	m := manager(t, s, ex, k)
	r := submit(t, m)
	await(t, func() bool { return state(s, r) == Waiting })
	if slots.Load() != 0 {
		t.Fatal("capacity held while waiting")
	}
	m.mu.Lock()
	active := m.active[r.ID] != nil
	m.mu.Unlock()
	if active {
		await(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.active[r.ID] == nil })
	}
	resume.Store(true)
	if e := m.Resume(r.KeyID, r.ID); e != nil {
		t.Fatal(e)
	}
	await(t, func() bool { return state(s, r) == Done })
	got, _ := s.Get(r.KeyID, r.ID)
	if !got.Attempts[0].Settled || got.Attempts[0].AccountingUncertain || string(got.Output) != `{"answer":1}` {
		t.Fatal(got)
	}
}
func TestCancelEveryLiveState(t *testing.T) {
	for _, st := range []State{Queued, Running, Waiting} {
		t.Run(string(st), func(t *testing.T) {
			s := store(t)
			entered := make(chan struct{})
			ex := func(ctx context.Context, _ string, _ Step, acquired func() error) (StepResult, error) {
				if st == Running {
					if e := acquired(); e != nil {
						return StepResult{Settled: true}, e
					}
				}
				close(entered)
				<-ctx.Done()
				return StepResult{Dispatched: st == Running, Settled: true}, ctx.Err()
			}
			k := func(_ context.Context, r Run) (Decision, error) {
				if st == Waiting {
					return Decision{Wait: "approval"}, nil
				}
				return Decision{Step: &Step{Input: json.RawMessage(`{}`)}}, nil
			}
			m := manager(t, s, ex, k)
			r := submit(t, m)
			if st == Waiting {
				await(t, func() bool { return state(s, r) == Waiting })
			} else {
				<-entered
			}
			if _, e := m.Cancel(r.KeyID, r.ID); e != nil {
				t.Fatal(e)
			}
			await(t, func() bool { return state(s, r) == Cancelled })
			m.Close()
			got, _ := s.Get(r.KeyID, r.ID)
			if len(got.Attempts) > 0 && !got.Attempts[0].Settled {
				t.Fatal("cancel lost settlement")
			}
			if _, e := m.Cancel(r.KeyID, r.ID); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestRecoveryExpiryAndRefusedSubmit(t *testing.T) {
	s := store(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	r := create(t, s, "k_a")
	_, _ = s.change(r.KeyID, r.ID, func(v *Run) error {
		v.State = Running
		v.Attempts = []Attempt{{ID: "a_1", Dispatched: true}}
		return nil
	})
	m := manager(t, s, nil, nil)
	got, _ := s.Get(r.KeyID, r.ID)
	if got.State != Failed || got.Reason != "interrupted" || !got.Attempts[0].AccountingUncertain {
		t.Fatal(got)
	}
	if _, e := m.Submit("k_a", "unregistered", "", json.RawMessage(`{}`)); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	now = now.Add(Retention + time.Second)
	if e := m.Sweep(); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Get(r.KeyID, r.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
func TestAbandonedWait(t *testing.T) {
	s := store(t)
	m := manager(t, s, nil, func(context.Context, Run) (Decision, error) { return Decision{Wait: "approval"}, nil })
	r := submit(t, m)
	await(t, func() bool { return state(s, r) == Waiting })
	s.mu.Lock()
	s.now = func() time.Time { return time.Now().Add(MaxAge + time.Hour) }
	s.mu.Unlock()
	if e := m.Sweep(); e != nil {
		t.Fatal(e)
	}
	if state(s, r) != Cancelled {
		t.Fatal(state(s, r))
	}
}
func TestWaitingEventIsImmediatelyResumable(t *testing.T) {
	s := store(t)
	var resume atomic.Bool
	m := manager(t, s, nil, func(context.Context, Run) (Decision, error) {
		if !resume.Load() {
			return Decision{Wait: "tool"}, nil
		}
		return Decision{Output: json.RawMessage(`{}`)}, nil
	})
	_, events, stop, e := s.Subscribe("k_a", "")
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	r := submit(t, m)
	for event := range events {
		if event.State == Waiting {
			break
		}
	}
	resume.Store(true)
	if e = m.Resume(r.KeyID, r.ID); e != nil {
		t.Fatalf("published waiting but Resume refused: %v", e)
	}
	await(t, func() bool { return state(s, r) == Done })
}
func TestOutputLimitAndStepFailurePreserveSettlement(t *testing.T) {
	for _, oversize := range []bool{false, true} {
		t.Run(fmt.Sprint(oversize), func(t *testing.T) {
			s := store(t)
			ex := func(_ context.Context, _ string, _ Step, acquired func() error) (StepResult, error) {
				if e := acquired(); e != nil {
					t.Fatal(e)
				}
				out := json.RawMessage(`{}`)
				var e error
				if oversize {
					out = json.RawMessage(`"` + strings.Repeat("x", MaxOutput) + `"`)
				} else {
					e = errors.New("engine failed")
				}
				return StepResult{Output: out, Settled: true, Dispatched: true, Usage: usage.Event{PromptTokens: 7}}, e
			}
			m := manager(t, s, ex, func(context.Context, Run) (Decision, error) {
				return Decision{Step: &Step{Input: json.RawMessage(`{}`)}}, nil
			})
			r := submit(t, m)
			await(t, func() bool { return state(s, r) == Failed })
			got, _ := s.Get(r.KeyID, r.ID)
			if !got.Attempts[0].Settled || got.Attempts[0].Usage.PromptTokens != 7 {
				t.Fatal("settlement dropped", got)
			}
			if oversize && len(got.Attempts[0].Output) != 0 {
				t.Fatal("oversized output retained")
			}
		})
	}
}
func TestCompletionAndCancelHaveOneTerminalWinner(t *testing.T) {
	for i := 0; i < 20; i++ {
		s := store(t)
		ready, release := make(chan struct{}), make(chan struct{})
		m := manager(t, s, nil, func(context.Context, Run) (Decision, error) {
			close(ready)
			<-release
			return Decision{Output: json.RawMessage(`{}`)}, nil
		})
		r := submit(t, m)
		<-ready
		done := make(chan struct{})
		go func() { defer close(done); _, _ = m.Cancel(r.KeyID, r.ID) }()
		close(release)
		<-done
		await(t, func() bool { return terminal(state(s, r)) })
		winner := state(s, r)
		m.Close()
		if winner != Done && winner != Cancelled {
			t.Fatal(winner)
		}
		if _, e := m.Cancel(r.KeyID, r.ID); e != nil {
			t.Fatal(e)
		}
		if state(s, r) != winner {
			t.Fatal("terminal state changed")
		}
	}
}
func TestOversizedStepInputRefusedBeforeExecutor(t *testing.T) {
	s := store(t)
	var called atomic.Bool
	m := manager(t, s, func(context.Context, string, Step, func() error) (StepResult, error) {
		called.Store(true)
		return StepResult{}, nil
	}, func(context.Context, Run) (Decision, error) {
		return Decision{Step: &Step{Input: json.RawMessage(`"` + strings.Repeat("x", MaxInput) + `"`)}}, nil
	})
	r := submit(t, m)
	await(t, func() bool { return state(s, r) == Failed })
	if called.Load() {
		t.Fatal("oversized step executed")
	}
}
