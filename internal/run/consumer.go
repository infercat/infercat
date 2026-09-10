package run

import (
	"context"
	"encoding/json"
	"sync"
)

// Policy is registered by the host alongside a kind, never supplied by a friend.
// BatchAdmission is prepared without the manager lock. Reserve runs under the store
// admission lock, does no disk I/O, and returns rollback for a refused commit.
type BatchAdmission struct {
	QueueLimit int
	Reserve    func([]Run) (func(), error)
}

type Policy struct {
	Admission func(context.Context, string) (BatchAdmission, error)
	Release   func(string, string) // Idempotently release any unspent per-job reservation.

	ForceStop      func(string) // Must end the owned runtime generation; installed before Start.
	Serial         bool
	QueueLimit     func(string) (int, error) // Resolved outside the manager mutex.
	Validate       func(json.RawMessage) error
	DeferredCancel bool // A running attempt finishes; successful completion remains Done.
	JoinCancel     bool // An active consumer must return before cancellation becomes terminal.
}

// Register is startup wiring, before submitting any work.
func (m *Manager) Register(name string, kind Kind, policy Policy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if policy.JoinCancel && policy.ForceStop == nil {
		return ErrInvalid
	}
	if m.started || m.ctx.Err() != nil {
		return ErrConflict
	}
	if m.Kinds == nil {
		m.Kinds = map[string]Kind{}
	}
	if m.Policies == nil {
		m.Policies = map[string]Policy{}
	}
	m.Kinds[name], m.Policies[name] = kind, policy
	return nil
}

// Consumer adapts a long-lived host consumer to the same registry as pull Kinds.
// Register with JoinCancel. The callback returns only after joining its child work;
// on runtime loss it returns an error, never recreates or replays the native run.
func (m *Manager) Consumer(fn func(context.Context, *Work) (json.RawMessage, error)) Kind {
	return func(ctx context.Context, r Run) (Decision, error) {
		output, err := fn(ctx, &Work{Run: r, Store: m.Store, manager: m, ctx: ctx})
		return Decision{Output: output}, err
	}
}

type Work struct {
	Run     Run
	Store   *Store
	manager *Manager
	ctx     context.Context
	mu      sync.Mutex
}

// Step serializes helper/model calls within one run and persists each attempt
// before dispatch and its accounting before returning. Capacity is reserved first.
func (w *Work) Step(step Step) (StepResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ctx.Err(); err != nil {
		return StepResult{}, err
	}
	release, err := w.Store.Admit(w.Run.KeyID, w.Run.ID, 2*MaxOutput+4096)
	if err != nil {
		return StepResult{}, err
	}
	defer release()
	_, result, err := w.manager.attempt(w.ctx, w.Run, step, true)
	return result, err
}

// Approval returns only the committed answer. The consumer may then forward it
// once over IPC; a send failure ends the run rather than resending the answer.
func (w *Work) Approval(id, request string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !safeID.MatchString(id) || len(request) > MaxOutput {
		return false, ErrInvalid
	}
	release, err := w.Store.Admit(w.Run.KeyID, w.Run.ID, MaxOutput+4096)
	if err != nil {
		return false, err
	}
	defer release()
	_, err = w.Store.change(w.Run.KeyID, w.Run.ID, func(r *Run) error {
		if terminal(r.State) || r.CancelRequested {
			return ErrConflict
		}
		r.State = Waiting
		return nil
	}, func(data *Retained) error {
		if data.Approval != nil && (data.Approval.Status == "pending" || data.Approval.ID == id) {
			return ErrConflict
		}
		data.Approval = &Approval{ID: id, Request: request, Status: "pending"}
		return nil
	})
	if err != nil {
		return false, err
	}
	w.manager.mu.Lock()
	worker := w.manager.active[w.Run.ID]
	w.manager.mu.Unlock()
	if worker == nil {
		return false, ErrConflict
	}
	for {
		if err := w.ctx.Err(); err != nil {
			return false, err
		}
		data, err := w.Store.Retained(w.Run.KeyID, w.Run.ID)
		if err != nil {
			return false, err
		}
		if data.Approval.Status == "answered" {
			return *data.Approval.Allow, nil
		}
		select {
		case <-w.ctx.Done():
			return false, w.ctx.Err()
		case <-worker.answer:
		}
	}
}

func (m *Manager) Answer(key, rid, approvalID string, allow bool) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.Store.Get(key, rid); err != nil {
		return Run{}, err
	}
	worker := m.active[rid]
	if worker == nil {
		return Run{}, ErrConflict
	}
	r, err := m.Store.change(key, rid, func(r *Run) error {
		if r.State != Waiting || r.CancelRequested {
			return ErrConflict
		}
		r.State = Running
		return nil
	}, func(data *Retained) error {
		if data.Approval == nil || data.Approval.ID != approvalID || data.Approval.Status != "pending" {
			return ErrConflict
		}
		data.Approval.Status, data.Approval.Allow = "answered", &allow
		return nil
	})
	if err == nil {
		select {
		case worker.answer <- struct{}{}:
		default:
		}
	}
	return r, err
}
