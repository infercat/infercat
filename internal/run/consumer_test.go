package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/infercat/infercat/internal/usage"
)

func TestConsumerCancelWaitsForCleanupInEveryLiveState(t *testing.T) {
	for _, phase := range []State{Queued, Running, Waiting} {
		t.Run(string(phase), func(t *testing.T) {
			s := store(t)
			entered, cleanup, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			m := manager(t, s, func(ctx context.Context, _ string, _ Step, acquired func() error) (StepResult, error) {
				if err := acquired(); err != nil {
					return StepResult{Settled: true}, err
				}
				close(entered)
				<-ctx.Done()
				return StepResult{Dispatched: true, Settled: true}, ctx.Err()
			}, nil)
			m.Register("test", m.Consumer(func(ctx context.Context, w *Work) (json.RawMessage, error) {
				var err error
				switch phase {
				case Queued:
					close(entered)
					<-ctx.Done()
					err = ctx.Err()
				case Running:
					_, err = w.Step(Step{Input: json.RawMessage(`{}`)})
				case Waiting:
					_, err = w.Approval("p_1", "Allow this action?")
				}
				close(cleanup)
				<-release
				return nil, err
			}), Policy{JoinCancel: true, ForceStop: func(string) {}})
			r := submit(t, m)
			if phase == Waiting {
				await(t, func() bool { return state(s, r) == Waiting })
			} else {
				<-entered
			}
			got, err := m.Cancel(r.KeyID, r.ID)
			if err != nil || !got.CancelRequested || terminal(got.State) {
				t.Fatal("cancel acknowledged before join", got, err)
			}
			<-cleanup
			if terminal(state(s, r)) {
				t.Fatal("terminal before child cleanup")
			}
			if phase == Waiting {
				if _, err = m.Answer(r.KeyID, r.ID, "p_1", true); !errors.Is(err, ErrConflict) {
					t.Fatal("answer after cancel", err)
				}
			}
			close(release)
			await(t, func() bool { return state(s, r) == Cancelled })
			got, _ = s.Get(r.KeyID, r.ID)
			if phase == Running && (len(got.Attempts) != 1 || !got.Attempts[0].Settled) {
				t.Fatal("lost settlement", got)
			}
		})
	}
}

func TestConsumerApprovalCommitsBeforeDeliveryAndNeverReplays(t *testing.T) {
	s := store(t)
	var deliveries atomic.Int32
	m := manager(t, s, nil, nil)
	m.Register("test", m.Consumer(func(ctx context.Context, w *Work) (json.RawMessage, error) {
		allow, err := w.Approval("p_1", "Allow once?")
		if err != nil {
			return nil, err
		}
		// This is the IPC send boundary: the same answer must already be durable.
		reopened, e := NewStore(filepath.Dir(s.root))
		if e != nil {
			return nil, e
		}
		data, e := reopened.Retained(w.Run.KeyID, w.Run.ID)
		if e != nil || data.Approval.Status != "answered" || data.Approval.Allow == nil || *data.Approval.Allow != allow {
			t.Error("answer reached IPC before commit", data, e)
		}
		deliveries.Add(1)
		return json.RawMessage(`{"answer":"done"}`), nil
	}), Policy{JoinCancel: true, ForceStop: func(string) {}})
	r := submit(t, m)
	await(t, func() bool { return state(s, r) == Waiting })
	if _, err := m.Answer("k_other", r.ID, "p_1", true); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-key answer", err)
	}
	if _, err := m.Answer(r.KeyID, r.ID, "stale", true); !errors.Is(err, ErrConflict) {
		t.Fatal("stale answer", err)
	}
	if _, err := m.Answer(r.KeyID, r.ID, "p_1", false); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return state(s, r) == Done })
	if _, err := m.Answer(r.KeyID, r.ID, "p_1", false); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate answer", err)
	}
	if deliveries.Load() != 1 {
		t.Fatal("answer replayed", deliveries.Load())
	}
}

func TestConsumerFailedApprovalCommitNeverDelivers(t *testing.T) {
	s := store(t)
	var delivered atomic.Bool
	m := manager(t, s, nil, nil)
	m.Register("test", m.Consumer(func(ctx context.Context, w *Work) (json.RawMessage, error) {
		_, e := w.Approval("p_1", "Allow?")
		if e == nil {
			delivered.Store(true)
		}
		return nil, e
	}), Policy{JoinCancel: true, ForceStop: func(string) {}})
	r := submit(t, m)
	await(t, func() bool { return state(s, r) == Waiting })
	s.mu.Lock()
	s.write = func(string, []byte) error { return errors.New("disk unavailable") }
	s.mu.Unlock()
	if _, err := m.Answer(r.KeyID, r.ID, "p_1", true); err == nil {
		t.Fatal("failed write accepted")
	}
	m.Close()
	if delivered.Load() {
		t.Fatal("uncommitted approval delivered")
	}
	raw, _ := os.ReadFile(filepath.Join(s.root, r.KeyID, "state.json"))
	var snapshot snapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Retained[r.ID].Approval.Status != "pending" {
		t.Fatal("failed write altered approval")
	}
}

func TestConsumerUsesSharedAttemptLedgerForEveryModelCall(t *testing.T) {
	s := store(t)
	var calls atomic.Int32
	m := manager(t, s, func(ctx context.Context, key string, _ Step, acquired func() error) (StepResult, error) {
		rows, _ := s.List(key)
		r, _ := s.Get(key, rows[0].ID)
		a := r.Attempts[len(r.Attempts)-1]
		if a.Settled || !a.AccountingUncertain {
			t.Error("attempt not persisted before dispatch")
		}
		if err := acquired(); err != nil {
			return StepResult{Settled: true}, err
		}
		calls.Add(1)
		return StepResult{Output: json.RawMessage(`{"ok":true}`), Usage: usage.Event{KeyID: key, PromptTokens: 2, CompletionTokens: 3}, Dispatched: true, Settled: true}, nil
	}, nil)
	m.Register("test", m.Consumer(func(ctx context.Context, w *Work) (json.RawMessage, error) {
		for range 2 {
			if _, err := w.Step(Step{Input: json.RawMessage(`{}`)}); err != nil {
				return nil, err
			}
		}
		return json.RawMessage(`{"done":true}`), nil
	}), Policy{JoinCancel: true, ForceStop: func(string) {}})
	r := submit(t, m)
	await(t, func() bool { return state(s, r) == Done })
	got, _ := s.Get(r.KeyID, r.ID)
	if calls.Load() != 2 || len(got.Attempts) != 2 {
		t.Fatal("missing calls", got)
	}
	for _, a := range got.Attempts {
		if !a.Settled || a.AccountingUncertain || a.Usage.CompletionTokens != 3 {
			t.Fatal("lost usage", a)
		}
	}
}

func TestDeferredImagePolicyFinishesRunningAttempt(t *testing.T) {
	s := store(t)
	entered, release := make(chan struct{}), make(chan struct{})
	m := manager(t, s, func(ctx context.Context, _ string, _ Step, acquired func() error) (StepResult, error) {
		if err := acquired(); err != nil {
			return StepResult{Settled: true}, err
		}
		close(entered)
		<-release
		if ctx.Err() != nil {
			t.Error("running image interrupted")
		}
		return StepResult{Output: json.RawMessage(`{"image":1}`), Dispatched: true, Settled: true}, nil
	}, nil)
	m.Register("test", func(ctx context.Context, r Run) (Decision, error) {
		if len(r.Attempts) == 0 {
			return Decision{Step: &Step{Input: json.RawMessage(`{}`)}}, nil
		}
		return Decision{Output: r.Attempts[0].Output}, nil
	}, Policy{DeferredCancel: true})
	r := submit(t, m)
	<-entered
	got, err := m.Cancel(r.KeyID, r.ID)
	if err != nil || got.State != Running {
		t.Fatal(got, err)
	}
	close(release)
	await(t, func() bool { return state(s, r) == Done })
}
