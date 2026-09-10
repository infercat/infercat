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
	State      json.RawMessage     `json:"state,omitempty"`
	Trajectory []json.RawMessage   `json:"trajectory,omitempty"`
	Outputs    map[string]Captured `json:"outputs,omitempty"`
	Approval   *Approval           `json:"approval,omitempty"`
}
type Captured struct {
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
	bytes int
	note  bool
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
	if bytes <= 0 || bytes > MaxStored-MaxLiveKey*terminalBound {
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
	if len(raw)+s.reservedBytes(key)+bytes > MaxStored-MaxLiveKey*4096 {
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
	var lease *reservation
	credit := 0
	if rid != "" {
		lease = s.reserved[key][rid]
		if lease != nil {
			old, _ := json.Marshal(s.data[key])
			credit = min(lease.bytes, max(0, size-len(old)))
		}
	}
	if size+s.reservedBytes(key)-credit > MaxStored-MaxLiveKey*terminalBound {
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
// adds immutable output snapshots. The caller must reserve capacity first.
func (s *Store) Retain(key, rid string, state json.RawMessage, events []json.RawMessage, outputs map[string]Captured) error {
	input, err := json.Marshal(Retained{State: state, Trajectory: events, Outputs: outputs})
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
	state, events, outputs = detached.State, detached.Trajectory, detached.Outputs
	for id, o := range outputs {
		_, _, mimeErr := mime.ParseMediaType(o.MIME)
		if !safeID.MatchString(id) || o.Name == "" || o.Name == "." || o.Name == ".." || len(o.Name) > 255 || filepath.Base(o.Name) != o.Name || strings.ContainsAny(o.Name, "\\\r\n\x00") || mimeErr != nil {
			return ErrInvalid
		}
	}
	var note *reservation
	_, err = s.change(key, rid, func(r *Run) error {
		if r.CancelRequested {
			lease := s.reserved[key][rid]
			if lease == nil || lease.note || len(input) > terminalBound || len(outputs) > 0 {
				return ErrLimit
			}
			note = lease
		}
		if terminal(r.State) {
			return ErrConflict
		}
		return nil
	}, func(data *Retained) error {
		lease := s.reserved[key][rid]
		if lease == nil {
			return ErrLimit
		}
		old, _ := json.Marshal(data)
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
		next, _ := json.Marshal(data)
		// Includes room for the change's cursor, timestamps and envelope.
		if max(0, len(next)-len(old))+1024 > lease.bytes {
			return ErrLimit
		}
		if note != nil {
			note.note = true
		}
		return nil
	})
	if err != nil && note != nil {
		s.mu.Lock()
		note.note = false
		s.mu.Unlock()
	}
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
	return clone(v).Retained[rid], nil
}
