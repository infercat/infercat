package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func imageIn(prompt string) json.RawMessage {
	b, _ := json.Marshal(ImageInput{Prompt: prompt})
	return b
}
func untilImage(t *testing.T, s *Store, key, rid string, state State) Run {
	t.Helper()
	var r Run
	if awaitFor(t, 3*time.Second, "", func() bool {
		var e error
		r, e = s.Get(key, rid)
		return e == nil && r.State == state
	}) {
		return r
	}
	r, _ = s.Get(key, rid)
	t.Fatalf("wanted %s: %+v", state, r)
	return r
}
func TestImageWorkerPriorityAndDeferredCancel(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	started := make(chan string, 4)
	release := make(chan struct{}, 4)
	var mu sync.Mutex
	active, peak := 0, 0
	exec := func(ctx context.Context, _ string, step Step, acquired func() error) (StepResult, error) {
		if err := acquired(); err != nil {
			return StepResult{}, err
		}
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		started <- step.RunID
		select {
		case <-release:
		case <-ctx.Done():
			return StepResult{}, ctx.Err()
		}
		mu.Lock()
		active--
		mu.Unlock()
		return StepResult{Output: json.RawMessage(`{"url":"image"}`), Dispatched: true, Settled: true}, nil
	}
	m, _ := New(s, exec, nil)
	m.Register("image", ImageKind, Policy{Serial: true, DeferredCancel: true, Validate: ValidateImage})
	defer m.Close()
	first, _ := m.Submit("key", "image", "planted", imageIn("first"))
	if <-started != first.ID {
		t.Fatal("first")
	}
	planted, _ := m.Submit("key", "image", "planted", imageIn("planted"))
	interactive, _ := m.Submit("key", "image", "interactive", imageIn("interactive"))
	if m.Position("image", interactive.ID) != 1 {
		t.Fatal("interactive not first")
	}
	r, e := m.Cancel("key", first.ID)
	if e != nil || r.State != Running || !r.CancelRequested {
		t.Fatal(r, e)
	}
	release <- struct{}{}
	untilImage(t, s, "key", first.ID, Done)
	if <-started != interactive.ID {
		t.Fatal("priority")
	}
	if _, e = m.Cancel("key", planted.ID); e != nil {
		t.Fatal(e)
	}
	untilImage(t, s, "key", planted.ID, Cancelled)
	release <- struct{}{}
	untilImage(t, s, "key", interactive.ID, Done)
	if peak != 1 {
		t.Fatal("parallel images", peak)
	}
}
func TestImageBatchCapIsAtomic(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	rows, e := s.createBatch("key", "image", "planted", []json.RawMessage{imageIn("a"), imageIn("b")}, 2, nil)
	if e != nil || len(rows) != 2 || rows[0].Batch.ID != rows[1].Batch.ID || rows[1].Batch.Index != 1 {
		t.Fatal(rows, e)
	}
	if _, e = s.createBatch("key", "image", "planted", []json.RawMessage{imageIn("c"), imageIn("d")}, 2, nil); !errors.Is(e, ErrQueueLimit) {
		t.Fatal(e)
	}
	all, _ := s.List("key")
	if len(all) != 2 {
		t.Fatal("partial admission")
	}
	rows[0].Input[0] = 'x'
	r, _ := s.Get("key", rows[0].ID)
	if !json.Valid(r.Input) {
		t.Fatal("aliased batch")
	}
}
func TestImageArtifactsEvictDiscardExpireAndIsolate(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	s.imageBudget = 8
	makeImage := func(name string) Run {
		r, e := s.Create("key", "image", "interactive", imageIn(name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.PutImage("key", r.ID, []byte("four"), "image/png", 1, 1); e != nil {
			t.Fatal(e)
		}
		return r
	}
	a := makeImage("a")
	b := makeImage("b")
	c := makeImage("c")
	if _, o, e := s.ReadImage("key", a.ID); !errors.Is(e, ErrNotFound) || !o.Gone {
		t.Fatal("oldest not evicted", o, e)
	}
	if _, _, e := s.ReadImage("other", b.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross key", e)
	}
	if e := s.DiscardImage("key", b.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.DiscardImage("key", b.ID); e != nil {
		t.Fatal("discard not idempotent", e)
	}
	if _, _, e := s.ReadImage("key", c.ID); e != nil {
		t.Fatal(e)
	}
	_ = os.Remove(s.artifactPath("key", c.ID))
	_ = os.Symlink("/etc/hosts", s.artifactPath("key", c.ID))
	if _, _, e := s.ReadImage("key", c.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("symlink read", e)
	}
	s.now = func() time.Time { return time.Now().Add(Retention + time.Hour) }
	if _, _, e := s.ReadImage("key", c.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("expired read", e)
	}
	if _, e := s.PutImage("key", c.ID, make([]byte, MaxImage+1), "image/png", 1, 1); !errors.Is(e, ErrLimit) {
		t.Fatal("artifact cap", e)
	}
}

func TestImageExpiryRetainsGoneMetadataAndRestartDoesNotReplay(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	now := time.Now()
	s.now = func() time.Time { return now }
	r, _ := s.Create("key", "image", "interactive", imageIn("one"))
	meta, e := s.PutImage("key", r.ID, []byte("png"), "image/png", 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(time.Hour)
	_, e = s.change("key", r.ID, func(v *Run) error { v.State = Done; v.Output = meta; return nil })
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(Retention - time.Minute)
	m, _ := New(s, nil, nil)
	defer m.Close()
	if e = m.Sweep(); e != nil {
		t.Fatal(e)
	}
	kept, e := s.Get("key", r.ID)
	o, ok := imageOutput(kept)
	if e != nil || !ok || !o.Gone {
		t.Fatal(kept, e)
	}
	if _, e = os.Stat(s.artifactPath("key", r.ID)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	live, _ := s.Create("key", "image", "planted", imageIn("never replay"))
	s2, e := NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	replayed := false
	m2, e := New(s2, func(context.Context, string, Step, func() error) (StepResult, error) {
		replayed = true
		return StepResult{}, nil
	}, map[string]Kind{"image": ImageKind})
	if e != nil {
		t.Fatal(e)
	}
	defer m2.Close()
	got, _ := s2.Get("key", live.ID)
	if got.State != Failed || replayed {
		t.Fatal(got, replayed)
	}
}

func TestDiscardBeforeStepFinishDoesNotResurrectMetadata(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	r, _ := s.Create("key", "image", "interactive", imageIn("one"))
	meta, e := s.PutImage("key", r.ID, []byte("png"), "image/png", 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DiscardImage("key", r.ID); e != nil {
		t.Fatal(e)
	}
	m := &Manager{Store: s}
	m.finish("key", r.ID, Done, "", meta)
	r, _ = s.Get("key", r.ID)
	o, ok := imageOutput(r)
	if !ok || !o.Gone || r.State != Done {
		t.Fatal(r)
	}
}

func TestImageCancelAfterOutputBeforeFinalSnapshot(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	ready, finish := make(chan struct{}), make(chan struct{})
	m, _ := New(s, func(_ context.Context, key string, step Step, acquired func() error) (StepResult, error) {
		if e := acquired(); e != nil {
			return StepResult{}, e
		}
		output, e := s.PutImage(key, step.RunID, []byte("image"), "image/png", 1, 1)
		return StepResult{Output: output, Dispatched: true, Settled: true}, e
	}, nil)
	m.Register("image", func(ctx context.Context, r Run) (Decision, error) {
		if len(r.Attempts) > 0 {
			close(ready)
			<-finish
		}
		return ImageKind(ctx, r)
	}, Policy{Serial: true, DeferredCancel: true})
	defer m.Close()
	defer close(finish)
	r, e := m.Submit("key", "image", "interactive", imageIn("one"))
	if e != nil {
		t.Fatal(e)
	}
	<-ready
	r, e = m.Cancel("key", r.ID)
	if e != nil || r.State != Running || !r.CancelRequested {
		t.Fatalf("completed image reverted: %+v %v", r, e)
	}
	finish <- struct{}{}
	untilImage(t, s, "key", r.ID, Done)
}

func TestUnlinkFailureKeepsKeyUsableAndRetries(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	s.imageBudget = 4
	a, _ := s.Create("key", "image", "interactive", imageIn("first"))
	if _, e := s.PutImage("key", a.ID, []byte("four"), "image/png", 1, 1); e != nil {
		t.Fatal(e)
	}
	path := s.artifactPath("key", a.ID)
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path+"/held", []byte("busy"), 0600); e != nil {
		t.Fatal(e)
	}
	b, _ := s.Create("key", "image", "interactive", imageIn("second"))
	if _, e := s.PutImage("key", b.ID, []byte("four"), "image/png", 1, 1); e != nil {
		t.Fatal("stale unlink refused a new image", e)
	}
	r, e := s.Get("key", a.ID)
	if e != nil {
		t.Fatal("key bricked", e)
	}
	if s.ImageCleanupPending() != 1 {
		t.Fatal("missing pending cleanup count")
	}
	o, _ := imageOutput(r)
	if !o.Gone {
		t.Fatal("old output still servable")
	}
	if e = s.DiscardImage("key", a.ID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.ReadImage("key", b.ID); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path + "/held"); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	e = s.sweepImages("key", s.data["key"])
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("cleanup not retried", e)
	}
	if s.ImageCleanupPending() != 0 {
		t.Fatal("stale cleanup count")
	}
}

func TestImageAdmissionRollsBackFailedCommit(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	held := 0
	s.write = func(string, []byte) error { return errors.New("disk refusal") }
	_, e := s.createBatch("key", "image", "interactive", []json.RawMessage{imageIn("a"), imageIn("b")}, 8, nil, func(rows []Run) (func(), error) {
		held += len(rows)
		return func() { held -= len(rows) }, nil
	})
	if e == nil || held != 0 {
		t.Fatal("failed commit retained image reservations", e, held)
	}
}
func TestAdmissionResolvesOutsideManagerLock(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	m, _ := New(s, nil, nil)
	defer m.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	_ = m.Register("image", ImageKind, Policy{Admission: func(context.Context, string) (BatchAdmission, error) {
		close(entered)
		<-release
		return BatchAdmission{}, errors.New("refused")
	}})
	done := make(chan struct{})
	go func() { defer close(done); _, _ = m.Submit("key", "image", "interactive", imageIn("one")) }()
	<-entered
	free := m.mu.TryLock()
	if free {
		m.mu.Unlock()
	}
	close(release)
	<-done
	if !free {
		t.Fatal("admission lookup held the manager mutex")
	}
}

func TestImagePositionIsCachedAndTracksQueueChanges(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	defer close(release)
	m, _ := New(s, func(ctx context.Context, _ string, step Step, acquired func() error) (StepResult, error) {
		if e := acquired(); e != nil {
			return StepResult{}, e
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return StepResult{}, ctx.Err()
		}
		return StepResult{Output: json.RawMessage(`{}`), Settled: true}, nil
	}, nil)
	defer m.Close()
	m.Register("image", ImageKind, Policy{Serial: true, DeferredCancel: true})
	first, _ := m.Submit("key", "image", "interactive", imageIn("first"))
	<-entered
	second, _ := m.Submit("key", "image", "interactive", imageIn("second"))
	third, _ := m.Submit("key", "image", "interactive", imageIn("third"))
	s.mu.Lock()
	result := make(chan int, 1)
	go func() { result <- m.Position("image", second.ID) }()
	var rank int
	select {
	case rank = <-result:
	case <-time.After(time.Second):
		s.mu.Unlock()
		t.Fatal("Position read the store")
	}
	s.mu.Unlock()
	if rank != 1 || m.Position("image", first.ID) != 0 || m.Position("image", third.ID) != 2 {
		t.Fatal("wrong queue snapshot", rank)
	}
	m.Cancel("key", second.ID)
	if m.Position("image", second.ID) != 0 || m.Position("image", third.ID) != 1 {
		t.Fatal("cancel did not refresh ranks")
	}
	release <- struct{}{}
	<-entered
	if m.Position("image", third.ID) != 0 {
		t.Fatal("running image kept a queue rank")
	}
	release <- struct{}{}
	untilImage(t, s, "key", third.ID, Done)
}

func TestImageKeyChecksAndDailyReservePrecedeHostCapacity(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	own, e := s.Create("key", "image", "interactive", imageIn("existing"))
	if e != nil {
		t.Fatal(e)
	}
	// Other hosts' retained state is full; per-key refusals must still win.
	s.data["other"] = &snapshot{Runs: map[string]Run{}}
	for i := 0; i < MaxLiveHost; i++ {
		s.data["other"].Runs[fmt.Sprint(i)] = Run{State: Queued}
	}
	daily := errors.New("daily budget exhausted")
	calls, refunds := 0, 0
	reserve := func([]Run) (func(), error) { calls++; return nil, daily }
	if _, e = s.createBatch("key", "image", "interactive", []json.RawMessage{imageIn("refused")}, 1, nil, reserve); !errors.Is(e, ErrQueueLimit) || calls != 0 {
		t.Fatal(e, calls)
	}
	if _, e = s.createBatch("key", "image", "interactive", []json.RawMessage{imageIn("refused")}, 8, nil, reserve); !errors.Is(e, daily) || calls != 1 {
		t.Fatal(e, calls)
	}
	reserve = func([]Run) (func(), error) { calls++; return func() { refunds++ }, nil }
	if _, e = s.createBatch("key", "image", "interactive", []json.RawMessage{imageIn("host full")}, 8, nil, reserve); !errors.Is(e, ErrLimit) || refunds != 1 {
		t.Fatal(e, refunds)
	}
	if len(s.data["key"].Runs) != 1 || s.data["key"].Runs[own.ID].State != Queued {
		t.Fatal("refusal mutated key")
	}
}
func TestImageQueueLimitDoesNotCountOtherKinds(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	for range 15 {
		if _, e := s.Create("key", "agent", "interactive", json.RawMessage(`{}`)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.createBatch("key", "image", "interactive", []json.RawMessage{imageIn("one"), imageIn("two")}, 8, nil); !errors.Is(e, ErrLimit) || errors.Is(e, ErrQueueLimit) {
		t.Fatal(e)
	}
}
