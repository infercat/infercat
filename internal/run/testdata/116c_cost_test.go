package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func Test116CCostExperiment(t *testing.T) {
	s := store(t)
	r := create(t, s, "key")
	r.Kind = "agent"
	s.data[r.KeyID].Runs[r.ID] = r
	payload := bytes.Repeat([]byte("x"), 700<<10)
	held := Retained{Outputs: map[string]Captured{}}
	for i := range 68 {
		held.Outputs[fmt.Sprint(i)] = Captured{Name: "result", MIME: "text/plain", Data: payload}
	}
	s.data[r.KeyID].Retained = map[string]Retained{r.ID: held}
	raw, _ := json.Marshal(s.data[r.KeyID])
	s.data[r.KeyID].encodedBytes = len(raw)
	for range 3 {
		start := time.Now()
		release, err := s.Admit(r.KeyID, r.ID, 8192)
		if err != nil {
			t.Fatal(err)
		}
		admit := time.Since(start)
		start = time.Now()
		err = s.Retain(r.KeyID, r.ID, nil, []json.RawMessage{json.RawMessage(`{"type":"cost-probe"}`)}, nil)
		elapsed := time.Since(start)
		release()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("encoded=%d admission=%v small_native_commit=%v", len(raw), admit, elapsed)
	}
}
