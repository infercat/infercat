package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Write a valid legacy snapshot that predates the reserved terminal headroom.
func ceilingFixture(t *testing.T, s *Store, r Run, headroom int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	v := clone(s.data[r.KeyID])
	f := v.Runs[r.ID]
	f.Input = json.RawMessage(`""`)
	v.Runs[r.ID] = f
	raw, _ := json.Marshal(v)
	f.Input = json.RawMessage(`"` + strings.Repeat("x", MaxStored-headroom-len(raw)) + `"`)
	v.Runs[r.ID] = f
	raw, _ = json.Marshal(v)
	if len(raw) != MaxStored-headroom {
		t.Fatal(len(raw))
	}
	if err := atomicWrite(filepath.Join(s.root, r.KeyID, "state.json"), raw); err != nil {
		t.Fatal(err)
	}
	s.data[r.KeyID] = v
}
func Test154DeferredCancelCannotDispatchAnotherStep(t *testing.T) {
	for _, output := range []json.RawMessage{json.RawMessage(`{"image":1}`), nil} {
		s := store(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		m := manager(t, s, func(ctx context.Context, _ string, _ Step, acquired func() error) (StepResult, error) {
			if e := acquired(); e != nil {
				return StepResult{Settled: true}, e
			}
			calls.Add(1)
			close(entered)
			<-release
			return StepResult{Output: output, Dispatched: true, Settled: true}, nil
		}, nil)
		m.Register("test", func(context.Context, Run) (Decision, error) {
			return Decision{Step: &Step{Input: json.RawMessage(`{}`)}}, nil
		}, Policy{DeferredCancel: true})
		r := submit(t, m)
		<-entered
		m.Cancel(r.KeyID, r.ID)
		close(release)
		await(t, func() bool { return terminal(state(s, r)) })
		got, _ := s.Get(r.KeyID, r.ID)
		want := Done
		if output == nil {
			want = Cancelled
		}
		if got.State != want || calls.Load() != 1 {
			t.Fatal(got.State, calls.Load())
		}
	}
}
func Test154TerminalBudgetFallbackAndLazyRecovery(t *testing.T) {
	s := store(t)
	filler := create(t, s, "k_a")
	r := create(t, s, "k_a")
	_, _ = s.change(filler.KeyID, filler.ID, func(v *Run) error { v.State = Done; return nil })
	ceilingFixture(t, s, filler, 151)
	m := &Manager{Store: s}
	m.finish(r.KeyID, r.ID, Done, "", json.RawMessage(`"`+strings.Repeat("z", 700<<10)+`"`))
	got, err := s.Get(r.KeyID, r.ID)
	if err != nil || got.State != Done || !strings.Contains(string(got.Output), "output not retained: budget") {
		t.Fatal(got.State, err)
	}
	reopened, err := NewStore(filepath.Dir(s.root))
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.data) != 0 {
		t.Fatal("eager snapshot load")
	}
	m2, err := New(reopened, nil, nil)
	if err != nil {
		t.Fatal("host boot refused", err)
	}
	defer m2.Close()
	got, err = reopened.Get(r.KeyID, r.ID)
	if err != nil || got.State != Done {
		t.Fatal(got.State, err)
	}
	// The near-full live record must also recover without refusing host boot.
	s2 := store(t)
	live := create(t, s2, "k_live")
	ceilingFixture(t, s2, live, 151)
	reopened, _ = NewStore(filepath.Dir(s2.root))
	m3, err := New(reopened, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m3.Close()
	got, err = reopened.Get(live.KeyID, live.ID)
	if err != nil || got.State != Failed || got.Reason != "interrupted" {
		t.Fatal(got.State, got.Reason, err)
	}
}
func Test154BrokenKeyDoesNotBlockBootSweepOrIdleRelease(t *testing.T) {
	s := store(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	bad, good := create(t, s, "k_bad"), create(t, s, "k_good")
	s.write = func(path string, raw []byte) error {
		if strings.Contains(path, "k_bad") {
			return errors.New("disk unavailable")
		}
		return atomicWrite(path, raw)
	}
	logs := 0
	s.Log = func(string, ...any) { logs++ }
	m, err := New(s, nil, nil)
	if err != nil {
		t.Fatal("boot refused", err)
	}
	defer m.Close()
	if _, err = s.Get(bad.KeyID, bad.ID); err == nil || logs == 0 {
		t.Fatal("broken key not reported")
	}
	if r, e := s.Get(good.KeyID, good.ID); e != nil || r.State != Failed {
		t.Fatal(r, e)
	}
	now = now.Add(Retention + time.Hour)
	if err = m.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(good.KeyID, good.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("healthy expiry blocked", err)
	}
	now = now.Add(idleRelease + time.Second)
	m.Sweep()
	if _, ok := s.data[good.KeyID]; ok {
		t.Fatal("inactive key retained in RAM")
	}
}
func Test154FailedAttemptStopsSerialAndReasonDoesNotLeak(t *testing.T) {
	s := store(t)
	var starts atomic.Int32
	m := manager(t, s, func(context.Context, string, Step, func() error) (StepResult, error) {
		t.Error("dispatched refused start")
		return StepResult{}, nil
	}, nil)
	m.Register("test", func(_ context.Context, r Run) (Decision, error) {
		starts.Add(1)
		s.mu.Lock()
		s.reserved = map[string]map[string]*reservation{r.KeyID: {r.ID: {bytes: MaxStored}}}
		s.mu.Unlock()
		return Decision{Step: &Step{Input: json.RawMessage(`{}`)}}, nil
	}, Policy{Serial: true})
	r := submit(t, m)
	await(t, func() bool { return terminal(state(s, r)) })
	time.Sleep(30 * time.Millisecond)
	if starts.Load() != 1 {
		t.Fatal("serial start loop", starts.Load())
	}
	s2 := store(t)
	m2 := manager(t, s2, func(ctx context.Context, _ string, _ Step, a func() error) (StepResult, error) {
		if err := a(); err != nil {
			return StepResult{}, err
		}
		return StepResult{Settled: true}, errors.New("provider response with SECRET")
	}, func(context.Context, Run) (Decision, error) {
		return Decision{Step: &Step{Input: json.RawMessage(`{}`)}}, nil
	})
	r = submit(t, m2)
	await(t, func() bool { return state(s2, r) == Failed })
	got, _ := s2.Get(r.KeyID, r.ID)
	if strings.Contains(got.Reason, "SECRET") {
		t.Fatal("provider detail leaked")
	}
}
func Test154SettlementErrorDistinguishesSuccessfulCall(t *testing.T) {
	s := store(t)
	m := manager(t, s, func(ctx context.Context, _ string, _ Step, a func() error) (StepResult, error) {
		if err := a(); err != nil {
			return StepResult{}, err
		}
		s.mu.Lock()
		s.write = func(string, []byte) error { return errors.New("disk unavailable") }
		s.mu.Unlock()
		return StepResult{Output: json.RawMessage(`{}`), Dispatched: true, Settled: true}, nil
	}, nil)
	observed := make(chan error, 1)
	m.Register("test", m.Consumer(func(_ context.Context, w *Work) (json.RawMessage, error) {
		_, e := w.Step(Step{Input: json.RawMessage(`{}`)})
		observed <- e
		return nil, e
	}), Policy{JoinCancel: true})
	submit(t, m)
	err := <-observed
	var recorded *SettlementError
	if !errors.As(err, &recorded) || recorded.CallErr != nil || recorded.StoreErr == nil {
		t.Fatal(err)
	}
}
func Test154JoinDeadlineAndRegistrationFreeze(t *testing.T) {
	s := store(t)
	kinds := map[string]Kind{"unused": func(context.Context, Run) (Decision, error) { return Decision{}, nil }}
	m, err := New(s, nil, kinds)
	if err != nil {
		t.Fatal(err)
	}
	m.joinTimeout = 30 * time.Millisecond
	delete(kinds, "unused")
	if m.Kinds["unused"] == nil {
		t.Fatal("caller map aliased")
	}
	release, entered := make(chan struct{}), make(chan struct{})
	var stops atomic.Int32
	m.Register("test", m.Consumer(func(context.Context, *Work) (json.RawMessage, error) { close(entered); <-release; return nil, nil }), Policy{JoinCancel: true, ForceStop: func() {
		if stops.Add(1) == 1 {
			close(release)
		}
	}})
	r := submit(t, m)
	<-entered
	if e := m.Register("late", nil, Policy{}); !errors.Is(e, ErrConflict) {
		t.Fatal("late registration accepted")
	}
	m.Cancel(r.KeyID, r.ID)
	start := time.Now()
	m.Close()
	if time.Since(start) > time.Second {
		t.Fatal("Close unbounded")
	}
	await(t, func() bool { return state(s, r) == Cancelled })
	got, _ := s.Get(r.KeyID, r.ID)
	if got.Reason != "consumer did not join" || stops.Load() != 1 {
		t.Fatal(got.Reason, stops.Load())
	}
}
func Test154CapturedMetadataAndOneCancelledNote(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_a")
	release, _ := s.Admit(r.KeyID, r.ID, 8192)
	defer release()
	for _, o := range []Captured{{Name: "../escape", MIME: "text/plain"}, {Name: "file", MIME: "bad\r\nheader"}, {Name: "..", MIME: "text/plain"}} {
		if e := s.Retain(r.KeyID, r.ID, nil, nil, map[string]Captured{"o": o}); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
	_, err := s.change(r.KeyID, r.ID, func(v *Run) error { v.CancelRequested = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Retain(r.KeyID, r.ID, json.RawMessage(`{"cancelled":true}`), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.Retain(r.KeyID, r.ID, json.RawMessage(`{"again":true}`), nil, nil); !errors.Is(err, ErrLimit) {
		t.Fatal("multiple terminal notes", err)
	}
	if _, err = os.Stat(filepath.Join(s.root, r.KeyID, "state.json")); err != nil {
		t.Fatal(err)
	}
}

func Test154CancelAcquisitionRaceUsesCommittedState(t *testing.T) {
	for range 40 {
		s := store(t)
		ready, release := make(chan struct{}), make(chan struct{})
		var killed atomic.Bool
		m, _ := New(s, func(ctx context.Context, _ string, _ Step, acquired func() error) (StepResult, error) {
			close(ready)
			<-release
			if err := acquired(); err != nil {
				return StepResult{Settled: true}, err
			}
			select {
			case <-ctx.Done():
				killed.Store(true)
			case <-time.After(time.Millisecond):
			}
			return StepResult{Settled: true, Output: json.RawMessage(`{}`)}, nil
		}, nil)
		m.Register("test", func(_ context.Context, r Run) (Decision, error) {
			if len(r.Attempts) == 0 {
				return Decision{Step: &Step{Input: json.RawMessage(`{}`)}}, nil
			}
			return Decision{Output: r.Attempts[0].Output}, nil
		}, Policy{DeferredCancel: true})
		r := submit(t, m)
		<-ready
		close(release)
		got, err := m.Cancel(r.KeyID, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		await(t, func() bool { return terminal(state(s, r)) })
		m.Close()
		if got.State == Running && killed.Load() {
			t.Fatal("cancel saw running but killed generating attempt")
		}
	}
}
