package run

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func exhaust157(t *testing.T, s *Store, filler Run, rid string) {
	t.Helper()
	ceilingFixture(t, s, filler, MaxLiveKey*terminalBound)
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.data[filler.KeyID]
	if v.ExceptionBytes == nil {
		v.ExceptionBytes = map[string]int{}
	}
	v.ExceptionBytes[rid] = terminalBound
	raw, _ := json.Marshal(v)
	if err := atomicWrite(filepath.Join(s.root, filler.KeyID, "state.json"), raw); err != nil {
		t.Fatal(err)
	}
	v.encodedBytes = len(raw)
}
func Test157V3CommittedTerminalStopsOwnedConsumer(t *testing.T) {
	for _, mode := range []string{"answer", "cancel", "already_terminal"} {
		t.Run(mode, func(t *testing.T) {
			s := store(t)
			m := manager(t, s, nil, nil)
			m.joinTimeout = 30 * time.Millisecond
			m.stopTimeout = 100 * time.Millisecond
			wedge := make(chan struct{})
			var calls, stops atomic.Int32
			if err := m.Register("test", m.Consumer(func(ctx context.Context, w *Work) (json.RawMessage, error) {
				if calls.Add(1) > 1 {
					return json.RawMessage(`{}`), nil
				}
				if mode == "answer" {
					_, err := w.Approval("ap_1", "may I?")
					return nil, err
				}
				<-wedge
				return nil, nil
			}), Policy{Serial: true, JoinCancel: true, ForceStop: func(string) error {
				if stops.Add(1) == 1 {
					close(wedge)
				}

				return nil
			}}); err != nil {
				t.Fatal(err)
			}
			defer func() {
				select {
				case <-wedge:
				default:
					close(wedge)
				}
			}()
			filler := create(t, s, "key")
			s.change("key", filler.ID, func(v *Run) error { v.State = Done; return nil })
			r, err := m.Submit("key", "test", "", json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			want := Queued
			if mode == "answer" {
				want = Waiting
			}
			await(t, func() bool { return calls.Load() == 1 && state(s, r) == want })
			exhaust157(t, s, filler, r.ID)
			var got Run
			if mode == "answer" {
				got, err = m.Answer("key", r.ID, "ap_1", true)
			} else {
				if mode == "already_terminal" {
					_, err = s.change("key", r.ID, func(v *Run) error { v.State = Failed; v.Reason = "fixture failure"; return nil })
					if err != nil && !committedTerminal(err) {
						t.Fatal(err)
					}
				}
				got, err = m.Cancel("key", r.ID)
			}
			if mode != "already_terminal" {
				var ended *CommittedTerminal
				if !errors.As(err, &ended) || errors.Is(err, ErrLimit) || ended.Run.ID != r.ID || ended.Run.State != Failed {
					t.Fatal("ambiguous outcome", got.State, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			awaitFor(t, 10*time.Second, "", func() bool {
				m.mu.Lock()
				defer m.mu.Unlock()
				return m.active[r.ID] == nil
			})
			m.mu.Lock()
			active := m.active[r.ID] != nil
			m.mu.Unlock()
			if active {
				t.Fatal("terminal row stranded worker")
			}
			if mode != "answer" && stops.Load() != 1 {
				t.Fatal("force-stop not armed", stops.Load())
			}
			next, err := m.Submit("other", "test", "", json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			// CI's race-instrumented ceiling snapshot can hold the store lock longer
			// than the small-fixture helper's three-second budget. Keep all owner/
			// force-stop assertions above; allow the follow-on disk work to finish.
			awaitFor(t, 30*time.Second, "", func() bool { return state(s, next) == Done })
			finished, err := s.Get(next.KeyID, next.ID)
			if err != nil || finished.State != Done {
				t.Fatalf("follow-on serial run did not finish: state=%s reason=%q calls=%d stops=%d err=%v", finished.State, finished.Reason, calls.Load(), stops.Load(), err)
			}
		})
	}
}
func Test157V3FailedWriteReturnsNoCommittedClone(t *testing.T) {
	s := store(t)
	r := create(t, s, "key")
	s.write = func(string, []byte) error { return ErrLimit }
	got, err := s.change("key", r.ID, func(v *Run) error { v.State = Failed; return nil })
	if !errors.Is(err, ErrLimit) || committedTerminal(err) || got.ID != "" {
		t.Fatal("speculative state escaped", got, err)
	}
	stored, _ := s.Get("key", r.ID)
	if stored.State != Queued {
		t.Fatal(stored.State)
	}
}
func Test157V3WaitingQuarantineAndStopping(t *testing.T) {
	s := store(t)
	m := manager(t, s, nil, nil)
	if err := m.Register("bad", nil, Policy{JoinCancel: true, ForceStop: func(string) error {
		return nil
	}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("nonserial join accepted", err)
	}
	a, b := create(t, s, "key"), create(t, s, "key")
	s.change("key", a.ID, func(v *Run) error { v.State = Waiting; return nil })
	var releases atomic.Int32
	m.releases = map[string]func(){a.ID: func() { releases.Add(1) }, b.ID: func() { releases.Add(1) }}
	_, ch, stop, err := s.Subscribe("key", "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	m.mu.Lock()
	m.blocked["test"] = 1
	m.mu.Unlock()
	if _, err = m.Submit("key", "test", "", json.RawMessage(`{}`)); !errors.Is(err, ErrStopping) {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.blocked["test"] = 0
	m.quarantine("test", "fixture")
	m.mu.Unlock()
	for _, r := range []Run{a, b} {
		got, _ := s.Get("key", r.ID)
		if got.State != Failed || got.Reason != "runtime quarantined" {
			t.Fatal(got)
		}
	}
	if releases.Load() != 2 || len(ch) != 2 {
		t.Fatal("release or events missing", releases.Load(), len(ch))
	}
	if _, err = m.Submit("key", "test", "", json.RawMessage(`{}`)); !errors.Is(err, ErrQuarantined) {
		t.Fatal(err)
	}
}

func Test157V3StopWaiterEndsBeforeHookReturns(t *testing.T) {
	s := store(t)
	m := manager(t, s, nil, nil)
	m.joinTimeout = 15 * time.Millisecond
	m.stopTimeout = 30 * time.Millisecond
	entered, hook, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	if err := m.Register("test", m.Consumer(func(context.Context, *Work) (json.RawMessage, error) { close(entered); <-release; return nil, nil }), Policy{Serial: true, JoinCancel: true, ForceStop: func(string) error {
		close(hook)
		<-release
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	r := submit(t, m)
	<-entered
	m.mu.Lock()
	w := m.active[r.ID]
	m.mu.Unlock()
	if _, err := m.Cancel(r.KeyID, r.ID); err != nil {
		t.Fatal(err)
	}
	<-hook
	select {
	case <-w.joined:
	case <-time.After(time.Second):
		t.Fatal("manager waiter still held by hook")
	}
	m.mu.Lock()
	blocked := m.quarantined["test"]
	active := m.active[r.ID] != nil
	m.mu.Unlock()
	if !blocked || active {
		t.Fatal("stop timeout did not quarantine/release", blocked, active)
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close waited for noncooperative hook")
	}
}

func Test157V3AnswerAlreadyDeadReturnsCommittedSnapshot(t *testing.T) {
	s := store(t)
	m := manager(t, s, nil, nil)
	r := create(t, s, "key")
	for _, state := range []State{Failed, Cancelled, Done} {
		_, err := s.change(r.KeyID, r.ID, func(v *Run) error { v.State = state; v.Reason = "storage exhausted"; return nil })
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.Answer(r.KeyID, r.ID, "old_approval", true)
		var ended *CommittedTerminal
		if !errors.As(err, &ended) || got.ID != r.ID || ended.Run.State != state {
			t.Fatal(got, err)
		}
	}
}
