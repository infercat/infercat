package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func Test116DCallFailureAndPurposeSurviveSettlement(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			s := store(t)
			disk := errors.New("fixture disk failure")
			m := manager(t, s, func(_ context.Context, _ string, step Step, acquired func() error) (StepResult, error) {
				if err := acquired(); err != nil {
					return StepResult{}, err
				}
				if broken {
					s.mu.Lock()
					s.write = func(string, []byte) error { return disk }
					s.mu.Unlock()
				}
				return StepResult{Settled: true}, Failure("key_revoked")
			}, nil)
			r := create(t, s, "k_cause")
			got, _, err := m.attempt(context.Background(), r, Step{Input: json.RawMessage(`{}`), Purpose: "session-title"}, false)
			var cause Failure
			if !errors.As(err, &cause) || cause != "key_revoked" {
				t.Fatal("call cause lost", err)
			}
			if broken {
				var settlement *SettlementError
				if !errors.As(err, &settlement) || !errors.Is(err, disk) || settlement.CallErr == nil {
					t.Fatal("settlement cause lost", err)
				}
			} else {
				reopened, e := NewStore(filepath.Dir(s.root))
				if e != nil {
					t.Fatal(e)
				}
				got, e = reopened.Get(r.KeyID, r.ID)
				if e != nil || len(got.Attempts) != 1 || got.Attempts[0].Purpose != "session-title" || !got.Attempts[0].Settled {
					t.Fatal(got, e)
				}
			}
		})
	}
}

func Test116DLeaseFreeCancelNoteOnceAndFailureAtomicity(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_note")
	_, err := s.change(r.KeyID, r.ID, func(v *Run) error { v.CancelRequested = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	note := []json.RawMessage{json.RawMessage(`{"type":"turn/end","reason":"cancelled"}`)}
	// Another live lease's capacity cannot be spent by the lease-free note.
	s.reserved = map[string]map[string]*reservation{r.KeyID: {"other": {bytes: MaxStored}}}
	if err = s.Retain(r.KeyID, r.ID, nil, note, nil); !errors.Is(err, ErrLimit) {
		t.Fatal("note bypassed the shared budget", err)
	}
	delete(s.reserved[r.KeyID], "other")
	original := s.write
	s.write = func(string, []byte) error { return ErrLimit }
	if err = s.Retain(r.KeyID, r.ID, nil, note, nil); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if s.data[r.KeyID].Retained[r.ID].CancelNote {
		t.Fatal("failed commit spent the note")
	}
	s.write = original
	if err = s.Retain(r.KeyID, r.ID, nil, note, map[string]Captured{"out": {Name: "x", MIME: "text/plain", Data: []byte("no")}}); !errors.Is(err, ErrLimit) {
		t.Fatal("post-cancel output allowed", err)
	}
	if err = s.Retain(r.KeyID, r.ID, nil, note, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.Retain(r.KeyID, r.ID, nil, note, nil); !errors.Is(err, ErrLimit) {
		t.Fatal("second note allowed", err)
	}
	reopened, err := NewStore(filepath.Dir(s.root))
	if err != nil {
		t.Fatal(err)
	}
	held, err := reopened.Retained(r.KeyID, r.ID)
	if err != nil || !held.CancelNote || len(held.Trajectory) != 1 {
		t.Fatal(held, err)
	}
	// An oversized note refuses before consuming the once-only allowance.
	other := create(t, s, "k_other")
	s.change(other.KeyID, other.ID, func(v *Run) error { v.CancelRequested = true; return nil })
	raw, _ := json.Marshal(string(make([]byte, terminalBound)))
	if err = s.Retain(other.KeyID, other.ID, raw, nil, nil); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if err = s.Retain(other.KeyID, other.ID, nil, note, nil); err != nil {
		t.Fatal(err)
	}
}

func Test116DTerminalApprovalProjectionAfterRecovery(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_approval")
	_, err := s.change(r.KeyID, r.ID, func(v *Run) error { v.State = Waiting; return nil }, func(d *Retained) error {
		d.Approval = &Approval{ID: "ask", Request: "Allow once?", Status: "pending"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(filepath.Dir(s.root))
	if err != nil {
		t.Fatal(err)
	}
	manager(t, reopened, nil, nil)
	d, err := reopened.Detail(r.KeyID, r.ID)
	if err != nil || d.State != Failed || d.Approval != nil {
		t.Fatal(d, err)
	}
	held, _ := reopened.Retained(r.KeyID, r.ID)
	if held.Approval.Status != "pending" {
		t.Fatal("projection rewrote history")
	}
}

func Test116DSharedStepRingResetsAndRecoversImage(t *testing.T) {
	s := store(t)
	image, err := s.Create("k_ring", "image", "interactive", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	agent := create(t, s, "k_ring")
	replay, _, stop, err := s.Subscribe(image.KeyID, "")
	if err != nil {
		t.Fatal(err)
	}
	cursor := replay[0].Cursor
	stop()
	release, err := s.Admit(agent.KeyID, agent.ID, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	step := StepEvent{ID: "think", Type: "step", At: time.Now().UTC(), Kind: "think", Status: "running"}
	for i := range 260 {
		step.Text = fmt.Sprint(i)
		if err = s.Retain(agent.KeyID, agent.ID, nil, nil, nil, step); err != nil {
			t.Fatal(err)
		}
	}
	replay, _, stop, err = s.Subscribe(image.KeyID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if len(replay) != 1 || !replay[0].Reset || len(replay[0].Runs) != 2 {
		t.Fatal(replay)
	}
	if _, err = s.Detail(image.KeyID, image.ID); err != nil {
		t.Fatal("image missing from recovery", err)
	}
	d, err := s.Detail(agent.KeyID, agent.ID)
	if err != nil || len(d.Steps) != 1 || d.Steps[0].Text != "259" {
		t.Fatal(d, err)
	}
}
