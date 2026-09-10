package run

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Review F2's above-ceiling state, arranged directly to avoid ten expensive
// full-size terminal commits in each race run. Expiry must shrink while STILL
// above the ordinary limit, and must stay recoverable after restart.
func Test157ExpiryAboveCeilingAndRestart(t *testing.T) {
	s := store(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	filler, small := create(t, s, "key"), create(t, s, "key")
	for _, r := range []Run{filler, small} {
		_, err := s.change(r.KeyID, r.ID, func(v *Run) error { v.State = Done; return nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	v := s.data[small.KeyID]
	r := v.Runs[small.ID]
	r.Expires = now.Add(-time.Hour)
	v.Runs[small.ID] = r
	s.mu.Unlock()
	ceilingFixture(t, s, filler, MaxLiveKey*terminalBound-2048)
	reopened, err := NewStore(filepath.Dir(s.root))
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = func() time.Time { return now }
	m, err := New(reopened, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Get(small.KeyID, small.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expiry did not commit", err)
	}
	if _, err = reopened.Get(filler.KeyID, filler.ID); err != nil {
		t.Fatal("healthy key bricked", err)
	}
	reopened.mu.Lock()
	raw, _ := json.Marshal(reopened.data[filler.KeyID])
	reopened.mu.Unlock()
	if len(raw) <= MaxStored-MaxLiveKey*terminalBound {
		t.Fatal("fixture fell below ordinary ceiling")
	}
	again, err := NewStore(filepath.Dir(s.root))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = again.Get(filler.KeyID, filler.ID); err != nil {
		t.Fatal("restart remains broken", err)
	}
}
