package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func store(t *testing.T) *Store {
	t.Helper()
	s, e := NewStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func create(t *testing.T, s *Store, key string) Run {
	t.Helper()
	r, e := s.Create(key, "test", "interactive", json.RawMessage(`{"hello":"world"}`))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestRoundTripIsolationAndRefusal(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_a")
	path := filepath.Join(s.root, "k_a", "state.json")
	before, _ := os.ReadFile(path)
	_, e := s.change("k_a", r.ID, func(v *Run) error { v.State = Done; return ErrConflict })
	if !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("refusal mutated file")
	}
	got, e := s.Get("k_b", r.ID)
	if !errors.Is(e, ErrNotFound) {
		t.Fatal(got, e)
	}
	got, e = s.Get("k_a", r.ID)
	if e != nil {
		t.Fatal(e)
	}
	got.Input[2] = 'X'
	got2, _ := s.Get("k_a", r.ID)
	if string(got.Input) == string(got2.Input) {
		t.Fatal("aliased read")
	}
	reopened, e := NewStore(filepath.Dir(s.root))
	if e != nil {
		t.Fatal(e)
	}
	got, e = reopened.Get("k_a", r.ID)
	if e != nil || got.ID != r.ID || string(got.Input) != `{"hello":"world"}` {
		t.Fatal(got, e)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}
func TestCapsCorruptionAndFailedCommit(t *testing.T) {
	s := store(t)
	for i := 0; i < MaxLiveKey; i++ {
		create(t, s, "k_a")
	}
	if _, e := s.Create("k_a", "test", "interactive", json.RawMessage(`{}`)); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	if _, e := s.Create("../escape", "test", "interactive", json.RawMessage(`{}`)); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	if _, e := s.Create("k_b", "test", "interactive", json.RawMessage(`"`+strings.Repeat("a", MaxInput)+`"`)); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	r := create(t, s, "k_b")
	s.write = func(string, []byte) error { return errors.New("disk unavailable") }
	if _, e := s.change("k_b", r.ID, func(v *Run) error { v.State = Done; return nil }); e == nil {
		t.Fatal("write accepted")
	}
	if _, e := s.Get("k_b", r.ID); e == nil {
		t.Fatal("did not fail closed")
	}
	os.WriteFile(filepath.Join(s.root, "k_a", "state.json"), []byte("broken"), 0600)
	reopened, e := NewStore(filepath.Dir(s.root))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = reopened.Get("k_a", "missing"); e == nil {
		t.Fatal("corrupt reset")
	}
}
func TestReplayResetAndSlowSubscriber(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_a")
	initial, ch, stop, e := s.Subscribe("k_a", "")
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	if len(initial) != 1 || !initial[0].Reset || len(initial[0].Runs) != 1 {
		t.Fatal(initial)
	}
	cursor := initial[0].Cursor
	_, e = s.change("k_a", r.ID, func(v *Run) error { v.State = Running; return nil })
	if e != nil {
		t.Fatal(e)
	}
	live := <-ch
	replay, _, stop2, e := s.Subscribe("k_a", cursor)
	if e != nil {
		t.Fatal(e)
	}
	if len(replay) != 1 || replay[0].Cursor != live.Cursor {
		t.Fatal(replay, live)
	}
	if _, _, _, e = s.Subscribe("k_a", ""); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	stop2()
	if _, _, _, e = s.Subscribe("k_b", live.Cursor); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	for i := 0; i < 260; i++ {
		if _, e = s.change("k_a", r.ID, func(v *Run) error { v.Updated = time.Now(); return nil }); e != nil {
			t.Fatal(e)
		}
	}
	for range ch {
	} // Overflow disconnects instead of blocking execution.
	replay, _, stop3, e := s.Subscribe("k_a", cursor)
	if e != nil {
		t.Fatal(e)
	}
	defer stop3()
	if !replay[0].Reset {
		t.Fatal("stale cursor not reset")
	}
}
func TestConcurrentCreateBound(t *testing.T) {
	s := store(t)
	var wg sync.WaitGroup
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.Create("k_a", "test", "interactive", json.RawMessage(`{}`)) }()
	}
	wg.Wait()
	rows, _ := s.List("k_a")
	if len(rows) != 16 {
		t.Fatal(len(rows))
	}
}
func TestReadRefusalCreatesNoKeyDirectory(t *testing.T) {
	s := store(t)
	_, err := s.Get("k_absent", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.root, "k_absent")); !os.IsNotExist(err) {
		t.Fatal("refused read created a directory")
	}
	outside := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(s.root, "k_link")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create("k_link", "test", "interactive", json.RawMessage(`{}`)); err == nil {
		t.Fatal("symlink accepted")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside store")
	}
}
func TestInvalidPersistedCursorFailsClosed(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_a")
	path := filepath.Join(s.root, "k_a", "state.json")
	raw, _ := os.ReadFile(path)
	var v snapshot
	json.Unmarshal(raw, &v)
	v.Events[0].Cursor = "malformed"
	raw, _ = json.Marshal(v)
	os.WriteFile(path, raw, 0600)
	reopened, e := NewStore(filepath.Dir(s.root))
	if e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = reopened.Subscribe(r.KeyID, ""); e == nil {
		t.Fatal("corrupt cursor accepted")
	}
}
func TestReturnedMutationCannotChangeStore(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_a")
	r.Input[2] = 'X'
	got, _ := s.Get(r.KeyID, r.ID)
	if string(got.Input) != `{"hello":"world"}` {
		t.Fatal("Create exposed stored input")
	}
	changed, err := s.change(r.KeyID, r.ID, func(v *Run) error {
		v.Attempts = []Attempt{{ID: "a_1", Output: json.RawMessage(`{"x":1}`)}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	changed.Attempts[0].Output[2] = 'z'
	got, _ = s.Get(r.KeyID, r.ID)
	if string(got.Attempts[0].Output) != `{"x":1}` {
		t.Fatal("change exposed stored attempt")
	}
}
func TestHostAndRetainedRunLimits(t *testing.T) {
	s := store(t)
	for key := 0; key < 4; key++ {
		for n := 0; n < 16; n++ {
			create(t, s, fmt.Sprintf("k_%d", key))
		}
	}
	if _, e := s.Create("k_other", "test", "interactive", json.RawMessage(`{}`)); !errors.Is(e, ErrLimit) {
		t.Fatal("host cap", e)
	}
	s = store(t)
	for n := 0; n < 100; n++ {
		r := create(t, s, "k_a")
		if _, e := s.change("k_a", r.ID, func(v *Run) error { v.State = Done; return nil }); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.Create("k_a", "test", "interactive", json.RawMessage(`{}`)); !errors.Is(e, ErrLimit) {
		t.Fatal("retained cap", e)
	}
}
