package run

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestStepReplacementsReplayAndStorageWritesStaySilent(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_steps")
	replay, ch, stop, err := s.Subscribe(r.KeyID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	cursor := replay[len(replay)-1].Cursor
	release, err := s.Admit(r.KeyID, r.ID, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	step := StepEvent{ID: "s1", Type: "step", At: time.Now().UTC(), Kind: "think", Status: "running", Text: "first"}
	for _, text := range []string{"first", "replacement"} {
		step.Text = text
		if err = s.Retain(r.KeyID, r.ID, nil, nil, nil, step); err != nil {
			t.Fatal(err)
		}
		ev := <-ch
		if ev.Type != "step" || ev.Step == nil || !reflect.DeepEqual(*ev.Step, step) {
			t.Fatalf("wrong explicit event: %+v", ev)
		}
	}
	if err = s.Retain(r.KeyID, r.ID, nil, []json.RawMessage{json.RawMessage(`{"type":"native"}`)}, nil); err != nil {
		t.Fatal(err)
	}
	// Model-attempt settlement is durable ledger data, not a lifecycle update.
	if _, err = s.change(r.KeyID, r.ID, func(v *Run) error {
		v.Attempts = append(v.Attempts, Attempt{ID: "a_settled", Output: json.RawMessage(`{"role":"assistant","content":"done"}`)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		t.Fatal("pure storage published", ev)
	default:
	}
	again, _, closeAgain, err := s.Subscribe(r.KeyID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	closeAgain()
	if len(again) != 2 || again[0].Step.Text != "first" || again[1].Step.Text != "replacement" {
		t.Fatal(again)
	}
	data, _ := s.Retained(r.KeyID, r.ID)
	if len(data.Steps) != 1 || !reflect.DeepEqual(data.Steps[0], step) {
		t.Fatal(data)
	}
	changed := step
	changed.At = changed.At.Add(time.Second)
	if err = s.Retain(r.KeyID, r.ID, nil, nil, nil, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed identity accepted", err)
	}
	select {
	case ev := <-ch:
		t.Fatal("refusal published", ev)
	default:
	}
}
