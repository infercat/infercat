package run

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func Test157SweepReleasePreservesImages(t *testing.T) {
	s := store(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	img, err := s.Create("k_a", "image", "interactive", json.RawMessage(`{"prompt":"a cat"}`))
	if err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("p", 4096))
	if _, err = s.PutImage("k_a", img.ID, png, "image/png", 8, 8); err != nil {
		t.Fatal(err)
	}
	if _, err = s.change("k_a", img.ID, func(v *Run) error { v.State = Done; return nil }); err != nil {
		t.Fatal(err)
	}
	now = now.Add(idleRelease + time.Minute) // the key has been idle for five minutes
	m := &Manager{Store: s, active: map[string]*execution{}}
	path := s.artifactPath("k_a", img.ID)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // a friend polling their image job
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = s.List("k_other")
		}
	}()
	hit := -1
	for i := 0; i < 500; i++ {
		if err = m.Sweep(); err != nil {
			t.Fatal(err)
		}
		if _, e := os.Stat(path); e != nil {
			hit = i
			break
		}
	}
	close(stop)
	wg.Wait()
	if hit >= 0 {
		raw, out, readErr := s.ReadImage("k_a", img.ID)
		t.Errorf("sweep iteration %d deleted the artifact of a healthy key; ledger still advertises %q, readable bytes %d (%v)", hit, out.URL, len(raw), readErr)
	}
}
