package run

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func Test157StoredImageSurvivesMinimalRecord(t *testing.T) {
	s := store(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	filler, err := s.Create("k_a", "test", "interactive", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	img, err := s.Create("k_a", "image", "interactive", json.RawMessage(`{"prompt":"a cat"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.change(filler.KeyID, filler.ID, func(v *Run) error { v.State = Done; return nil }); err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("p", 4096))
	meta, err := s.PutImage("k_a", img.ID, png, "image/png", 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("stored image metadata:", string(meta))
	if _, _, err = s.ReadImage("k_a", img.ID); err != nil {
		t.Fatal("image unreadable before terminal", err)
	}
	// The key's ledger is at the designed ordinary ceiling when the image job finishes.
	ceilingFixture(t, s, filler, MaxLiveKey*terminalBound)
	m := &Manager{Store: s, active: map[string]*execution{}}
	m.finish("k_a", img.ID, Done, "", meta)
	got, _ := s.Get("k_a", img.ID)
	t.Log("terminal image run:", got.State, string(got.Output))
	raw, out, readErr := s.ReadImage("k_a", img.ID)
	t.Log("ReadImage after terminal:", len(raw), out, readErr)
	if readErr != nil {
		t.Errorf("stored image lost its metadata on the terminal write: %v", readErr)
	}
	s.mu.Lock()
	err = s.sweepImages("k_a", s.data["k_a"])
	s.mu.Unlock()
	t.Log("sweepImages:", err)
	if _, e := os.Stat(s.artifactPath("k_a", img.ID)); e != nil {
		t.Errorf("image bytes deleted by the sweep after the ledger dropped the metadata: %v", e)
	}
}
