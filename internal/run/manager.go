package run

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Manager serializes control operations; engine work runs outside its mutex and the store lock.
type Manager struct {
	Store   *Store
	Execute Executor
	Kinds   map[string]Kind
	mu      sync.Mutex
	active  map[string]context.CancelFunc
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func New(s *Store, exec Executor, kinds map[string]Kind) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{Store: s, Execute: exec, Kinds: kinds, active: map[string]context.CancelFunc{}, ctx: ctx, cancel: cancel}
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return Run{}, ErrConflict
	}
	if m.Kinds[kind] == nil {
		return Run{}, ErrInvalid
	}
	if priority == "" {
		priority = "interactive"
	}
	r, err := m.Store.Create(key, kind, priority, input)
	if err == nil {
		m.start(r)
	}
	return r, err
}
func (m *Manager) start(r Run) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.active[r.ID] = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() { m.mu.Lock(); delete(m.active, r.ID); m.mu.Unlock(); cancel() }()
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
		if m.active[rid] == nil || v.State != Running {
			v.State = Cancelled
		}
		return nil
	})
	if err == nil {
		if cancel := m.active[rid]; cancel != nil {
			cancel()
		}
	}
	return r, err
}
func (m *Manager) finish(key, rid string, st State, reason string, output json.RawMessage) {
	_, _ = m.Store.change(key, rid, func(v *Run) error {
		if terminal(v.State) {
			return ErrConflict
		}
		if v.CancelRequested {
			st = Cancelled
		}
		v.State = st
		v.Reason = reason
		v.Output = output
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
		aid := id("a_")
		r, err = m.Store.change(r.KeyID, r.ID, func(v *Run) error {
			if v.State != Queued || v.CancelRequested {
				return ErrConflict
			}
			v.Attempts = append(v.Attempts, Attempt{ID: aid, AccountingUncertain: true})
			return nil
		})
		if err != nil {
			return
		}
		result, stepErr := m.Execute(ctx, r.KeyID, *d.Step, func() error {
			_, e := m.Store.change(r.KeyID, r.ID, func(v *Run) error {
				if v.State != Queued || v.CancelRequested {
					return ErrConflict
				}
				v.State = Running
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
			if v.CancelRequested || errors.Is(stepErr, context.Canceled) {
				v.State = Cancelled
			} else if stepErr != nil || len(result.Output) > MaxOutput || !json.Valid(result.Output) {
				v.State = Failed
				v.Reason = "step failed"
			} else {
				v.State = Queued
			}
			return nil
		})
		if err != nil || terminal(r.State) {
			return
		}
	}
	m.finish(r.KeyID, r.ID, Cancelled, "cancelled", nil)
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
