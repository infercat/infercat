package run

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sort"
	"sync"
	"time"
)

// Manager serializes control operations; engine work runs outside its mutex and the store lock.
type Manager struct {
	Store       *Store
	Execute     Executor
	Kinds       map[string]Kind
	Policies    map[string]Policy
	mu          sync.Mutex
	active      map[string]*execution
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	started     bool
	sweeping    bool
	joinTimeout time.Duration
	stopTimeout time.Duration
	blocked     map[string]int
	quarantined map[string]bool
	releases    map[string]func()
	positions   map[string]map[string]int
}

type execution struct {
	cancel        context.CancelFunc
	answer        chan struct{}
	kind          string
	done          chan struct{}
	joined        chan struct{}
	join          sync.Once
	key           string
	expired       bool
	stopAbandoned bool
}

func New(s *Store, exec Executor, kinds map[string]Kind) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{Store: s, Execute: exec, Kinds: maps.Clone(kinds), joinTimeout: 30 * time.Second, stopTimeout: 10 * time.Second, blocked: map[string]int{}, active: map[string]*execution{}, ctx: ctx, cancel: cancel}
	s.mu.Lock()
	s.recovery = true
	for key := range s.data {
		_ = s.recoverKey(key)
	}
	s.mu.Unlock()
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
	if err := m.availability(kind); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		return nil, ErrConflict
	}
	if m.Kinds[kind] == nil {
		m.mu.Unlock()
		return nil, ErrInvalid
	}
	m.started = true
	p := m.Policies[kind]
	m.mu.Unlock()
	if priority == "" {
		priority = "interactive"
	}
	admission := BatchAdmission{QueueLimit: MaxLiveKey}
	var err error
	if p.QueueLimit != nil {
		admission.QueueLimit, err = p.QueueLimit(key)
	}
	if err == nil && p.Admission != nil {
		admission, err = p.Admission(m.ctx, key)
	}
	if err != nil {
		return nil, err
	}
	for _, in := range inputs {
		if p.Validate != nil {
			if err = p.Validate(in); err != nil {
				return nil, err
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.availability(kind); err != nil {
		return nil, err
	}
	if m.ctx.Err() != nil {
		return nil, ErrConflict
	}
	rows, err := m.Store.CreateBatch(key, kind, priority, inputs, admission.QueueLimit, admission.Reserve)
	if err != nil {
		return nil, err
	}
	if p.Release != nil {
		if m.releases == nil {
			m.releases = map[string]func(){}
		}
		for _, r := range rows {
			m.releases[r.ID] = func() { p.Release(key, r.ID) }
		}
	}
	if p.Serial {
		m.schedule(kind)
	} else {
		for _, r := range rows {
			m.start(r)
		}
	}
	return rows, nil
}
func (m *Manager) release(rid string) {
	if release := m.releases[rid]; release != nil {
		delete(m.releases, rid)
		release()
	}
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
	if m.ctx.Err() != nil || m.unavailable(kind) {
		return
	}
	rows := m.queued(kind)
	if m.positions == nil {
		m.positions = map[string]map[string]int{}
	}
	positions := map[string]int{}
	for i, r := range rows {
		positions[r.ID] = i + 1
	}
	m.positions[kind] = positions
	for _, active := range m.active {
		if active.kind == kind {
			return
		}
	}
	if len(rows) > 0 {
		m.start(rows[0])
	}
}
func (m *Manager) Position(kind, rid string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.positions[kind][rid]
}
func (m *Manager) acquiredPosition(kind, rid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	positions := m.positions[kind]
	rank := positions[rid]
	if rank == 0 {
		return
	}
	delete(positions, rid)
	for id, p := range positions {
		if p > rank {
			positions[id] = p - 1
		}
	}
}
func (m *Manager) start(r Run) {
	ctx, cancel := context.WithCancel(m.ctx)
	worker := &execution{cancel: cancel, kind: r.Kind, key: r.KeyID, done: make(chan struct{}), joined: make(chan struct{}), answer: make(chan struct{}, 1)}
	m.active[r.ID] = worker
	go func() {
		defer close(worker.done)
		defer func() {
			m.mu.Lock()
			if m.active[r.ID] == worker && !worker.expired {
				delete(m.active, r.ID)
				m.release(r.ID)
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
		if err := m.availability(v.Kind); err != nil {
			return err
		}
		if v.State != Waiting || m.active[rid] != nil {
			return ErrConflict
		}
		v.State = Queued
		v.Reason = ""
		return nil
	})
	if err == nil {
		if m.Policies[r.Kind].Serial {
			m.schedule(r.Kind)
		} else {
			m.start(r)
		}
	} else if committedTerminal(err) {
		m.stopTerminal(r)
	}
	return err
}
func (m *Manager) Cancel(key, rid string) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.Store.Get(key, rid)
	if err != nil {
		return old, err
	}
	if terminal(old.State) {
		m.stopTerminal(old)
		return old, nil
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
	if err == nil || committedTerminal(err) {
		if terminal(r.State) {
			m.stopTerminal(r)
			if m.Policies[r.Kind].Serial {
				m.schedule(r.Kind)
			}
			return r, err
		}
		if worker := m.active[rid]; worker != nil && !(r.State == Running && m.Policies[r.Kind].DeferredCancel) {
			worker.cancel()
			if m.Policies[r.Kind].JoinCancel {
				m.boundJoin(r, worker)
			}
		}
	}
	if err == nil && m.Policies[r.Kind].Serial {
		m.schedule(r.Kind)
	}
	return r, err
}
func (m *Manager) finish(key, rid string, st State, reason string, output json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w := m.active[rid]; w != nil && w.expired {
		st = Cancelled
		reason = "consumer did not join"
		output = nil
	}
	r, err := m.Store.change(key, rid, func(v *Run) error {
		if terminal(v.State) {
			return ErrConflict
		}
		if v.CancelRequested && !(m.Policies[v.Kind].DeferredCancel && st == Done) {
			st = Cancelled
		}
		v.State = st
		v.Reason = boundedReason(reason)
		// Artifact metadata can change (Discard/eviction) between persistence and step finish.
		if _, stored := imageOutput(*v); !stored {
			v.Output = output
		}
		return nil
	})
	if err == nil && st == Waiting {
		delete(m.active, rid)
	}
	if committedTerminal(err) {
		m.stopTerminal(r)
	}
	if err != nil && !errors.Is(err, ErrConflict) {
		m.Store.mu.Lock()
		m.Store.markBroken(key, err)
		m.Store.mu.Unlock()
	}
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
		original := r
		r, _, err = m.attempt(ctx, r, *d.Step, false)
		if err != nil {
			m.finish(original.KeyID, original.ID, Failed, "attempt failed", nil)
			return
		}
		if terminal(r.State) {
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
		if live {
			v.State = Running
		} else {
			v.State = Queued
		}
		v.Attempts = append(v.Attempts, Attempt{ID: aid, AccountingUncertain: true})
		return nil
	})
	if err != nil {
		return r, StepResult{}, err
	}
	result, stepErr := m.Execute(ctx, r.KeyID, step, func() error {
		_, e := m.Store.change(r.KeyID, r.ID, func(v *Run) error {
			if (v.State != Queued && !(live && v.State == Running)) || v.CancelRequested {
				return ErrConflict
			}
			v.State = Running
			now := m.Store.now().UTC()
			v.Started = &now
			return nil
		})
		if e == nil {
			m.acquiredPosition(r.Kind, r.ID)
		}
		return e
	})
	r, err = m.Store.change(r.KeyID, r.ID, func(v *Run) error {
		wasTerminal := terminal(v.State)
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
		if wasTerminal {
			return nil
		}
		if live {
			v.State = Running
		} else if v.CancelRequested && m.Policies[v.Kind].DeferredCancel {
			v.State = Cancelled
			if stepErr == nil && len(result.Output) > 0 && json.Valid(result.Output) {
				v.State = Done
				if _, stored := imageOutput(*v); !stored {
					v.Output = result.Output
				}
			}
		} else if v.CancelRequested || errors.Is(stepErr, context.Canceled) {
			v.State = Cancelled
		} else if stepErr != nil || len(result.Output) > MaxOutput || !json.Valid(result.Output) {
			v.State = Failed
			v.Reason = "step failed"
			if v.Kind == "image" && result.Usage.Code != "" {
				v.Reason = boundedReason(result.Usage.Code)
			}
		} else if _, stored := imageOutput(*v); stored && m.Policies[v.Kind].DeferredCancel {
			v.State = Running // the completed image remains running until its terminal snapshot
		} else {
			v.State = Queued
		}
		return nil
	})
	if err != nil {
		return r, result, &SettlementError{CallErr: stepErr, StoreErr: err}
	}
	if err == nil && (stepErr != nil || len(result.Output) > MaxOutput || !json.Valid(result.Output)) {
		err = ErrInvalid
	}
	return r, result, err
}

// Sweep cancels abandoned live work; terminal expiry deletes content without trimming live runs.
func (m *Manager) Sweep() error {
	s := m.Store
	s.mu.Lock()
	keys := make([]string, 0, len(s.known))
	for key := range s.known {
		keys = append(keys, key)
	}
	s.mu.Unlock()
	for _, key := range keys {
		s.mu.Lock()
		last := s.accessed[key]
		v, err := s.load(key)
		var rows []Run
		if err == nil {
			for _, r := range v.Runs {
				rows = append(rows, r)
			}
		}
		s.accessed[key] = last
		s.mu.Unlock()
		if err != nil {
			s.mu.Lock()
			s.markBroken(key, err)
			s.mu.Unlock()
			continue
		}
		for _, r := range rows {
			if !terminal(r.State) && !r.Expires.After(s.now()) {
				if _, cancelErr := m.Cancel(key, r.ID); cancelErr != nil && !committedTerminal(cancelErr) {
					s.log("run expiry cancel failed for %s/%s: %v", key, r.ID, cancelErr)
				}
			}
		}
		s.mu.Lock()
		if err == nil {
			last = s.accessed[key]
			v, loadErr := s.load(key)
			if loadErr != nil {
				s.markBroken(key, loadErr)
				s.mu.Unlock()
				continue
			}
			next := clone(v)
			var paths []string
			changed := false
			for rid, r := range next.Runs {
				if terminal(r.State) && !r.Expires.After(s.now()) {
					if _, ok := imageOutput(r); ok {
						paths = append(paths, s.artifactPath(key, rid))
					}
					delete(next.Runs, rid)
					changed = true
				}
			}
			if changed {
				err = s.commit(key, next, nil)
			}
			if err == nil {
				for _, path := range paths {
					s.unlinkImage(path)
				}
				err = s.sweepImages(key, next)
			}
		}
		if err != nil {
			s.markBroken(key, err)
		}
		s.accessed[key] = last
		s.releaseIdle()
		s.mu.Unlock()
	}
	return nil
}
func (m *Manager) Start() {
	m.mu.Lock()
	if m.sweeping {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.sweeping = true
	m.wg.Add(1)
	m.mu.Unlock()
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
func (m *Manager) Close() {
	m.mu.Lock()
	m.cancel()
	for id := range m.releases {
		m.release(id)
	}
	var workers []*execution
	for id, w := range m.active {
		workers = append(workers, w)
		m.boundJoin(Run{ID: id, KeyID: w.key}, w)
	}
	m.mu.Unlock()
	deadline := time.NewTimer(m.joinTimeout + m.stopTimeout)
	defer deadline.Stop()
	for _, w := range workers {
		select {
		case <-w.done:
		case <-w.joined:
		case <-deadline.C:
			return
		}
	}
	sweepDone := make(chan struct{})
	go func() { m.wg.Wait(); close(sweepDone) }()
	select {
	case <-sweepDone:
	case <-deadline.C:
	}

}

// Done lets transports leave when host shutdown cancels the manager.
func (m *Manager) Done() <-chan struct{} { return m.ctx.Done() }
