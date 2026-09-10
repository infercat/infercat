package run

import (
	"fmt"
	"path/filepath"
	"testing"
)

func Test157V5RecoveryRetriesAfterRefusedWrite(t *testing.T) {
	s := store(t)
	r := create(t, s, "key")
	s.change(r.KeyID, r.ID, func(r *Run) error { r.State = Waiting; return nil })
	s.recovered[r.KeyID] = true // A previous successful pass must not hide a later refusal.
	writes := 0
	s.write = func(path string, raw []byte) error {
		writes++
		if writes == 1 {
			return ErrLimit
		}
		return atomicWrite(path, raw)
	}
	m, err := New(s, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if s.recovered[r.KeyID] {
		t.Fatal("failed recovery marked complete")
	}
	got, err := s.Get(r.KeyID, r.ID)
	if err != nil || got.State != Failed || !s.recovered[r.KeyID] || writes != 2 {
		t.Fatal(got.State, err, writes)
	}
	fresh, _ := NewStore(filepath.Dir(s.root))
	got, err = fresh.Get(r.KeyID, r.ID)
	if err != nil || got.State != Failed {
		t.Fatal("recovery not durable", got.State, err)
	}
}
func Test157V5TerminalMessageHasNoDanglingColon(t *testing.T) {
	if got := (&CommittedTerminal{Run: Run{State: Done}}).Error(); got != "run ended" {
		t.Fatal(got)
	}
}

func Test157V5TerminalReserveOrReplayAllowsRecovery(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(fmt.Sprint(replay), func(t *testing.T) {
			s := store(t)
			filler, live := create(t, s, "key"), create(t, s, "key")
			s.change("key", filler.ID, func(r *Run) error { r.State = Done; return nil })
			s.change("key", live.ID, func(r *Run) error { r.State = Waiting; return nil })
			s.mu.Lock()
			used := terminalBound
			if !replay {
				s.data["key"].Events = []Event{}
				used -= 512
			}
			s.data["key"].ExceptionBytes = map[string]int{live.ID: used}
			s.mu.Unlock()
			ceilingFixture(t, s, filler, MaxLiveKey*terminalBound)
			fresh, err := NewStore(filepath.Dir(s.root))
			if err != nil {
				t.Fatal(err)
			}
			m, err := New(fresh, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			got, err := fresh.Get("key", live.ID)
			if err != nil || got.State != Failed {
				t.Fatal("recoverable snapshot refused", got.State, err)
			}
		})
	}
}
