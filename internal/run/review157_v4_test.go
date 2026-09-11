package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func Test157V4ResumeUsesSerialSchedule(t *testing.T) {
	s := store(t)
	m := manager(t, s, nil, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls, active, peak atomic.Int32
	if err := m.Register("test", func(context.Context, Run) (Decision, error) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > peak.Load() {
			peak.Store(n)
		}
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return Decision{Output: json.RawMessage(`{}`)}, nil
	}, Policy{Serial: true, JoinCancel: true, ForceStop: func(string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	a := submit(t, m)
	<-entered
	b := create(t, s, "k_b")
	if _, err := s.change(b.KeyID, b.ID, func(r *Run) error { r.State = Waiting; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(b.KeyID, b.ID); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	started := m.active[b.ID] != nil
	m.mu.Unlock()
	if started || calls.Load() != 1 || state(s, b) != Queued {
		t.Fatal("Resume bypassed serial admission", started, calls.Load(), state(s, b))
	}
	close(release)
	await(t, func() bool { return state(s, a) == Done && state(s, b) == Done })
	if peak.Load() != 1 {
		t.Fatal("parallel kind bodies", peak.Load())
	}
}
func Test157V4CloseDoesNotQuarantineAcceptedRows(t *testing.T) {
	s := store(t)
	m := manager(t, s, nil, nil)
	m.joinTimeout = 10 * time.Millisecond
	m.stopTimeout = time.Second
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	if err := m.Register("test", m.Consumer(func(context.Context, *Work) (json.RawMessage, error) { close(entered); <-release; return nil, nil }), Policy{Serial: true, JoinCancel: true, ForceStop: func(string) error { return errors.New("stop failed") }}); err != nil {
		t.Fatal(err)
	}
	r := submit(t, m)
	<-entered
	m.mu.Lock()
	w := m.active[r.ID]
	m.mu.Unlock()
	var logged atomic.Bool
	s.Log = func(f string, args ...any) {
		if strings.Contains(fmt.Sprintf(f, args...), "force-stop error") {
			logged.Store(true)
		}
	}
	queued, waiting := create(t, s, "k_queued"), create(t, s, "k_waiting")
	s.change(waiting.KeyID, waiting.ID, func(r *Run) error { r.State = Waiting; return nil })
	m.Close()
	// Close has its own deadline; observe the completed join before its effects.
	select {
	case <-w.joined:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown join did not finish")
	}
	if !logged.Load() {
		t.Fatal("shutdown stop failure was silent")
	}
	if state(s, queued) != Queued || state(s, waiting) != Waiting {
		t.Fatal("shutdown quarantined accepted work", state(s, queued), state(s, waiting))
	}
	m.mu.Lock()
	quarantined := m.quarantined["test"]
	m.mu.Unlock()
	if quarantined {
		t.Fatal("shutdown installed quarantine")
	}
}
func Test157V4CleanupPopulationsStayDistinct(t *testing.T) {
	s := store(t)
	s.imageCleanup = map[string]bool{"proven": true, "observed": false}
	proven, review := s.ImageCleanupCounts()
	if proven != 1 || review != 1 || s.ImageCleanupPending() != 2 {
		t.Fatal(proven, review)
	}
}
func Test157V4CeilingEnumerationCost(t *testing.T) {
	s := store(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	r := create(t, s, "key")
	s.change(r.KeyID, r.ID, func(v *Run) error { v.State = Done; return nil })
	ceilingFixture(t, s, r, MaxLiveKey*terminalBound)
	now = now.Add(idleRelease + time.Second)
	s.mu.Lock()
	s.releaseIdle()
	s.mu.Unlock()
	for range 3 {
		began := time.Now()
		rows, err := s.List("")
		elapsed := time.Since(began)
		if err != nil || len(rows) != 1 || len(s.data) != 0 {
			t.Fatal(len(rows), len(s.data), err)
		}
		t.Logf("nonresident ceiling key: bytes=%d elapsed=%v", MaxStored-MaxLiveKey*terminalBound, elapsed)
	}
}

func Test157V4ExpiredJoinUsesTerminalReserve(t *testing.T) {
	s := store(t)
	m := manager(t, s, nil, nil)
	m.joinTimeout = 10 * time.Millisecond
	m.stopTimeout = 100 * time.Millisecond
	release := make(chan struct{})
	var stops atomic.Int32
	if err := m.Register("test", m.Consumer(func(ctx context.Context, w *Work) (json.RawMessage, error) {
		_, err := w.Approval("ap_wait", "continue?")
		<-release
		return nil, err
	}), Policy{Serial: true, JoinCancel: true, ForceStop: func(string) error {
		if stops.Add(1) == 1 {
			close(release)
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	filler := create(t, s, "key")
	s.change("key", filler.ID, func(v *Run) error { v.State = Done; return nil })
	r, err := m.Submit("key", "test", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return state(s, r) == Waiting })
	exhaust157(t, s, filler, r.ID)
	s.mu.Lock()
	s.data["key"].ExceptionBytes[r.ID] = terminalBound - 512
	s.mu.Unlock()
	m.mu.Lock()
	worker := m.active[r.ID]
	worker.cancel()
	m.boundJoin(r, worker)
	m.mu.Unlock()
	select {
	case <-worker.joined:
	case <-time.After(30 * time.Second):
		t.Fatal("exhausted join did not settle")
	}
	got, err := s.Get(r.KeyID, r.ID)
	if err != nil || !terminal(got.State) || stops.Load() != 1 {
		t.Fatal("expired join left waiting row", got.State, err, stops.Load())
	}
}
func Test157V4SweepRecognizesCommittedTerminal(t *testing.T) {
	s := store(t)
	m := manager(t, s, nil, nil)
	filler, r := create(t, s, "key"), create(t, s, "key")
	s.change("key", filler.ID, func(v *Run) error { v.State = Done; return nil })
	s.change("key", r.ID, func(v *Run) error { v.Expires = s.now().Add(-time.Minute); return nil })
	exhaust157(t, s, filler, r.ID)
	var logs []string
	s.Log = func(f string, _ ...any) { logs = append(logs, f) }
	if err := m.Sweep(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(r.KeyID, r.ID)
	if err != nil || got.State != Failed {
		t.Fatal(got.State, err)
	}
	for _, line := range logs {
		if line == "run expiry cancel failed for %s/%s: %v" {
			t.Fatal("committed terminal logged as failed cancellation")
		}
	}
}

func Test157V4LegacyFullKeyFailsClosedWithoutChangingBytes(t *testing.T) {
	for _, room := range []int{-MaxRuns * terminalBound, MaxLiveKey * terminalBound} {
		t.Run(fmt.Sprint(room), func(t *testing.T) {
			s := store(t)
			filler, live := create(t, s, "legacy"), create(t, s, "legacy")
			if _, err := s.change("legacy", filler.ID, func(r *Run) error { r.State = Done; return nil }); err != nil {
				t.Fatal(err)
			}
			if _, err := s.change("legacy", live.ID, func(r *Run) error { r.State = Waiting; return nil }); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			s.data["legacy"].Events = []Event{}
			s.data["legacy"].ExceptionBytes = map[string]int{live.ID: terminalBound}
			s.mu.Unlock()
			ceilingFixture(t, s, filler, room)
			path := filepath.Join(s.root, "legacy", "state.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := NewStore(filepath.Dir(s.root))
			if err != nil {
				t.Fatal(err)
			}
			var logs []string
			fresh.Log = func(f string, _ ...any) { logs = append(logs, f) }
			m, err := New(fresh, nil, nil)
			if err != nil {
				t.Fatal("one key prevented startup", err)
			}
			defer m.Close()
			began := time.Now()
			if _, err := fresh.Get("legacy", live.ID); !errors.Is(err, ErrNeedsAttention) {
				t.Fatal(err)
			}
			t.Logf("allowance-check cold load bytes=%d elapsed=%v", len(before), time.Since(began))
			if _, err := fresh.Get("legacy", live.ID); !errors.Is(err, ErrNeedsAttention) {
				t.Fatal("second load admitted legacy key", err)
			}
			restarted, _ := NewStore(filepath.Dir(s.root))
			if _, err := restarted.Get("legacy", live.ID); !errors.Is(err, ErrNeedsAttention) {
				t.Fatal("mark not re-derived", err)
			}
			if fresh.broken["legacy"] == nil || fresh.data["legacy"] != nil {
				t.Fatal("legacy key was admitted")
			}
			if len(logs) == 0 {
				t.Fatal("failure was silent")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("snapshot changed", err)
			}
			other := create(t, fresh, "healthy")
			if _, err := fresh.Get("healthy", other.ID); err != nil {
				t.Fatal("other key unavailable", err)
			}
			t.Logf("legacy bytes=%d, failed closed; snapshot identical; healthy key served", len(before))
		})
	}
}
