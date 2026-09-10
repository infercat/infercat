package run

import "time"

func (m *Manager) availability(kind string) error {
	if m.quarantined[kind] {
		return ErrQuarantined
	}
	if m.blocked[kind] > 0 {
		return ErrStopping
	}
	return nil
}
func (m *Manager) unavailable(kind string) bool { return m.availability(kind) != nil }

// Called under m.mu, even if the row was already terminal before this request.
func (m *Manager) stopTerminal(r Run) {
	if !terminal(r.State) {
		return
	}
	if w := m.active[r.ID]; w != nil {
		w.cancel()
		if m.Policies[r.Kind].JoinCancel {
			m.boundJoin(r, w)
		}
	} else {
		m.release(r.ID)
	}
}
func (m *Manager) resumeKind(kind string) {
	if m.unavailable(kind) || m.ctx.Err() != nil {
		return
	}
	if m.Policies[kind].Serial {
		m.schedule(kind)
		return
	}
	for _, r := range m.queued(kind) {
		if m.active[r.ID] == nil {
			m.start(r)
		}
	}
}

// Called under m.mu. Accepted queued work settles before any new admission.
func (m *Manager) quarantine(kind, cause string) {
	m.Store.log("run kind %s force-stop %s", kind, cause)
	if m.ctx.Err() != nil {
		return
	}
	if m.quarantined == nil {
		m.quarantined = map[string]bool{}
	}
	m.quarantined[kind] = true
	rows, _ := m.Store.List("")
	for _, r := range rows {
		if r.Kind != kind || (r.State != Queued && r.State != Waiting) || m.active[r.ID] != nil {
			continue
		}
		_, err := m.Store.change(r.KeyID, r.ID, func(v *Run) error {
			v.State, v.Reason = Failed, "runtime quarantined"
			return nil
		})
		if err != nil {
			m.Store.log("quarantine settlement failed for %s: %v", r.ID, err)
		}
		m.release(r.ID)
	}
}
func (m *Manager) recoveredKind(kind string) {
	if m.quarantined[kind] {
		delete(m.quarantined, kind)
		m.Store.log("run kind %s quarantine cleared after successful stop", kind)
	}
	m.resumeKind(kind)
}
func (m *Manager) boundJoin(r Run, w *execution) {
	w.join.Do(func() {
		go func() {
			defer close(w.joined)
			timer := time.NewTimer(m.joinTimeout)
			defer timer.Stop()
			select {
			case <-w.done:
				return
			case <-timer.C:
			}
			w.cancel()
			m.mu.Lock()
			if m.active[r.ID] != w {
				m.mu.Unlock()
				return
			}
			w.expired = true
			m.blocked[w.kind]++
			stop := m.Policies[w.kind].ForceStop
			m.mu.Unlock()
			stopped := make(chan string, 1)
			go func() {
				result := "panic"
				defer func() {
					_ = recover()
					m.mu.Lock()
					defer m.mu.Unlock()
					if w.stopAbandoned {
						if result == "" {
							m.recoveredKind(w.kind)
						}
					} else {
						stopped <- result
					}
				}()
				if stop != nil {
					if err := stop(r.ID); err != nil {
						result = "error"
						return
					}
				} else {
					m.Store.log("run %s join deadline: no force-stop hook", r.ID)
				}
				result = ""
			}()
			deadline := time.NewTimer(m.stopTimeout)
			defer deadline.Stop()
			cause, timedOut := "", false
			select {
			case cause = <-stopped:
			case <-deadline.C:
				cause, timedOut = "timeout", true
			}
			m.finish(r.KeyID, r.ID, Cancelled, "consumer did not join", nil)
			m.mu.Lock()
			if timedOut {
				select {
				case cause = <-stopped:
					timedOut = false
				default:
					w.stopAbandoned = true
				}
			}
			if m.active[r.ID] == w {
				delete(m.active, r.ID)
				m.release(r.ID)
			}
			m.blocked[w.kind]--
			if cause == "" {
				m.recoveredKind(w.kind)
			} else {
				m.quarantine(w.kind, cause)
			}
			m.mu.Unlock()
		}()
	})
}
