package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRetainedOwnerIsolationImmutabilityAndRestart(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_a")
	if e := s.Retain(r.KeyID, r.ID, json.RawMessage(`{}`), nil, nil); !errors.Is(e, ErrLimit) {
		t.Fatal("unadmitted ingestion", e)
	}
	release, err := s.Admit(r.KeyID, r.ID, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	state := json.RawMessage(`{"steps":[{"id":"s1"}]}`)
	outputs := map[string]Captured{"o_1": {Name: "notes.md", MIME: "text/markdown", Data: []byte("two lines\nhere\n")}}
	if err = s.Retain(r.KeyID, r.ID, state, []json.RawMessage{json.RawMessage(`{"seq":0,"type":"turn/start"}`)}, outputs); err != nil {
		t.Fatal(err)
	}
	state[2] = 'X'
	outputs["o_1"].Data[0] = 'X'
	got, err := s.Retained(r.KeyID, r.ID)
	if err != nil || string(got.State) != `{"steps":[{"id":"s1"}]}` || string(got.Outputs["o_1"].Data) != "two lines\nhere\n" {
		t.Fatal("input alias", got, err)
	}
	got.Outputs["o_1"].Data[0] = 'Y'
	if _, err = s.Retained("k_b", r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-key read", err)
	}
	before, _ := os.ReadFile(filepath.Join(s.root, r.KeyID, "state.json"))
	if err = s.Retain(r.KeyID, r.ID, nil, nil, outputs); !errors.Is(err, ErrConflict) {
		t.Fatal("output replacement", err)
	}
	after, _ := os.ReadFile(filepath.Join(s.root, r.KeyID, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("refusal changed retained data")
	}
	reopened, err := NewStore(filepath.Dir(s.root))
	if err != nil {
		t.Fatal(err)
	}
	got, err = reopened.Retained(r.KeyID, r.ID)
	if err != nil || len(got.Trajectory) != 1 || string(got.Outputs["o_1"].Data) != "two lines\nhere\n" {
		t.Fatal("restart lost retained data", got, err)
	}
	// The read is detached, and volatile reservations do not survive a restart.
	if _, err = reopened.Admit(r.KeyID, r.ID, 8192); err != nil {
		t.Fatal(err)
	}
}

func TestExpiryRemovesRetainedPayloadAndAdmission(t *testing.T) {
	s := store(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	r := create(t, s, "k_a")
	release, err := s.Admit(r.KeyID, r.ID, 40<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = s.Retain(r.KeyID, r.ID, nil, []json.RawMessage{json.RawMessage(`{"native":"retained-fixture"}`)}, map[string]Captured{"o_1": {Name: "notes.md", MIME: "text/plain", Data: []byte("retained-fixture")}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.change(r.KeyID, r.ID, func(v *Run) error { v.State = Done; return nil }); err != nil {
		t.Fatal(err)
	}
	now = now.Add(Retention + time.Second)
	m := manager(t, s, nil, nil)
	if err = m.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Retained(r.KeyID, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired data still reachable", err)
	}
	raw, _ := os.ReadFile(filepath.Join(s.root, r.KeyID, "state.json"))
	if bytes.Contains(raw, []byte("retained-fixture")) {
		t.Fatal("expired trajectory still retained")
	}
	next := create(t, s, r.KeyID)
	finish, err := s.Admit(next.KeyID, next.ID, 40<<20)
	if err != nil {
		t.Fatal("expired reservation still charged", err)
	}
	finish()
}

func TestConcurrentRetainedAdmissionCannotOvercommit(t *testing.T) {
	s := store(t)
	a, b := create(t, s, "k_a"), create(t, s, "k_a")
	var wg sync.WaitGroup
	results := make(chan func(), 2)
	for _, r := range []Run{a, b} {
		wg.Add(1)
		go func(r Run) {
			defer wg.Done()
			release, err := s.Admit(r.KeyID, r.ID, 40<<20)
			if err == nil {
				results <- release
			} else if !errors.Is(err, ErrLimit) {
				t.Error(err)
			}
		}(r)
	}
	wg.Wait()
	close(results)
	winners := 0
	for release := range results {
		winners++
		release()
		release()
	}
	if winners != 1 {
		t.Fatal("reservation overcommit", winners)
	}
	release, err := s.Admit(a.KeyID, a.ID, 40<<20)
	if err != nil {
		t.Fatal("release leaked capacity", err)
	}
	defer release()
	// Another run's ordinary ledger growth must also honor that reservation.
	_, err = s.change(b.KeyID, b.ID, func(v *Run) error {
		v.Output = json.RawMessage(`"` + string(bytes.Repeat([]byte("a"), 25<<20)) + `"`)
		return nil
	})
	if !errors.Is(err, ErrLimit) {
		t.Fatal("ledger spent reserved bytes", err)
	}
}

func TestRetainedBoundsAndFailedWriteNeverPublish(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_a")
	release, err := s.Admit(r.KeyID, r.ID, 2048)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = s.Retain(r.KeyID, r.ID, json.RawMessage(`"`+string(bytes.Repeat([]byte("a"), 2048))+`"`), nil, nil); !errors.Is(err, ErrLimit) {
		t.Fatal("exceeded reservation", err)
	}
	if err = s.Retain(r.KeyID, r.ID, json.RawMessage(`"`+string(bytes.Repeat([]byte("a"), MaxOutput))+`"`), nil, nil); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded frame", err)
	}
	if err = s.Retain(r.KeyID, r.ID, nil, []json.RawMessage{json.RawMessage(`broken`)}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("bad event accepted", err)
	}
	_, updates, stop, err := s.Subscribe(r.KeyID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	before, _ := os.ReadFile(filepath.Join(s.root, r.KeyID, "state.json"))
	s.write = func(string, []byte) error { return errors.New("disk unavailable") }
	if err = s.Retain(r.KeyID, r.ID, json.RawMessage(`{"step":1}`), nil, nil); err == nil {
		t.Fatal("accepted failed commit")
	}
	select {
	case <-updates:
		t.Fatal("published failed write")
	default:
	}
	after, _ := os.ReadFile(filepath.Join(s.root, r.KeyID, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("failed write replaced evidence")
	}
}

func Test116EDiffDescriptorRoundTrip(t *testing.T) {
	s := store(t)
	r := create(t, s, "k_diff")
	release, err := s.Admit(r.KeyID, r.ID, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	diff := Captured{Kind: "diff", Name: "Write diff", MIME: "text/x-diff", Data: []byte("--- before\n+++ after\n@@ -0,0 +1,1 @@\n+new\n")}
	step := StepEvent{ID: "write", Type: "step", At: time.Now().UTC(), Kind: "write", Status: "done", OutputID: "diff_write"}
	if err = s.Retain(r.KeyID, r.ID, nil, nil, map[string]Captured{"diff_write": diff}, step); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(filepath.Dir(s.root))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := reopened.Detail(r.KeyID, r.ID)
	if err != nil || len(detail.Outputs) != 1 || detail.Outputs[0].Kind != "diff" || detail.Outputs[0].MIME != "text/x-diff" || detail.Steps[0].OutputID != "diff_write" {
		t.Fatal(detail, err)
	}
	raw, _ := json.Marshal(detail.Outputs[0])
	if bytes.Contains(raw, []byte("content_type")) {
		t.Fatal("unruled alias", string(raw))
	}
	captured, err := reopened.CapturedOutput(r.KeyID, r.ID, "diff_write")
	if err != nil || !bytes.Equal(captured.Data, diff.Data) {
		t.Fatal(captured, err)
	}
	if _, err = reopened.CapturedOutput("other", r.ID, "diff_write"); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-key diff", err)
	}
	diff.Kind = "unknown"
	if err = s.Retain(r.KeyID, r.ID, nil, nil, map[string]Captured{"other": diff}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown descriptor kind", err)
	}
}
