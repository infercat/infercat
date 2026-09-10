package run

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Review zadv157d: one run must not earn another 4 KiB on each lifecycle flip.
func Test157V2ExceptionPerRunSurvivesRestart(t *testing.T) {
	s := store(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	filler, r := create(t, s, "key"), create(t, s, "key")
	s.change(filler.KeyID, filler.ID, func(v *Run) error { v.State = Done; return nil })
	ceilingFixture(t, s, filler, MaxLiveKey*terminalBound)
	// A new-run refusal must not spend an existing-run lifecycle exception.
	oldSeq := s.data["key"].Seq
	if _, err := s.Create("key", "test", "interactive", json.RawMessage(`{}`)); !errors.Is(err, ErrLimit) {
		t.Fatal("new admission used exception", err)
	}
	if len(s.data["key"].Runs) != 2 || s.data["key"].Seq != oldSeq {
		t.Fatal("refused admission mutated state")
	}
	start := s.data["key"].encodedBytes
	previous := 0
	failed := false
	logs := 0
	for i := range 40 {
		s.Log = func(string, ...any) { logs++ }
		now = now.Add(time.Second)
		got, err := s.change(r.KeyID, r.ID, func(v *Run) error {
			if v.State == Queued {
				v.State = Running
			} else {
				v.State = Queued
			}
			v.Attempts = append(v.Attempts, Attempt{ID: id("a_"), Output: json.RawMessage(`"` + strings.Repeat("x", 3000) + `"`)})
			return nil
		})
		used := s.data["key"].ExceptionBytes[r.ID]
		if used < previous || used > terminalBound {
			t.Fatal("allowance re-earned", previous, used)
		}
		previous = used
		if s.data["key"].encodedBytes-start > terminalBound {
			t.Fatal("per-run growth exceeded")
		}
		if err != nil {
			if !committedTerminal(err) || errors.Is(err, ErrLimit) || got.State != Failed || got.Reason != "storage exhausted" {
				t.Fatal(got.State, got.Reason, err)
			}
			failed = true
			break
		}
		if i == 3 {
			reopened, e := NewStore(filepath.Dir(s.root))
			if e != nil {
				t.Fatal(e)
			}
			s = reopened
			s.now = func() time.Time { return now }
			if _, e = s.Get(r.KeyID, r.ID); e != nil {
				t.Fatal(e)
			}
			if s.data["key"].ExceptionBytes[r.ID] != previous {
				t.Fatal("restart forgot allowance")
			}
		}
	}
	if !failed || logs == 0 {
		t.Fatal("not visibly terminal at exhaustion", failed, logs)
	}
	got, err := s.Get(r.KeyID, r.ID)
	if err != nil || len(got.Attempts) < 4 {
		t.Fatal("attempts lost or key broken", err)
	}
	t.Logf("terminal after %d attempts; tally=%d; growth=%d", len(got.Attempts), previous, s.data["key"].encodedBytes-start)
}

func Test157V2AbsoluteCapTrimsReplayNotEvidence(t *testing.T) {
	s := store(t)
	filler := create(t, s, "key")
	r, err := s.Create("key", "image", "interactive", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	s.change(filler.KeyID, filler.ID, func(v *Run) error { v.State = Done; return nil })
	output, err := s.PutImage("key", r.ID, []byte("image-bytes"), "image/png", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := Attempt{ID: "a_preserve", Output: json.RawMessage(`{"answer":"retained"}`), Settled: true}
	attempt.Usage.Model = "identity"
	attempt.Usage.Prompt = "already retained prompt"
	_, err = s.change("key", r.ID, func(v *Run) error { v.State = Running; v.Attempts = []Attempt{attempt}; return nil })
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.Admit("key", r.ID, 8192)
	if err != nil {
		t.Fatal(err)
	}
	held := map[string]Captured{"proof": {Name: "proof.txt", MIME: "text/plain", Data: []byte("keep")}}
	if err = s.Retain("key", r.ID, nil, []json.RawMessage{json.RawMessage(`{"native":"keep"}`)}, held); err != nil {
		t.Fatal(err)
	}
	release()
	before, _ := s.Retained("key", r.ID)
	cursor := s.data["key"].Epoch + ":0"
	_, ch, stop, err := s.Subscribe("key", "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ceilingFixture(t, s, filler, -MaxRuns*terminalBound)
	logs := 0
	s.Log = func(string, ...any) { logs++ }
	got, err := s.change("key", r.ID, func(v *Run) error { v.State = Waiting; return nil })
	if !committedTerminal(err) || errors.Is(err, ErrLimit) || got.State != Failed || got.Reason != "storage exhausted" {
		t.Fatal(got.State, got.Reason, err)
	}
	if !reflect.DeepEqual(got.Attempts, []Attempt{attempt}) || string(got.Output) != string(output) {
		t.Fatal("metered attempts or image metadata changed")
	}
	after, err := s.Retained("key", r.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("retained evidence changed", err)
	}
	if _, _, err = s.ReadImage("key", r.ID); err != nil {
		t.Fatal(err)
	}
	if logs == 0 || s.data["key"].encodedBytes > MaxStored+MaxRuns*terminalBound {
		t.Fatal("unlogged or oversized")
	}
	select {
	case e := <-ch:
		if e.State != Failed {
			t.Fatal(e)
		}
	default:
		t.Fatal("terminal event absent")
	}
	replay, _, closeReplay, err := s.Subscribe("key", cursor)
	if err != nil {
		t.Fatal(err)
	}
	closeReplay()
	if len(replay) != 1 || !replay[0].Reset {
		t.Fatal("trim did not reset old cursor", replay)
	}
	reopened, err := NewStore(filepath.Dir(s.root))
	if err != nil {
		t.Fatal(err)
	}
	got, err = reopened.Get("key", r.ID)
	if err != nil || got.State != Failed {
		t.Fatal("terminal lost on restart", err)
	}
}

func Test157V2FullRecoveryContinuesAfterMinimalTerminal(t *testing.T) {
	s := store(t)
	filler, a, b := create(t, s, "key"), create(t, s, "key"), create(t, s, "key")
	s.change(filler.KeyID, filler.ID, func(v *Run) error { v.State = Done; return nil })
	ceilingFixture(t, s, filler, -MaxRuns*terminalBound)
	reopened, err := NewStore(filepath.Dir(s.root))
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(reopened, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for _, r := range []Run{a, b} {
		got, err := reopened.Get(r.KeyID, r.ID)
		if err != nil || got.State != Failed {
			t.Fatal("recovery stopped after first minimal commit", got.State, err)
		}
	}
}
