package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func Test157V2RequiresStopHook(t *testing.T) {
	s := store(t)
	m := manager(t, s, nil, nil)
	if err := m.Register("missing", m.Consumer(func(context.Context, *Work) (json.RawMessage, error) { return nil, nil }), Policy{JoinCancel: true}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, ok := m.Kinds["missing"]; ok {
		t.Fatal("refusal mutated registry")
	}
}
func Test157V2TwentyModelStepsDoNotCloseSubscriber(t *testing.T) {
	s := store(t)
	m := manager(t, s, func(_ context.Context, _ string, _ Step, acquired func() error) (StepResult, error) {
		if err := acquired(); err != nil {
			return StepResult{}, err
		}
		return StepResult{Settled: true, Output: json.RawMessage(`{"ok":1}`)}, nil
	}, nil)
	if err := m.Register("test", m.Consumer(func(_ context.Context, w *Work) (json.RawMessage, error) {
		for range 20 {
			if _, err := w.Step(Step{Input: json.RawMessage(`{}`)}); err != nil {
				return nil, err
			}
		}
		return json.RawMessage(`{}`), nil
	}), Policy{}); err != nil {
		t.Fatal(err)
	}
	_, ch, stop, err := s.Subscribe("key", "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	r, err := m.Submit("key", "test", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return state(s, r) == Done })
	got, _ := s.Get(r.KeyID, r.ID)
	if len(got.Attempts) != 20 {
		t.Fatal(len(got.Attempts))
	}
	n := 0
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				t.Fatal("subscriber dropped")
			}
			n++
		default:
			if n != 3 {
				t.Fatal("expected queued, running, done", n)
			}
			return
		}
	}
}
func Test157V2EnumerationReleasesIdleKeys(t *testing.T) {
	s := store(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	for i := range 8 {
		r := create(t, s, "k_"+strings.Repeat("a", i+1))
		_, err := s.change(r.KeyID, r.ID, func(v *Run) error {
			v.State = Done
			v.Output = json.RawMessage(`"` + strings.Repeat("x", 1<<20) + `"`)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(idleRelease + time.Hour)
	for range 4 {
		started := time.Now()
		rows, err := s.List("")
		t.Logf("all-key read: 8 x 1 MiB idle snapshots, elapsed=%v", time.Since(started))
		if err != nil || len(rows) != 8 {
			t.Fatal(len(rows), err)
		}
		if len(s.data) != 0 {
			t.Fatal("enumeration pinned idle keys", len(s.data))
		}
		now = now.Add(time.Minute)
	}
	s.List("k_a")
	if s.data["k_a"] == nil {
		t.Fatal("single-key poll evicted")
	}
}
func Test157V2EmptyImagesLogOnlyChanges(t *testing.T) {
	s := store(t)
	lines := 0
	s.Log = func(string, ...any) { lines++ }
	empty := &snapshot{}
	for range 60 {
		if err := s.sweepImages("key", empty); err != nil {
			t.Fatal(err)
		}
	}
	if lines != 0 {
		t.Fatal("logged missing directory", lines)
	}
	dir := filepath.Join(s.root, "key", "images")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "orphan"), []byte("keep"), 0600)
	for range 60 {
		if err := s.sweepImages("key", empty); err != nil {
			t.Fatal(err)
		}
	}
	if lines != 1 || imageCleanupPending(s) != 1 {
		t.Fatal("repeated orphan log or missing status count", lines, imageCleanupPending(s))
	}
	later := time.Now().Add(time.Hour)
	os.Chtimes(dir, later, later)
	if err := s.sweepImages("key", empty); err != nil {
		t.Fatal(err)
	}
	if lines != 2 {
		t.Fatal("changed directory not logged", lines)
	}
	if _, err := os.Stat(filepath.Join(dir, "orphan")); err != nil {
		t.Fatal("orphan deleted", err)
	}
	// 158 v4 narrows report-only handling to empty snapshots; live rows restore normal collection.
	r := create(t, s, "key")
	if err := s.sweepImages("key", s.data[r.KeyID]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "orphan")); !os.IsNotExist(err) {
		t.Fatal("nonempty snapshot did not collect unnamed file", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := s.sweepImages("key", empty); err != nil || imageCleanupPending(s) != 0 {
		t.Fatal("vanished directory retained or broken", err)
	}

}
func Test157V2ExpiryContinuesAfterCancelRefusal(t *testing.T) {
	s := store(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	a := create(t, s, "key")
	b := create(t, s, "key")
	_, err := s.change(b.KeyID, b.ID, func(v *Run) error { v.State = Done; return nil })
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(Retention + MaxAge + time.Hour)
	write := s.write
	calls := 0
	s.write = func(p string, b []byte) error {
		calls++
		if calls == 1 {
			return ErrLimit
		}
		return write(p, b)
	}
	m := &Manager{Store: s, active: map[string]*execution{}}
	if err = m.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(b.KeyID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expiry skipped", err)
	}
	if _, err = s.Get(a.KeyID, a.ID); err != nil {
		t.Fatal("key broken", err)
	}
}
