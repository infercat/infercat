package run

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// Manager serializes control operations; engine work runs outside its mutex and the store lock.
type Manager struct {
	Store    *Store
	Execute  Executor
	Kinds    map[string]Kind
	Policies map[string]Policy
	mu       sync.Mutex
	active   map[string]*execution
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

type execution struct {
	cancel context.CancelFunc
	answer chan struct{}
	kind   string
}

func New(s *Store, exec Executor, kinds map[string]Kind) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{Store: s, Execute: exec, Kinds: kinds, active: map[string]*execution{}, ctx: ctx, cancel: cancel}
	rows, _ := s.List("")
	for _, r := range rows {
		if !terminal(r.State) {
			_, err := s.change(r.KeyID, r.ID, func(v *Run) error {
				v.State = Failed
				v.Reason = "interrupted"
				for i := range v.Attempts {
					if !v.Attempts[i].Settled {
						v.Attempts[i].AccountingUncertain = true
					}
				}
				return nil
			})
			if err != nil {
				cancel()
				return nil, err
			}
		}
	}
	return m, nil
}
func (m *Manager) Submit(key, kind, priority string, input json.RawMessage) (Run, error) {
	rows, err := m.SubmitBatch(key, kind, priority, []json.RawMessage{input})
	if err != nil {
		return Run{}, err
	}
	return rows[0], nil
}
func (m *Manager) SubmitBatch(key, kind, priority string, inputs []json.RawMessage) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return nil, ErrConflict
	}
	if m.Kinds[kind] == nil {
		return nil, ErrInvalid
	}
	if priority == "" {
		priority = "interactive"
	}
	p := m.Policies[kind]
	cap := MaxLiveKey
	if p.QueueLimit != nil {
		var err error
		cap, err = p.QueueLimit(key)
		if err != nil {
			return nil, err
		}
	}
	for _, in := range inputs {
		if p.Validate != nil {
			if err := p.Validate(in); err != nil {
				return nil, err
			}
		}
	}
	rows, err := m.Store.CreateBatch(key, kind, priority, inputs, cap)
	if err == nil {
		if p.Serial {
			m.schedule(kind)
		} else {
			for _, r := range rows {
				m.start(r)
			}
		}
	}
	return rows, err
}
func (m *Manager) queued(kind string) []Run {
	summaries, _ := m.Store.List("")
	var rows []Run
	for _, r := range summaries {
		if r.Kind == kind && r.State == Queued {
			if v, err := m.Store.Get(r.KeyID, r.ID); err == nil {
				rows = append(rows, v)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Priority != b.Priority {
			return a.Priority == "interactive"
		}
		if a.Created.Equal(b.Created) {
			return a.ID < b.ID
		}
		return a.Created.Before(b.Created)
	})
	return rows
}
func (m *Manager) schedule(kind string) {
	if m.ctx.Err() != nil {
		return
	}
	for _, a := range m.active {
		if a.kind == kind {
			return
		}
	}
	if rows := m.queued(kind); len(rows) > 0 {
		m.start(rows[0])
	}
}
func (m *Manager) Position(kind, rid string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.queued(kind) {
		if r.ID == rid {
			return i + 1
		}
	}
	return 0
}
func (m *Manager) start(r Run) {
	ctx, cancel := context.WithCancel(m.ctx)
	worker := &execution{cancel: cancel, kind: r.Kind, answer: make(chan struct{}, 1)}
	m.active[r.ID] = worker
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			if m.active[r.ID] == worker {
				delete(m.active, r.ID)
				if m.Policies[r.Kind].Serial {
					m.schedule(r.Kind)
				}
			}
			m.mu.Unlock()
			cancel()
		}()
		m.drive(ctx, r)
	}()
}
func (m *Manager) Resume(key, rid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return ErrConflict
	}
	r, err := m.Store.change(key, rid, func(v *Run) error {
		if v.State != Waiting || m.active[rid] != nil {
			return ErrConflict
		}
		v.State = Queued
		v.Reason = ""
		return nil
	})
	if err == nil {
		m.start(r)
	}
	return err
}
func (m *Manager) Cancel(key, rid string) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.Store.Get(key, rid)
	if err != nil || terminal(old.State) {
		return old, err
	}
	r, err := m.Store.change(key, rid, func(v *Run) error {
		if terminal(v.State) {
			return ErrConflict
		}
		v.CancelRequested = true
		if m.active[rid] == nil || (v.State != Running && !m.Policies[v.Kind].JoinCancel) {
			v.State = Cancelled
		}
		return nil
	})
	if err == nil {
		if worker := m.active[rid]; worker != nil && !(r.State == Running && m.Policies[r.Kind].DeferredCancel) {
			worker.cancel()
		}
	}
	return r, err
}
func (m *Manager) finish(key, rid string, st State, reason string, output json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// A waiting event is a promise that Resume can start its next worker immediately.
	if st == Waiting {
		delete(m.active, rid)
	}
	_, _ = m.Store.change(key, rid, func(v *Run) error {
		if terminal(v.State) {
			return ErrConflict
		}
		if v.CancelRequested && !(m.Policies[v.Kind].DeferredCancel && st == Done) {
			st = Cancelled
		}
		v.State = st
		v.Reason = reason
		// Artifact metadata can change (Discard/eviction) between persistence and step finish.
		if _, stored := imageOutput(*v); !stored {
			v.Output = output
		}
		return nil
	})
}
func (m *Manager) drive(ctx context.Context, r Run) {
	defer func() {
		if recover() != nil {
			m.finish(r.KeyID, r.ID, Failed, "step panic", nil)
		}
	}()
	for ctx.Err() == nil {
		d, err := m.Kinds[r.Kind](ctx, r)
		if err != nil {
			m.finish(r.KeyID, r.ID, Failed, "kind failed", nil)
			return
		}
		if d.Step == nil {
			if len(d.Output) > MaxOutput || (len(d.Output) > 0 && !json.Valid(d.Output)) {
				m.finish(r.KeyID, r.ID, Failed, "output limit or format", nil)
				return
			}
			if d.Wait != "" {
				m.finish(r.KeyID, r.ID, Waiting, d.Wait, nil)
			} else {
				m.finish(r.KeyID, r.ID, Done, "", d.Output)
			}
			return
		}
		r, _, err = m.attempt(ctx, r, *d.Step, false)
		if err != nil || terminal(r.State) {
			return
		}
	}
	m.finish(r.KeyID, r.ID, Cancelled, "cancelled", nil)
}

// attempt is the single durable model-attempt path for pull kinds and live consumers.
func (m *Manager) attempt(ctx context.Context, r Run, step Step, live bool) (Run, StepResult, error) {
	if len(step.Input) > MaxInput || !json.Valid(step.Input) {
		if !live {
			m.finish(r.KeyID, r.ID, Failed, "step input limit or format", nil)
		}
		return r, StepResult{}, ErrInvalid
	}
	aid := id("a_")
	r, err := m.Store.change(r.KeyID, r.ID, func(v *Run) error {
		if (v.State != Queued && !(live && v.State == Running)) || v.CancelRequested {
			return ErrConflict
		}
		v.State = Queued
		v.Attempts = append(v.Attempts, Attempt{ID: aid, AccountingUncertain: true})
		return nil
	})
	if err != nil {
		return r, StepResult{}, err
	}
	result, stepErr := m.Execute(ctx, r.KeyID, step, func() error {
		_, e := m.Store.change(r.KeyID, r.ID, func(v *Run) error {
			if v.State != Queued || v.CancelRequested {
				return ErrConflict
			}
			v.State = Running
			now := m.Store.now().UTC()
			v.Started = &now
			return nil
		})
		return e
	})
	r, err = m.Store.change(r.KeyID, r.ID, func(v *Run) error {
		a := &v.Attempts[len(v.Attempts)-1]
		if a.ID != aid {
			return ErrConflict
		}
		a.Dispatched = result.Dispatched
		a.Settled = result.Settled
		a.AccountingUncertain = !result.Settled
		a.Usage = result.Usage
		if len(result.Output) <= MaxOutput && json.Valid(result.Output) {
			a.Output = result.Output
		}
		if live {
			v.State = Running
		} else if (v.CancelRequested && !m.Policies[v.Kind].DeferredCancel) || errors.Is(stepErr, context.Canceled) {
			v.State = Cancelled
		} else if stepErr != nil || len(result.Output) > MaxOutput || !json.Valid(result.Output) {
			v.State = Failed
			v.Reason = "step failed"
			if stepErr != nil {
				v.Reason = stepErr.Error()
			}
		} else if _, stored := imageOutput(*v); stored && m.Policies[v.Kind].DeferredCancel {
			v.State = Running // the completed image remains running until its terminal snapshot
		} else {
			v.State = Queued
		}
		return nil
	})
	if err == nil && (stepErr != nil || len(result.Output) > MaxOutput || !json.Valid(result.Output)) {
		err = ErrInvalid
	}
	return r, result, err
}

// Sweep cancels abandoned live work; terminal expiry deletes content without trimming live runs.
func (m *Manager) Sweep() error {
	rows, err := m.Store.List("")
	if err != nil {
		return err
	}
	now := m.Store.now()
	for _, r := range rows {
		if !terminal(r.State) && !r.Expires.After(now) {
			if _, err = m.Cancel(r.KeyID, r.ID); err != nil {
				return err
			}
		}
	}
	s := m.Store
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, v := range s.data {
		next := clone(v)
		changed := false
		for rid, r := range next.Runs {
			if terminal(r.State) && !r.Expires.After(now) {
				delete(next.Runs, rid)
				changed = true
			}
		}
		if changed {
			if err = s.commit(key, next, nil); err != nil {
				return err
			}
		}
		if err = s.sweepImages(key, next); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-t.C:
				_ = m.Sweep()
			}
		}
	}()
}
func (m *Manager) Close() { m.mu.Lock(); m.cancel(); m.mu.Unlock(); m.wg.Wait() }

// Done lets transports leave when host shutdown cancels the manager.
func (m *Manager) Done() <-chan struct{} { return m.ctx.Done() }
