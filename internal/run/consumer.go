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
	InProcess bool // Cooperative consumer: no shared external generation to stop.
	Admission func(context.Context, string) (BatchAdmission, error)
	Release   func(string, string) // Idempotently release any unspent per-job reservation.

	ForceStop      func(string) error // Must end the owned runtime generation; installed before Start.
	Serial         bool
	QueueLimit     func(string) (int, error) // Resolved outside the manager mutex.
	Validate       func(json.RawMessage) error
	DeferredCancel bool // A running attempt finishes; successful completion remains Done.
	JoinCancel     bool // Wait for the consumer up to the join bound; late in-process settlement remains owned.
}

// Register is startup wiring, before submitting any work.
func (m *Manager) Register(name string, kind Kind, policy Policy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if policy.InProcess && (!policy.JoinCancel || policy.Serial || policy.ForceStop != nil) {
		return ErrInvalid
	}
	if !policy.InProcess && policy.JoinCancel && (policy.ForceStop == nil || !policy.Serial) {
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
		w := &Work{Run: r, Store: m.Store, manager: m, ctx: ctx}
		defer func() {
			if w.finalRelease != nil {
				w.finalRelease()
			}
		}()
		output, err := fn(ctx, w)
		return Decision{Output: output}, err
	}
}

type Work struct {
	Run          Run
	Store        *Store
	manager      *Manager
	ctx          context.Context
	mu           sync.Mutex
	finalRelease func()
}

// Abort stops owned work on failure without claiming a user cancellation.
func (w *Work) Abort() {
	w.manager.mu.Lock()
	defer w.manager.mu.Unlock()
	if active := w.manager.active[w.Run.ID]; active != nil {
		active.cancel()
		w.manager.boundJoin(w.Run, active)
	}
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
	defer w.releaseAfter(release)
	r, result, err := w.manager.attempt(w.ctx, w.Run, step, true)
	if committedTerminal(err) {
		w.manager.mu.Lock()
		w.manager.stopTerminal(r)
		w.manager.mu.Unlock()
	}
	return result, err
}

// Keep the existing settlement lease through cancellation; never admit fresh work.
func (w *Work) releaseAfter(release func()) {
	if w.ctx.Err() != nil {
		w.finalRelease = release
	} else {
		release()
	}
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
	defer w.releaseAfter(release)
	r, err := w.Store.change(w.Run.KeyID, w.Run.ID, func(r *Run) error {
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
		if committedTerminal(err) {
			w.manager.mu.Lock()
			w.manager.stopTerminal(r)
			w.manager.mu.Unlock()
		}
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
	old, err := m.Store.Get(key, rid)
	if err != nil {
		return Run{}, err
	}
	if terminal(old.State) {
		m.stopTerminal(old)
		return old, &CommittedTerminal{Run: old}
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
	if committedTerminal(err) {
		m.stopTerminal(r)
	}
	if err == nil {
		select {
		case worker.answer <- struct{}{}:
		default:
		}
	}
	return r, err
}
