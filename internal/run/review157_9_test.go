package run

import (
	"encoding/json"
	"strings"
	"testing"
)

func Test157ArtifactReadyAndGonePublish(t *testing.T) {
	s := store(t)
	img, err := s.Create("k_a", "image", "interactive", json.RawMessage(`{"prompt":"a cat"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.change("k_a", img.ID, func(v *Run) error { v.State = Running; return nil }); err != nil {
		t.Fatal(err)
	}
	_, updates, stop, err := s.Subscribe("k_a", "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("p", 1024))
	if _, err = s.PutImage("k_a", img.ID, png, "image/png", 8, 8); err != nil {
		t.Fatal(err)
	}
	if err = s.DiscardImage("k_a", img.ID); err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		select {
		case <-updates:
			n++
			continue
		default:
		}
		break
	}
	t.Log("events published by PutImage + DiscardImage:", n)
	if n != 2 {
		t.Errorf("artifact-ready and artifact-gone events dropped: got %d of 2", n)
	}
}
