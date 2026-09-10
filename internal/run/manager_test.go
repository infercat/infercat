package run

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func await(t *testing.T, fn func() bool) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition timed out")
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
				return Decision{Step: &Step{}}, nil
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
