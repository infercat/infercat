package run

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestInterruptedChatToolKeepsIndependentImages(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.Create("key", "chat", "interactive", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.Admit("key", parent.ID, 32<<10)
	if err != nil {
		t.Fatal(err)
	}
	step := StepEvent{ID: "tool_1", Type: "step", At: time.Now().UTC(), Kind: "other", Tool: "make_image", Status: "running", Result: "submitting image jobs; outcome not confirmed"}
	if err = s.Retain("key", parent.ID, nil, nil, nil, step); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ImageInput{Prompt: "fox", ParentRunID: parent.ID, ToolCallID: step.ID})
	child, err := s.Create("key", "image", "interactive", raw)
	if err != nil {
		t.Fatal(err)
	}
	release()
	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(reopened, func(context.Context, string, Step, func() error) (StepResult, error) {
		t.Error("replayed work")
		return StepResult{}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	got, err := reopened.Get("key", parent.ID)
	if err != nil || got.State != Failed {
		t.Fatal(got, err)
	}
	data, err := reopened.Retained("key", parent.ID)
	if err != nil || len(data.Steps) != 1 || data.Steps[0].Status != "failed" || data.Steps[0].At != step.At {
		t.Fatal(data, err)
	}
	if data.Steps[0].Result != "interrupted; image submission outcome not confirmed; not submitted again" {
		t.Fatal(data)
	}
	image, err := reopened.Get("key", child.ID)
	if err != nil {
		t.Fatal(err)
	}
	var input ImageInput
	json.Unmarshal(image.Input, &input)
	if input.ParentRunID != parent.ID || input.ToolCallID != step.ID {
		t.Fatal(input)
	}
	rows, _ := reopened.List("key")
	if len(rows) != 2 {
		t.Fatal("duplicate or missing jobs", rows)
	}
}
