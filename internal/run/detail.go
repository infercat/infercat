package run

import (
	"encoding/json"
	"github.com/infercat/infercat/internal/usage"
)

type OutputInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int    `json:"size"`
}
type Detail struct {
	Run
	Steps    []StepEvent  `json:"steps"`
	Outputs  []OutputInfo `json:"outputs"`
	Approval *Approval    `json:"approval,omitempty"`
	Text     string       `json:"text,omitempty"`
}

// Detail copies metadata only: no native trajectory, captured bytes or bulk attempt bodies.
func (s *Store) Detail(key, rid string) (Detail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return Detail{}, err
	}
	r, ok := v.Runs[rid]
	if !ok {
		return Detail{}, ErrNotFound
	}
	r.Input = append(json.RawMessage(nil), r.Input...)
	r.Output = append(json.RawMessage(nil), r.Output...)
	if r.Started != nil {
		t := *r.Started
		r.Started = &t
	}
	if r.Batch != nil {
		b := *r.Batch
		r.Batch = &b
	}
	if r.Kind == "image" {
		r = copyRun(r)
	} else {
		r.Attempts = append([]Attempt{}, r.Attempts...)
		for i := range r.Attempts {
			a := &r.Attempts[i]
			a.Output = nil
			a.Usage.Prompt = ""
			a.Usage.Completion = ""
			a.Usage.Meters = append([]usage.Meter(nil), a.Usage.Meters...)
		}
	}
	held := v.Retained[rid]
	d := Detail{Run: r, Steps: append([]StepEvent{}, held.Steps...), Outputs: []OutputInfo{}}
	for id, o := range held.Outputs {
		d.Outputs = append(d.Outputs, OutputInfo{id, o.Name, o.MIME, len(o.Data)})
	}
	if held.Approval != nil && !(held.Approval.Status == "pending" && (terminal(r.State) || r.CancelRequested)) {
		a := *held.Approval
		if a.Allow != nil {
			b := *a.Allow
			a.Allow = &b
		}
		d.Approval = &a
	}
	var result struct{ Text string }
	_ = json.Unmarshal(r.Output, &result)
	d.Text = result.Text
	return d, nil
}

// CapturedOutput detaches only the requested output from the same retained owner.
func (s *Store) CapturedOutput(key, rid, oid string) (Captured, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return Captured{}, err
	}
	o, ok := v.Retained[rid].Outputs[oid]
	if !ok {
		return Captured{}, ErrNotFound
	}
	o.Data = append([]byte(nil), o.Data...)
	return o, nil
}
