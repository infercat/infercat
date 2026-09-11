package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
)

func Test116DPauseInsideAttemptKeepsKeyRevokedCause(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Agent = true })
	m := runManager(t, h, nil)
	entered, proceed := make(chan struct{}), make(chan struct{})
	m.Execute = func(ctx context.Context, key string, step runstate.Step, acquired func() error) (runstate.StepResult, error) {
		close(entered)
		<-proceed
		return h.gw.ExecuteStep(ctx, key, step, acquired)
	}
	observed := make(chan error, 1)
	err := m.Register("agent", m.Consumer(func(_ context.Context, w *runstate.Work) (json.RawMessage, error) {
		step := stepInput(false)
		step.RequireAgent = true
		_, err := w.Step(step)
		observed <- err
		return nil, err
	}), runstate.Policy{Serial: true, JoinCancel: true, ForceStop: func(string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Submit(h.key.ID, "agent", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	h.setKey(func(k *keys.Key) { k.Status = keys.Paused })
	close(proceed)
	err = <-observed
	var cause runstate.Failure
	if !errors.As(err, &cause) || cause != "key_revoked" {
		t.Fatal("Work.Step lost cause", err)
	}
	got := waitRun(t, m, h.key.ID, r.ID, runstate.Failed)
	if got.Reason != "key_revoked" || len(got.Attempts) != 1 || !got.Attempts[0].Settled || got.Attempts[0].Dispatched {
		t.Fatal(got)
	}
}

func Test116DFriendDeleteKeepsCancelReason(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Agent = true })
	m := runManager(t, h, nil)
	entered := make(chan struct{})
	if err := m.Register("agent", m.Consumer(func(ctx context.Context, _ *runstate.Work) (json.RawMessage, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}), runstate.Policy{Serial: true, JoinCancel: true, ForceStop: func(string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	r, err := m.Submit(h.key.ID, "agent", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if got := h.do("DELETE", "/v1/runs/"+r.ID, "Bearer "+testSecret, ""); got.status != 200 {
		t.Fatal(got)
	}
	waitRun(t, m, h.key.ID, r.ID, runstate.Cancelled)
	got := h.do("GET", "/v1/runs/"+r.ID, "Bearer "+testSecret, "")
	var view runstate.Detail
	if err = json.Unmarshal(got.body, &view); err != nil || got.status != 200 || view.State != runstate.Cancelled || view.Reason != "cancelled" {
		t.Fatal(got, err)
	}
}
