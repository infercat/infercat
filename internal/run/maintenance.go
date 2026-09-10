package run

import (
	"encoding/json"
	"time"
)

const terminalBound = 4096
const idleRelease = 5 * time.Minute

func (s *Store) markBroken(key string, err error) {
	first := s.broken[key] == nil
	s.broken[key] = err
	if first && s.Log != nil {
		s.Log("runs for key %s unavailable: %v", key, err)
	}
}
func (s *Store) recoverKey(key string) error {
	s.recovered[key] = true
	v := s.data[key]
	for id, r := range v.Runs {
		if terminal(r.State) {
			continue
		}
		next := clone(s.data[key])
		r.State = Failed
		r.Reason = "interrupted"
		r.Updated = s.now().UTC()
		r.Expires = r.Updated.Add(Retention)
		for i := range r.Attempts {
			if !r.Attempts[i].Settled {
				r.Attempts[i].AccountingUncertain = true
			}
		}
		next.Runs[id] = r
		if err := s.commit(key, next, &Event{RunID: id, State: Failed, Time: r.Updated}); err != nil {
			s.markBroken(key, err)
			return err
		}
	}
	return nil
}
func (s *Store) releaseIdle() {
	for key, v := range s.data {
		if s.broken[key] != nil {
			delete(s.data, key)
			continue
		}
		live := false
		for _, r := range v.Runs {
			if !terminal(r.State) {
				live = true
				break
			}
		}
		if !live && len(s.subs[key]) == 0 && s.now().Sub(s.accessed[key]) >= idleRelease {
			delete(s.data, key)
		}
	}
}

// SettlementError preserves whether dispatch failed or only recording failed.
type SettlementError struct{ CallErr, StoreErr error }

func (e *SettlementError) Error() string {
	if e.CallErr == nil {
		return "call succeeded; settlement not recorded"
	}
	return "call failed; settlement not recorded"
}
func (e *SettlementError) Unwrap() error { return e.StoreErr }

// Minimal terminal records preserve accumulated history and the last charge,
// while dropping newly returned output when ordinary admission has no room.
func boundedReason(reason string) string {
	if len(reason) > 128 {
		return "run failed"
	}
	return reason
}

// Clone only a returned run, not all of its key's captured bytes.
func copyRun(r Run) Run {
	raw, _ := json.Marshal(r)
	var copy Run
	_ = json.Unmarshal(raw, &copy)
	return copy
}
