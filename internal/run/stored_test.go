package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoredMetadataAndExactCachedBytes(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Create("k_a", "image", "interactive", json.RawMessage(`{"prompt":"PRIVATE PROMPT"}`))
	if err != nil {
		t.Fatal(err)
	}
	output, err := s.PutImage("k_a", r.ID, []byte("test bytes"), "image/png", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.change("k_a", r.ID, func(r *Run) error { r.State = Done; r.Output = output; return nil }); err != nil {
		t.Fatal(err)
	}
	other, err := s.Create("k_b", "agent", "interactive", json.RawMessage(`{"secret":"OTHER KEY"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = other
	raw, err := os.ReadFile(filepath.Join(dir, "runs", "k_a", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, cold := range []bool{false, true} {
		if cold {
			s, err = NewStore(dir)
			if err != nil {
				t.Fatal(err)
			}
		}
		data, err := s.Stored("k_a")
		if err != nil {
			t.Fatal(err)
		}
		if data.Bytes != len(raw) || data.Total != 1 || data.Terminal != 1 || data.Images != 1 || data.ImageBytes != 10 || data.Budget != MaxStored-MaxLiveKey*terminalBound || data.ImageBudget != ImageBudget {
			t.Fatalf("%+v", data)
		}
		encoded, _ := json.Marshal(data)
		if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "OTHER KEY") {
			t.Fatal(string(encoded))
		}
		data.Runs[0].Image.Bytes = 999
		again, _ := s.Stored("k_a")
		if again.ImageBytes != 10 {
			t.Fatal("caller mutated cached metadata")
		}
	}
	s.now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	data, _ := s.Stored("k_a")
	if data.Images != 0 || data.ImageBytes != 0 || data.Runs[0].Image != nil {
		t.Fatal("expired image counted as servable")
	}
}
func TestStoredBoundedListAndLiveKinds(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	v, _ := s.load("k_a")
	for i := 0; i < MaxRuns; i++ {
		rid := id("r_")
		state := Done
		if i < 3 {
			state = Running
		}
		v.Runs[rid] = Run{ID: rid, KeyID: "k_a", Kind: "agent", State: state, Expires: time.Now().Add(time.Hour), Input: json.RawMessage(`{}`)}
	}
	if err := s.commit("k_a", v, nil); err != nil {
		t.Fatal(err)
	}
	data, err := s.Stored("k_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Runs) != 20 || !data.Truncated || data.Total != 100 || data.Terminal != 97 || data.Kinds["agent"].Live != 3 {
		t.Fatalf("%+v", data)
	}
}
func BenchmarkStored(b *testing.B) {
	dir := b.TempDir()
	s, _ := NewStore(dir)
	v, _ := s.load("k_a")
	for i := 0; i < MaxRuns; i++ {
		rid := id("r_")
		v.Runs[rid] = Run{ID: rid, KeyID: "k_a", Kind: "agent", State: Done, Expires: time.Now().Add(time.Hour), Input: json.RawMessage(`"` + strings.Repeat("x", 256<<10) + `"`)}
	}
	if err := s.commit("k_a", v, nil); err != nil {
		b.Fatal(err)
	}
	b.Run("warm_100_runs_25MiB", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := s.Stored("k_a"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cold_100_runs_25MiB", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cold, err := NewStore(dir)
			if err != nil {
				b.Fatal(err)
			}
			if _, err = cold.Stored("k_a"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestClearTerminalKeepsLiveAndOtherKeys(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	live, _ := s.Create("k_a", "agent", "interactive", json.RawMessage(`{"keep":"live input"}`))
	dead, _ := s.Create("k_a", "image", "interactive", json.RawMessage(`{"remove":"terminal prompt"}`))
	other, _ := s.Create("k_b", "agent", "interactive", json.RawMessage(`{"keep":"other key"}`))
	output, err := s.PutImage("k_a", dead.ID, []byte("image"), "image/png", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.change("k_a", dead.ID, func(r *Run) error { r.State = Done; r.Output = output; return nil })
	if err != nil {
		t.Fatal(err)
	}
	v := clone(s.data["k_a"])
	v.Retained = map[string]Retained{live.ID: {State: json.RawMessage(`{"keep":"live retained"}`)}, dead.ID: {State: json.RawMessage(`{"remove":"terminal retained"}`)}}
	if err = s.commit("k_a", v, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get("k_a", live.ID)
	rawBefore, _ := json.Marshal(before)
	if err = clearTest(s, "k_a"); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Get("k_a", live.ID)
	rawAfter, _ := json.Marshal(after)
	if string(rawBefore) != string(rawAfter) {
		t.Fatal("live run changed")
	}
	held, _ := s.Retained("k_a", live.ID)
	if !strings.Contains(string(held.State), "live retained") {
		t.Fatal("live retained data lost")
	}
	if _, err = s.Get("k_b", other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get("k_a", dead.ID); err != ErrNotFound {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.artifactPath("k_a", dead.ID)); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "runs", "k_a", "state.json"))
	if strings.Contains(string(raw), "terminal prompt") || strings.Contains(string(raw), "terminal retained") {
		t.Fatal("terminal content remains")
	}
	if err = clearTest(s, "k_a"); err != nil {
		t.Fatal("repeat clear", err)
	}
}
func TestClearCommitFailureNeverUnlinks(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	r, _ := s.Create("k_a", "image", "interactive", json.RawMessage(`{}`))
	out, _ := s.PutImage("k_a", r.ID, []byte("keep"), "image/png", 1, 1)
	s.change("k_a", r.ID, func(r *Run) error { r.State = Done; r.Output = out; return nil })
	s.write = func(string, []byte) error { return os.ErrPermission }
	if err := clearTest(s, "k_a"); err == nil {
		t.Fatal("expected commit failure")
	}
	if raw, err := os.ReadFile(s.artifactPath("k_a", r.ID)); err != nil || string(raw) != "keep" {
		t.Fatal("unlinked before commit", err)
	}
}
func TestClearTracksPerKeyCleanupAndRetriesWithoutLiveRemoval(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	r, _ := s.Create("k_a", "image", "interactive", json.RawMessage(`{}`))
	proof, _ := json.Marshal(ImageOutput{URL: "/v1/images/outputs/" + r.ID, Bytes: 1, ExpiresAt: time.Now().Add(time.Hour)})
	s.change("k_a", r.ID, func(r *Run) error { r.State = Done; r.Output = proof; return nil })
	path := s.artifactPath("k_a", r.ID)
	os.MkdirAll(path, 0700)
	os.WriteFile(filepath.Join(path, "occupied"), []byte("x"), 0600)
	s.imageCleanup = map[string]bool{s.artifactPath("k_b", "r_other"): true}
	s.Log = func(string, ...any) {}
	if err := clearTest(s, "k_a"); err != nil {
		t.Fatal(err)
	}
	data, err := s.Stored("k_a")
	if err != nil || data.Total != 0 || data.PendingCleanup == nil || *data.PendingCleanup != 1 {
		t.Fatalf("%+v %v", data, err)
	}
	os.Remove(filepath.Join(path, "occupied"))
	if err = clearTest(s, "k_a"); err != nil {
		t.Fatal(err)
	}
	data, _ = s.Stored("k_a")
	if *data.PendingCleanup != 0 || s.ImageCleanupPending() != 1 {
		t.Fatal("wrong key cleanup")
	}
}

func TestStoredCacheSurvivesCloneAndUnknownOrphanSurvivesClear(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	r, _ := s.Create("k_a", "image", "interactive", json.RawMessage(`{}`))
	s.change("k_a", r.ID, func(r *Run) error { r.State = Done; return nil })
	original := s.data["k_a"]
	copy := clone(original)
	if copy.encodedBytes != original.encodedBytes {
		t.Fatal("clone lost byte cache")
	}
	path := s.artifactPath("k_a", "r_unknown")
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("unknown orphan"), 0600)
	logged := false
	s.Log = func(string, ...any) { logged = true }
	if err := clearTest(s, "k_a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil || !logged {
		t.Fatal("orphan removed or not logged", err, logged)
	}
	if err := clearTest(s, "k_a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("empty snapshot authorized orphan deletion")
	}
}

func expectation(v StoredData) ClearExpectation {
	return ClearExpectation{v.Cursor, v.Terminal, v.ClearImages, v.RetryCleanup}
}
func clearTest(s *Store, key string) error {
	m := &Manager{Store: s, active: map[string]*execution{}}
	v, err := m.Store.Stored(key)
	if err != nil {
		return err
	}
	_, err = m.ClearTerminal(key, expectation(v))
	return err
}

func TestClearExcludesOutstandingTerminalWorkers(t *testing.T) {
	for _, expired := range []bool{false, true} {
		s, _ := NewStore(t.TempDir())
		r, _ := s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
		s.change("k_a", r.ID, func(r *Run) error { r.State = Cancelled; return nil })
		m := &Manager{Store: s, active: map[string]*execution{r.ID: {expired: expired}}}
		before, _ := m.Store.Stored("k_a")
		if before.Terminal != 1 {
			t.Fatal(before)
		}
		if result, err := m.ClearTerminal("k_a", expectation(before)); err != nil || result.Cleared != 0 || result.Skipped != 1 {
			t.Fatal(result, err)
		}
		if _, err := s.Get("k_a", r.ID); err != nil {
			t.Fatal("cleared outstanding worker", err)
		}
		m.finish("k_a", r.ID, Done, "", nil)
		if _, err := s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`)); err != nil {
			t.Fatal("settlement bricked key", err)
		}
		m.finish("k_a", "r_missing", Done, "", nil)
		if _, err := s.Stored("k_a"); err != nil {
			t.Fatal("missing settlement bricked key", err)
		}
	}
}
func TestClearResetAndConfirmationPrecondition(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	dead, _ := s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
	s.change("k_a", dead.ID, func(r *Run) error { r.State = Done; return nil })
	before, _ := s.Stored("k_a")
	live, _ := s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
	m := &Manager{Store: s, active: map[string]*execution{}}
	if _, err := m.ClearTerminal("k_a", expectation(before)); err != ErrConflict {
		t.Fatal("stale confirm accepted", err)
	}
	before, _ = m.Store.Stored("k_a")
	_, ch, stop, err := s.Subscribe("k_a", before.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	write := s.write
	s.write = func(string, []byte) error { return os.ErrPermission }
	if _, err = m.ClearTerminal("k_a", expectation(before)); err == nil {
		t.Fatal("failed commit accepted")
	}
	select {
	case e := <-ch:
		t.Fatal("published failed commit", e)
	default:
	}
	s.write = write
	delete(s.broken, "k_a") // End the injected I/O failure; retain the subscriber for success coverage.
	if _, err = m.ClearTerminal("k_a", expectation(before)); err != nil {
		t.Fatal(err)
	}
	e := <-ch
	if !e.Reset || e.Cursor == before.Cursor || len(e.Runs) != 1 || e.Runs[0].ID != live.ID {
		t.Fatal(e)
	}
	replay, _, stop2, err := s.Subscribe("k_a", before.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	defer stop2()
	if len(replay) != 1 || !replay[0].Reset || len(replay[0].Runs) != 1 {
		t.Fatal(replay)
	}
	if v := s.data["k_a"]; v.Seq != 0 || len(v.Events) != 0 {
		t.Fatal("clear grew replay ring")
	}
}
func TestClearCrashAfterCommitRecoversCleanupIntent(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	r, _ := s.Create("k_a", "image", "interactive", json.RawMessage(`{}`))
	out, _ := s.PutImage("k_a", r.ID, []byte("picture"), "image/png", 1, 1)
	s.change("k_a", r.ID, func(r *Run) error { r.State = Done; r.Output = out; return nil })
	orphan := s.artifactPath("k_a", "r_orphan")
	os.WriteFile(orphan, []byte("unknown"), 0600)
	write := s.write
	s.now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	s.write = func(path string, raw []byte) error {
		if err := write(path, raw); err != nil {
			return err
		}
		panic("crash after durable shrink")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("no crash")
			}
		}()
		_ = clearTest(s, "k_a")
	}()
	if _, err := os.Stat(s.artifactPath("k_a", r.ID)); err != nil {
		t.Fatal("unlink before crash", err)
	}
	cold, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := cold.Stored("k_a")
	if err != nil || data.Total != 0 || data.RetryCleanup != 1 {
		t.Fatal(data, err)
	}
	cold.Log = func(string, ...any) {}
	if err = cold.sweepImages("k_a", cold.data["k_a"]); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(cold.artifactPath("k_a", r.ID)); !os.IsNotExist(err) {
		t.Fatal("known intent not drained", err)
	}
	if _, err = os.Stat(orphan); err != nil {
		t.Fatal("unknown orphan removed", err)
	}
	again, _ := NewStore(dir)
	data, err = again.Stored("k_a")
	if err != nil || data.RetryCleanup != 0 || len(again.data["k_a"].Cleanup) != 0 {
		t.Fatal("intent retirement not durable", data, err)
	}
}

func BenchmarkStoredCleanupScale(b *testing.B) {
	for _, paths := range []int{0, 1000, 10000} {
		b.Run(fmt.Sprint(paths), func(b *testing.B) {
			s, _ := NewStore(b.TempDir())
			v, _ := s.load("k_a")
			for i := 0; i < MaxRuns; i++ {
				rid := id("r_")
				v.Runs[rid] = Run{ID: rid, KeyID: "k_a", Kind: "agent", State: Done, Input: json.RawMessage(`{}`)}
			}
			if err := s.commit("k_a", v, nil); err != nil {
				b.Fatal(err)
			}
			s.imageCleanup = map[string]bool{}
			for i := 0; i < paths; i++ {
				s.imageCleanup[s.artifactPath("k_other", fmt.Sprintf("r_%d", i))] = true
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Stored("k_a"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
func BenchmarkStoredCommit(b *testing.B) {
	s, _ := NewStore(b.TempDir())
	v, _ := s.load("k_a")
	for i := 0; i < MaxRuns; i++ {
		rid := id("r_")
		v.Runs[rid] = Run{ID: rid, KeyID: "k_a", Kind: "agent", State: Done, Input: json.RawMessage(`"` + strings.Repeat("x", 256<<10) + `"`)}
	}
	if err := s.commit("k_a", v, nil); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.commit("k_a", v, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestStoredReadOrderingExpiryAndEligibleCleanup(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	v, _ := s.load("k_a")
	now := time.Now().UTC()
	for i, rid := range []string{"r_old", "r_new", "r_large", "r_live"} {
		input := `{}`
		if rid == "r_large" {
			input = `"` + strings.Repeat("x", 1000) + `"`
		}
		state := Done
		if rid == "r_live" {
			state = Running
		}
		v.Runs[rid] = Run{ID: rid, KeyID: "k_a", Kind: "agent", State: state, Created: now.Add(time.Duration(i) * time.Second), Expires: now.Add(time.Duration(i+1) * time.Hour), Input: json.RawMessage(input)}
	}
	if err := s.commit("k_a", v, nil); err != nil {
		t.Fatal(err)
	}
	s.imageCleanup = map[string]bool{s.artifactPath("k_a", "r_live"): true, s.artifactPath("k_a", "r_old"): true, s.artifactPath("k_a", "r_missing"): true}
	m := &Manager{Store: s, active: map[string]*execution{"r_old": {}}}
	got, err := m.Store.Stored("k_a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Runs[0].ID != "r_large" || got.RetryCleanup != 2 || *got.PendingCleanup != 3 {
		t.Fatal(got)
	}
	if !got.Last.Equal(now.Add(3 * time.Hour)) {
		t.Fatal("live deadline included", got.Last)
	}
	for _, r := range got.Runs {
		if r.ID == "r_live" && r.Expires != nil {
			t.Fatal("live row expiry", r)
		}
	}
	if got.Budget != MaxStored-MaxLiveKey*terminalBound {
		t.Fatal(got.Budget)
	}
}

func TestClearEmptyDirectoryDoesNotLogAnOrphan(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	os.MkdirAll(filepath.Join(s.root, "k_a", "images"), 0700)
	calls := 0
	s.Log = func(string, ...any) { calls++ }
	if err := clearTest(s, "k_a"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("empty directory reported as orphan", calls)
	}
}
func TestStoredRejectsUnprovenCleanupIntentOnLoad(t *testing.T) {
	for _, rid := range []string{"../outside", "r_live"} {
		dir := t.TempDir()
		s, _ := NewStore(dir)
		v, _ := s.load("k_a")
		v.Runs["r_live"] = Run{ID: "r_live", KeyID: "k_a", Kind: "agent", State: Running}
		v.Cleanup = []string{rid}
		raw, _ := json.Marshal(v)
		path := filepath.Join(s.root, "k_a", "state.json")
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, raw, 0600)
		cold, _ := NewStore(dir)
		if _, err := cold.Stored("k_a"); err != ErrInvalid {
			t.Fatal(rid, err)
		}
	}
}

func TestStoredObservedOrphansNeverBecomeCleanupAuthority(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	v, _ := s.load("k_a")
	dir := filepath.Join(s.root, "k_a", "images")
	os.MkdirAll(dir, 0700)
	orphan := filepath.Join(dir, "r_unknown")
	os.WriteFile(orphan, []byte("untouched"), 0600)
	s.Log = func(string, ...any) {}
	if err := s.sweepImages("k_a", v); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Stored("k_a")
	if got.ObservedOrphans != 1 || *got.PendingCleanup != 0 || got.RetryCleanup != 0 || s.ImageCleanupPending() != 1 {
		t.Fatal(got)
	}
	if err := clearTest(s, "k_a"); err != nil {
		t.Fatal(err)
	}
	s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
	if err := s.sweepImages("k_a", s.data["k_a"]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("nonempty snapshot must collect unnamed files", err)
	}
	os.Remove(orphan)
	os.Remove(dir)
	if err := s.sweepImages("k_a", s.data["k_a"]); err != nil {
		t.Fatal("vanishing directory", err)
	}
	if s.ImageCleanupPending() != 0 {
		t.Fatal("stale observed count")
	}
}
func TestClearPrunesOnlyRemovedExceptionAllowances(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	a, _ := s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
	b, _ := s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
	s.change("k_a", a.ID, func(r *Run) error { r.State = Done; return nil })
	v := clone(s.data["k_a"])
	v.ExceptionBytes = map[string]int{a.ID: 1, b.ID: 2}
	if err := s.commit("k_a", v, nil); err != nil {
		t.Fatal(err)
	}
	if err := clearTest(s, "k_a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.data["k_a"].ExceptionBytes[a.ID]; ok || s.data["k_a"].ExceptionBytes[b.ID] != 2 {
		t.Fatal(s.data["k_a"].ExceptionBytes)
	}
}

func TestStoredSweepKeepsFailedProvenIntentWithLiveRuns(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	s.Log = func(string, ...any) {}
	s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
	r, _ := s.Create("k_a", "image", "interactive", json.RawMessage(`{}`))
	out, _ := json.Marshal(ImageOutput{URL: "/v1/images/outputs/" + r.ID, Bytes: 1, ExpiresAt: time.Now().Add(time.Hour)})
	s.change("k_a", r.ID, func(r *Run) error { r.State = Done; r.Output = out; return nil })
	path := s.artifactPath("k_a", r.ID)
	os.MkdirAll(path, 0700)
	os.WriteFile(filepath.Join(path, "occupied"), []byte("x"), 0600)
	if err := clearTest(s, "k_a"); err != nil {
		t.Fatal(err)
	}
	if err := s.sweepImages("k_a", s.data["k_a"]); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Stored("k_a")
	if got.RetryCleanup != 1 || *got.PendingCleanup != 1 || got.ObservedOrphans != 0 {
		t.Fatal(got)
	}
}

func TestExpiryCrashRetainsProvenCleanupIntent(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	r, _ := s.Create("k_a", "image", "interactive", json.RawMessage(`{}`))
	out, _ := s.PutImage("k_a", r.ID, []byte("expire"), "image/png", 1, 1)
	s.change("k_a", r.ID, func(r *Run) error { r.State = Done; r.Output = out; r.Expires = time.Now().Add(-time.Hour); return nil })
	write := s.write
	s.now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	s.write = func(path string, raw []byte) error {
		if err := write(path, raw); err != nil {
			return err
		}
		panic("crash after expiry shrink")
	}
	m := &Manager{Store: s, active: map[string]*execution{}}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("missing crash")
			}
		}()
		_ = m.Sweep()
	}()
	cold, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, err := cold.load("k_a")
	if err != nil || len(v.Cleanup) != 1 {
		t.Fatal(v, err)
	}
	if err = cold.sweepImages("k_a", v); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(cold.artifactPath("k_a", r.ID)); !os.IsNotExist(err) {
		t.Fatal("expiry intent not drained", err)
	}
	if len(cold.data["k_a"].Cleanup) != 0 {
		t.Fatal("intent not retired")
	}
}
func TestClearPostCommitFailureReportsCommittedOutcome(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	r, _ := s.Create("k_a", "image", "interactive", json.RawMessage(`{}`))
	out, _ := s.PutImage("k_a", r.ID, []byte("clear"), "image/png", 1, 1)
	s.change("k_a", r.ID, func(r *Run) error { r.State = Done; r.Output = out; return nil })
	m := &Manager{Store: s, active: map[string]*execution{}}
	v, _ := m.Store.Stored("k_a")
	write := s.write
	calls := 0
	s.write = func(path string, raw []byte) error {
		calls++
		if calls == 2 {
			return os.ErrPermission
		}
		return write(path, raw)
	}
	result, err := m.ClearTerminal("k_a", expectation(v))
	if err != nil || result.Cleared != 1 || !strings.Contains(result.Warning, "cleanup step failed") {
		t.Fatal(result, err)
	}
	raw, _ := os.ReadFile(filepath.Join(s.root, "k_a", "state.json"))
	var disk snapshot
	json.Unmarshal(raw, &disk)
	if len(disk.Runs) != 0 {
		t.Fatal("clear not committed")
	}
}
func TestOrphanAcrossTwoClearsRemainsReportOnly(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	r, _ := s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
	s.change("k_a", r.ID, func(r *Run) error { r.State = Done; return nil })
	path := s.artifactPath("k_a", "r_unknown")
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("unknown"), 0600)
	s.Log = func(string, ...any) {}
	for i := 0; i < 2; i++ {
		if err := clearTest(s, "k_a"); err != nil {
			t.Fatal(err)
		}
		if s.ImageCleanupPending() != 1 || len(s.imageCleanup) != 0 || len(s.data["k_a"].Cleanup) != 0 {
			t.Fatal("observation promoted or counted twice")
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestPutImageCommitFailureTracksOwnUnlinkFailure(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	r, _ := s.Create("k_a", "image", "interactive", json.RawMessage(`{}`))
	path := s.artifactPath("k_a", r.ID)
	s.Log = func(string, ...any) {}
	s.write = func(string, []byte) error {
		os.Remove(path)
		os.Mkdir(path, 0700)
		os.WriteFile(filepath.Join(path, "busy"), []byte("x"), 0600)
		return os.ErrPermission
	}
	if _, err := s.PutImage("k_a", r.ID, []byte("picture"), "image/png", 1, 1); err == nil {
		t.Fatal("expected refusal")
	}
	if !s.imageCleanup[path] || s.imageOrphans[path] {
		t.Fatal("own artifact did not retain proven cleanup authority")
	}
}
func BenchmarkStoredCeiling(b *testing.B) {
	dir := b.TempDir()
	s, _ := NewStore(dir)
	v, _ := s.load("k_a")
	for i := 0; i < MaxRuns; i++ {
		rid := id("r_")
		v.Runs[rid] = Run{ID: rid, KeyID: "k_a", Kind: "agent", State: Done, Input: json.RawMessage(`"` + strings.Repeat("x", 630000) + `"`)}
	}
	if err := s.commit("k_a", v, nil); err != nil {
		b.Fatal(err)
	}
	for _, cold := range []bool{false, true} {
		b.Run(fmt.Sprintf("cold_%v_63MB", cold), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				target := s
				if cold {
					target, _ = NewStore(dir)
				}
				if _, err := target.Stored("k_a"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestStoredReadDoesNotRefreshResidency(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
	old := time.Now().Add(-time.Hour)
	s.accessed["k_a"] = old
	if _, err := s.Stored("k_a"); err != nil {
		t.Fatal(err)
	}
	if !s.accessed["k_a"].Equal(old) {
		t.Fatal("stored read pinned residency")
	}
}
func TestSweepDoesNotResurrectRetiredIntentFromCallerClone(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	r, _ := s.Create("k_a", "image", "interactive", json.RawMessage(`{}`))
	out, _ := s.PutImage("k_a", r.ID, []byte("image"), "image/png", 1, 1)
	var image ImageOutput
	json.Unmarshal(out, &image)
	image.ExpiresAt = time.Now().Add(-time.Hour)
	out, _ = json.Marshal(image)
	v := clone(s.data["k_a"])
	row := v.Runs[r.ID]
	row.Output = out
	v.Runs[r.ID] = row
	v.Cleanup = []string{"r_already_deleted"}
	if err := s.commit("k_a", v, nil); err != nil {
		t.Fatal(err)
	}
	stale := clone(v)
	s.restoreCleanup("k_a", v)
	if err := s.sweepImages("k_a", stale); err != nil {
		t.Fatal(err)
	}
	if len(s.data["k_a"].Cleanup) != 0 {
		t.Fatal("retired intent resurrected")
	}
	current, _ := imageOutput(s.data["k_a"].Runs[r.ID])
	if !current.Gone {
		t.Fatal("expiry not processed")
	}
}

func TestImageCleanupCountsSeparateProvenAndObserved(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	s.imageCleanup = map[string]bool{"proven": true}
	s.imageOrphans = map[string]bool{"observed": true, "proven": true}
	proven, review := s.ImageCleanupCounts()
	if proven != 1 || review != 1 || s.ImageCleanupPending() != 2 {
		t.Fatal(proven, review, s.ImageCleanupPending())
	}
}

func TestAtomicImageTempCrashIsCollectedForEmptyAndLiveSnapshots(t *testing.T) {
	for _, live := range []bool{false, true} {
		dir := t.TempDir()
		s, _ := NewStore(dir)
		if live {
			s.Create("k_a", "image", "interactive", json.RawMessage(`{}`))
		}
		images := filepath.Join(s.root, "k_a", "images")
		os.MkdirAll(images, 0700)
		f, err := os.CreateTemp(images, ".run-*")
		if err != nil {
			t.Fatal(err)
		}
		f.Write(make([]byte, 8<<20))
		f.Sync()
		f.Close() // Death before atomicWrite's Rename.
		unknown := filepath.Join(images, "r_unknown")
		os.WriteFile(unknown, []byte("unknown"), 0600)
		cold, _ := NewStore(dir)
		cold.Log = func(string, ...any) {}
		v, _ := cold.load("k_a")
		for i := 0; i < 5; i++ {
			if err := cold.sweepImages("k_a", v); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
			t.Fatal("atomic temp survived restart sweeps", err)
		}
		_, err = os.Stat(unknown)
		if live && !os.IsNotExist(err) || !live && err != nil {
			t.Fatal("wrong orphan carve-out", live, err)
		}
	}
}
func TestCleanupOnlyClearPreservesCursorAndReportsRetry(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
	path := s.artifactPath("k_a", "r_deleted")
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("cleanup"), 0600)
	s.imageCleanup = map[string]bool{path: true}
	before, _ := s.Stored("k_a")
	_, ch, stop, _ := s.Subscribe("k_a", before.Cursor)
	defer stop()
	m := &Manager{Store: s, active: map[string]*execution{}}
	result, err := m.ClearTerminal("k_a", expectation(before))
	if err != nil || result.Cleared != 0 || result.Retried != 1 {
		t.Fatal(result, err)
	}
	after, _ := s.Stored("k_a")
	if before.Cursor != after.Cursor || after.RetryCleanup != 0 {
		t.Fatal(before, after)
	}
	select {
	case e := <-ch:
		t.Fatal("cleanup-only emitted event", e)
	default:
	}
	if _, err := m.ClearTerminal("k_a", expectation(after)); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ch:
		t.Fatal("no-op emitted event", e)
	default:
	}
}
func TestFailedImageScanPreservesReviewObservations(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	v, _ := s.load("k_a")
	dir := filepath.Join(s.root, "k_a", "images")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "r_unknown")
	os.WriteFile(path, []byte("x"), 0600)
	s.Log = func(string, ...any) {}
	s.sweepImages("k_a", v)
	os.Rename(dir, dir+"-saved")
	os.WriteFile(dir, []byte("not a directory"), 0600)
	if err := s.sweepImages("k_a", v); err == nil {
		t.Fatal("scan should fail")
	}
	_, review := s.ImageCleanupCounts()
	if review != 1 {
		t.Fatal("observation lost on failed scan", review)
	}
	os.Remove(dir)
	os.Rename(dir+"-saved", dir)
	os.Remove(path)
	if err := s.sweepImages("k_a", v); err != nil {
		t.Fatal(err)
	}
	_, review = s.ImageCleanupCounts()
	if review != 0 {
		t.Fatal("successful scan did not replace observations")
	}
}

func TestDeletedTerminalIDsRetainRenameCrashCleanupAuthority(t *testing.T) {
	for _, door := range []string{"clear", "expiry"} {
		for _, kind := range []string{"image", "agent"} {
			t.Run(door+"/"+kind, func(t *testing.T) {
				dir := t.TempDir()
				s, _ := NewStore(dir)
				r, _ := s.Create("k_a", kind, "interactive", json.RawMessage(`{}`))
				s.change("k_a", r.ID, func(r *Run) error {
					r.State = Failed
					r.Output = json.RawMessage(`{"error":"interrupted before output commit"}`)
					return nil
				})
				path := s.artifactPath("k_a", r.ID)
				os.MkdirAll(filepath.Dir(path), 0700)
				os.WriteFile(path, make([]byte, 8<<20), 0600)
				m := &Manager{Store: s, active: map[string]*execution{}}
				if door == "clear" {
					before, _ := s.Stored("k_a")
					if _, err := m.ClearTerminal("k_a", expectation(before)); err != nil {
						t.Fatal(err)
					}
				} else {
					s.now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
					if err := m.Sweep(); err != nil {
						t.Fatal(err)
					}
				}
				cold, _ := NewStore(dir)
				v, err := cold.load("k_a")
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 5; i++ {
					if err = cold.sweepImages("k_a", v); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("deleted record lost artifact authority", err)
				}
			})
		}
	}
}
func TestStateWriteTempsCollectedOnColdLoadAndSweep(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	s.Create("k_a", "agent", "interactive", json.RawMessage(`{}`))
	keydir := filepath.Join(s.root, "k_a")
	makeTemp := func() string {
		f, err := os.CreateTemp(keydir, ".run-*")
		if err != nil {
			t.Fatal(err)
		}
		f.Write(make([]byte, 1<<20))
		f.Sync()
		f.Close()
		return f.Name()
	}
	first := makeTemp()
	untouched := filepath.Join(keydir, "keep.txt")
	os.WriteFile(untouched, []byte("keep"), 0600)
	cold, _ := NewStore(dir)
	if _, err := cold.load("k_a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("cold load left state temp", err)
	}
	second := makeTemp()
	m := &Manager{Store: cold, active: map[string]*execution{}}
	if err := m.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatal("sweep left state temp", err)
	}
	if raw, err := os.ReadFile(untouched); err != nil || string(raw) != "keep" {
		t.Fatal("non-temp touched", err)
	}
}
