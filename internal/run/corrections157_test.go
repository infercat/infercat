package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func Test157StopJoinAndQuarantine(t *testing.T) {
	for _, mode := range []string{"success", "timeout", "panic"} {
		t.Run(mode, func(t *testing.T) {
			s := store(t)
			m := manager(t, s, nil, nil)
			m.joinTimeout = 15 * time.Millisecond
			m.stopTimeout = 30 * time.Millisecond
			logs := make(chan string, 8)
			s.Log = func(f string, args ...any) { logs <- fmt.Sprintf(f, args...) }
			_, events, unsubscribe, err := s.Subscribe("key", "")
			if err != nil {
				t.Fatal(err)
			}
			defer unsubscribe()
			entered := make(chan string, 4)
			stopping := make(chan string, 1)
			release := make(chan struct{})
			stuck := make(chan struct{})
			defer close(stuck)
			var calls, releases atomic.Int32
			m.Register("test", m.Consumer(func(_ context.Context, w *Work) (json.RawMessage, error) {
				entered <- w.Run.ID
				if calls.Add(1) == 1 {
					<-stuck
				}
				return json.RawMessage(`{}`), nil
			}), Policy{Serial: true, JoinCancel: true, Release: func(string, string) { releases.Add(1) }, ForceStop: func(id string) {
				stopping <- id
				if mode == "panic" {
					panic("fixture")
				}
				<-release
			}})
			a, err := m.Submit("key", "test", "", json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			b, err := m.Submit("key", "test", "", json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			m.Cancel(a.KeyID, a.ID)
			if got := <-stopping; got != a.ID {
				t.Fatal("wrong generation identity", got)
			}
			select {
			case <-entered:
				t.Fatal("scheduled while stop pending")
			default:
			}
			if mode == "success" {
				close(release)
			} else {
				await(t, func() bool { return state(s, a) == Cancelled })
				if _, err = m.Submit("key", "test", "", json.RawMessage(`{}`)); !errors.Is(err, ErrQuarantined) {
					t.Fatal("quarantine accepted work", err)
				}
				select {
				case <-entered:
					t.Fatal("quarantined kind started")
				default:
				}
				if mode == "timeout" {
					close(release)
				}
			}
			if mode == "success" {
				select {
				case id := <-entered:
					if id != b.ID {
						t.Fatal(id)
					}
				case <-time.After(time.Second):
					t.Fatal("successful stop did not resume")
				}
			} else {
				await(t, func() bool { return state(s, b) == Failed && releases.Load() == 2 })
				got, _ := s.Get(b.KeyID, b.ID)
				if got.Reason != "runtime quarantined" {
					t.Fatal(got.Reason)
				}
				seen := false
				for len(events) > 0 {
					e := <-events
					seen = seen || e.RunID == b.ID && e.State == Failed
				}
				if !seen {
					t.Fatal("queued quarantine did not publish terminal event")
				}
				select {
				case line := <-logs:
					if !strings.Contains(line, "force-stop "+mode) {
						t.Fatal(line)
					}
				default:
					t.Fatal("quarantine entry not logged")
				}

				if mode == "timeout" {
					await(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return !m.unavailable("test") })
					c := submit(t, m)
					await(t, func() bool { return state(s, c) == Done })
					select {
					case line := <-logs:
						if !strings.Contains(line, "quarantine cleared") {
							t.Fatal(line)
						}
					default:
						t.Fatal("recovery not logged")
					}
				}
			}

		})
	}
}

func Test157ResidencyEpochUpdatedAndLastImageExpiry(t *testing.T) {
	s := store(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	r, err := s.Create("key", "image", "interactive", json.RawMessage(`{"prompt":"fixture"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutImage(r.KeyID, r.ID, []byte("png"), "image/png", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.change(r.KeyID, r.ID, func(v *Run) error { v.State = Done; return nil })
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(idleRelease + time.Second)
	s.List("key")
	if s.data["key"] == nil {
		t.Fatal("polled key evicted")
	}
	s.mu.Lock()
	v := s.data["key"]
	s.mu.Unlock()
	if _, err = s.List(""); err != nil || s.data["key"] != v {
		t.Fatal("resident global list reloaded", err)
	}
	now = now.Add(Retention + time.Second)
	m := &Manager{Store: s, active: map[string]*execution{}}
	if err = m.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.artifactPath(r.KeyID, r.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("last expired image leaked", err)
	}
	replay, _, stop, err := s.Subscribe("empty", "")
	if err != nil {
		t.Fatal(err)
	}
	cursor := replay[0].Cursor
	stop()
	now = now.Add(idleRelease + time.Second)
	s.List("key")
	replay, _, stop, err = s.Subscribe("empty", cursor)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if len(replay) != 1 || !replay[0].Reset {
		t.Fatal(replay)
	}
	r = create(t, s, "key2")
	release, _ := s.Admit(r.KeyID, r.ID, 8192)
	defer release()
	now = now.Add(time.Second)
	if err = s.Retain(r.KeyID, r.ID, nil, []json.RawMessage{json.RawMessage(`{}`)}, nil); err != nil {
		t.Fatal(err)
	}
	updated, _ := s.Get(r.KeyID, r.ID)
	if !updated.Updated.Equal(now) {
		t.Fatal("Updated did not advance")
	}
}

func Test157WaitingFallbackAndUsageIdentity(t *testing.T) {
	s := store(t)
	filler := create(t, s, "key")
	r := create(t, s, "key")
	_, _ = s.change(filler.KeyID, filler.ID, func(v *Run) error { v.State = Done; return nil })
	ceilingFixture(t, s, filler, MaxLiveKey*terminalBound)
	m := &Manager{Store: s, active: map[string]*execution{}}
	m.finish(r.KeyID, r.ID, Waiting, "approval", nil)
	got, err := s.Get(r.KeyID, r.ID)
	if err != nil || got.State != Waiting {
		t.Fatal(got, err)
	}
	model := strings.Repeat("m", 200)
	_, err = s.change(r.KeyID, r.ID, func(v *Run) error {
		v.State = Done
		v.Attempts = []Attempt{{ID: "a_identity"}}
		v.Attempts[0].Usage.Model = model
		v.Attempts[0].Usage.Code = "code_" + model
		v.Attempts[0].Usage.Endpoint = "/" + model
		v.Attempts[0].Output = json.RawMessage(`"` + strings.Repeat("x", 10000) + `"`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(r.KeyID, r.ID)
	if got.Attempts[0].Usage.Model != model || got.Attempts[0].Usage.Code != "code_"+model || got.Attempts[0].Usage.Endpoint != "/"+model {
		t.Fatal("usage identity rewritten")
	}
	s.mu.Lock()
	s.markBroken(r.KeyID, ErrLimit)
	s.mu.Unlock()
	if _, err = s.Get(r.KeyID, r.ID); err != nil {
		t.Fatal("budget bricked key", err)
	}
}

func Test157EmptyScanAndEveryEvictedArtifactEvent(t *testing.T) {
	s := store(t)
	logs := 0
	s.Log = func(string, ...any) { logs++ }
	a, err := s.Create("key", "image", "interactive", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create("key", "image", "interactive", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	s.imageBudget = 4
	if _, err = s.PutImage("key", a.ID, []byte("1234"), "image/png", 1, 1); err != nil {
		t.Fatal(err)
	}
	_, ch, stop, err := s.Subscribe("key", "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err = s.PutImage("key", b.ID, []byte("5678"), "image/png", 1, 1); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case e := <-ch:
			seen[e.RunID] = true
		case <-time.After(time.Second):
			t.Fatal("missing eviction event")
		}
	}
	if !seen[a.ID] || !seen[b.ID] {
		t.Fatal(seen)
	}
	s.mu.Lock()
	err = s.sweepImages("key", &snapshot{})
	s.mu.Unlock()
	if err != nil || logs == 0 {
		t.Fatal("empty scan not refused and logged", err)
	}
	if _, err = os.Stat(s.artifactPath("key", b.ID)); err != nil {
		t.Fatal("empty snapshot deleted healthy artifact", err)
	}
}
