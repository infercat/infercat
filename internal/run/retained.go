package run

import (
	"encoding/json"
	"mime"
	"path/filepath"
	"strings"
)

// Retained is the sole durable owner of consumer state, native trajectory and
// captured bytes. It lives in the same capped per-key snapshot as the run ledger.
type Retained struct {
	Steps      []StepEvent         `json:"steps,omitempty"`
	State      json.RawMessage     `json:"state,omitempty"`
	Trajectory []json.RawMessage   `json:"trajectory,omitempty"`
	Outputs    map[string]Captured `json:"outputs,omitempty"`
	CancelNote bool                `json:"cancel_note,omitempty"`
	Approval   *Approval           `json:"approval,omitempty"`
}
type Captured struct {
	Kind string `json:"kind,omitempty"`
	Name string `json:"name"`
	MIME string `json:"mime"`
	Data []byte `json:"data"`
}
type Approval struct {
	ID      string `json:"id"`
	Request string `json:"request"`
	Status  string `json:"status"`
	Allow   *bool  `json:"allow,omitempty"`
}
type reservation struct {
	late  bool // Only the returning in-process owner may release this lease.
	bytes int
}

// Admit reserves encoded snapshot capacity before a consumer enters another
// model/tool step. Reservations are volatile: restart fails live runs, never replays.
// Leave room for control/settlement records even when payload admission refuses.
func (s *Store) Admit(key, rid string, bytes int) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return nil, err
	}
	r, ok := v.Runs[rid]
	if !ok {
		return nil, ErrNotFound
	}
	if terminal(r.State) || r.CancelRequested {
		return nil, ErrConflict
	}
	if bytes <= 0 || bytes > s.maxStored-MaxLiveKey*terminalBound {
		return nil, ErrLimit
	}
	if s.reserved == nil {
		s.reserved = map[string]map[string]*reservation{}
	}
	if s.reserved[key] == nil {
		s.reserved[key] = map[string]*reservation{}
	}
	if s.reserved[key][rid] != nil {
		return nil, ErrConflict
	}
	raw, _ := json.Marshal(v)
	if len(raw)+s.reservedBytes(key)+bytes > s.maxStored-MaxLiveKey*4096 {
		return nil, ErrLimit
	}
	lease := &reservation{bytes: bytes}
	s.reserved[key][rid] = lease
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.reserved[key][rid] == lease {
			delete(s.reserved[key], rid)
		}
	}, nil
}
func (s *Store) reservedBytes(key string) int {
	total := 0
	for _, r := range s.reserved[key] {
		total += r.bytes
	}
	return total
}

// Every ledger commit participates, so another run cannot spend reserved capacity.
func (s *Store) retainedBudget(key string, next *snapshot, rid string, size int) (func(), error) {
	if size < s.data[key].encodedBytes {
		return func() {}, nil
	}
	var lease *reservation
	credit := 0
	if rid != "" {
		lease = s.reserved[key][rid]
		if lease != nil {
			old, _ := json.Marshal(s.data[key])
			credit = min(lease.bytes, max(0, size-len(old)))
		}
	}
	if size+s.reservedBytes(key)-credit > s.maxStored-MaxLiveKey*terminalBound {
		return nil, ErrLimit
	}
	if lease != nil {
		lease.bytes -= credit
	}
	return func() {
		if lease != nil {
			lease.bytes += credit
		}
	}, nil
}

// Retain atomically replaces opaque step state, appends exact native events and
// adds immutable output snapshots. Capacity must be reserved except for the one
// bounded post-cancel note, which still spends the ordinary snapshot budget.
func (s *Store) Retain(key, rid string, state json.RawMessage, events []json.RawMessage, outputs map[string]Captured, steps ...StepEvent) error {
	if len(steps) > 1 {
		return ErrInvalid
	}
	input, err := json.Marshal(Retained{State: state, Trajectory: events, Outputs: outputs, Steps: steps})
	if err != nil {
		return ErrInvalid
	}
	if len(input) > MaxOutput {
		return ErrLimit
	}
	var detached Retained
	if err = json.Unmarshal(input, &detached); err != nil {
		return ErrInvalid
	}
	state, events, outputs, steps = detached.State, detached.Trajectory, detached.Outputs, detached.Steps
	for _, step := range steps {
		if !step.Valid() {
			return ErrInvalid
		}
	}
	for id, o := range outputs {
		_, _, mimeErr := mime.ParseMediaType(o.MIME)
		if (o.Kind != "" && (o.Kind != "diff" || o.MIME != "text/x-diff")) || !safeID.MatchString(id) || o.Name == "" || o.Name == "." || o.Name == ".." || len(o.Name) > 255 || filepath.Base(o.Name) != o.Name || strings.ContainsAny(o.Name, "\\\r\n\x00") || mimeErr != nil {
			return ErrInvalid
		}
	}
	note := false
	_, err = s.change(key, rid, func(r *Run) error {
		if r.CancelRequested {
			if len(input) > terminalBound || len(outputs) > 0 {
				return ErrLimit
			}
			note = true
		}
		if terminal(r.State) {
			return ErrConflict
		}
		return nil
	}, func(data *Retained) error {
		lease := s.reserved[key][rid]
		if (lease == nil && !note) || (note && data.CancelNote) {
			return ErrLimit
		}
		old, _ := json.Marshal(data)
		if note {
			data.CancelNote = true
		}
		if state != nil {
			data.State = state
		}
		data.Trajectory = append(data.Trajectory, events...)
		if data.Outputs == nil {
			data.Outputs = map[string]Captured{}
		}
		for id, output := range outputs {
			if _, exists := data.Outputs[id]; exists {
				return ErrConflict
			}
			data.Outputs[id] = output
		}
		for _, step := range steps {
			found := false
			for i, old := range data.Steps {
				if old.ID == step.ID {
					if !old.At.Equal(step.At) {
						return ErrConflict
					}
					data.Steps[i] = step
					found = true
					break
				}
			}
			if !found {
				data.Steps = append(data.Steps, step)
			}
		}
		next, _ := json.Marshal(data)
		// Includes room for the change's cursor, timestamps and envelope.
		if lease != nil && max(0, len(next)-len(old))+1024 > lease.bytes {
			return ErrLimit
		}
		return nil
	})
	return err
}
func (s *Store) Retained(key, rid string) (Retained, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return Retained{}, err
	}
	if _, ok := v.Runs[rid]; !ok {
		return Retained{}, ErrNotFound
	}
	raw, err := json.Marshal(v.Retained[rid])
	if err != nil {
		return Retained{}, err
	}
	var out Retained
	err = json.Unmarshal(raw, &out)
	return out, err
}
