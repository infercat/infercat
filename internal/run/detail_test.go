package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func Test116CDetailFatRunCostAndDetachment(t *testing.T) {
	for _, n := range []int{1, 10, 40, 68} {
		for _, textBytes := range []int{0, 1 << 20} {
			t.Run(fmt.Sprintf("%d-outputs-%d-text", n, textBytes), func(t *testing.T) {
				s := store(t)
				r := create(t, s, "key")
				v := s.data[r.KeyID]
				r.Kind = "agent"
				r.Attempts = []Attempt{{ID: "attempt", Output: json.RawMessage(`"` + strings.Repeat("a", 128<<10) + `"`)}}
				r.Attempts[0].Usage.Prompt = strings.Repeat("p", 128<<10)
				r.Attempts[0].Usage.Completion = strings.Repeat("c", 128<<10)
				v.Runs[r.ID] = r
				payload := bytes.Repeat([]byte("x"), 700<<10)
				held := Retained{Outputs: map[string]Captured{}, Steps: []StepEvent{{ID: "think", Type: "step", At: time.Now(), Kind: "think", Status: "done", Text: strings.Repeat("t", textBytes)}}}
				for i := range n {
					held.Outputs[fmt.Sprint(i)] = Captured{Name: "result", MIME: "text/plain", Data: payload}
				}
				v.Retained = map[string]Retained{r.ID: held}
				raw, err := json.Marshal(v)
				if err != nil || len(raw) > MaxStored {
					t.Fatal("fixture exceeds owner budget", len(raw), err)
				}
				began := time.Now()
				for range 100 {
					d, err := s.Detail(r.KeyID, r.ID)
					if err != nil || len(d.Outputs) != n || len(d.Attempts[0].Output) != 0 || d.Attempts[0].Usage.Prompt != "" || d.Attempts[0].Usage.Completion != "" {
						t.Fatal(d, err)
					}
				}
				detail := time.Since(began) / 100
				view, _ := s.Detail(r.KeyID, r.ID)
				began = time.Now()
				for range 20 {
					if _, err := json.Marshal(view); err != nil {
						t.Fatal(err)
					}
				}
				encode := time.Since(began) / 20
				began = time.Now()
				for range 20 {
					o, err := s.CapturedOutput(r.KeyID, r.ID, "0")
					if err != nil || len(o.Data) != 700<<10 {
						t.Fatal(err)
					}
				}
				output := time.Since(began) / 20
				d, _ := s.Detail(r.KeyID, r.ID)
				d.Steps[0].Text = "changed"
				d.Attempts[0].ID = "changed"
				o, _ := s.CapturedOutput(r.KeyID, r.ID, "0")
				o.Data[0] = 'z'
				if v.Retained[r.ID].Steps[0].Text == "changed" || v.Runs[r.ID].Attempts[0].ID == "changed" || v.Retained[r.ID].Outputs["0"].Data[0] != 'x' {
					t.Fatal("detached view mutated owner")
				}
				t.Logf("encoded=%d outputs=%d step_text=%d detail_mean=%v one_output_mean=%v encode_outside_lock=%v", len(raw), n, textBytes, detail, output, encode)
			})
		}
	}
}

func Test116CQueuedDetailKeepsEmptyAttemptArray(t *testing.T) {
	s := store(t)
	r := create(t, s, "key")
	d, err := s.Detail(r.KeyID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(d)
	if err != nil || !bytes.Contains(raw, []byte(`"attempts":[]`)) {
		t.Fatal(string(raw), err)
	}
}
