package run

import "time"

func (m *Manager) boundJoin(r Run, w *execution) {
	w.join.Do(func() {
		go func() {
			timer := time.NewTimer(m.joinTimeout)
			defer timer.Stop()
			select {
			case <-w.done:
				return
			case <-timer.C:
			}
			w.cancel()
			m.mu.Lock()
			w.expired = true
			m.mu.Unlock()
			if stop := m.Policies[w.kind].ForceStop; stop != nil {
				go stop()
			}
			m.finish(r.KeyID, r.ID, Cancelled, "consumer did not join", nil)
			m.mu.Lock()
			if m.active[r.ID] == w {
				delete(m.active, r.ID)
				if m.Policies[w.kind].Serial {
					m.schedule(w.kind)
				}
			}
			m.mu.Unlock()
		}()
	})
}
