package run

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type snapshot struct {
	Epoch    string              `json:"epoch"`
	Seq      uint64              `json:"seq"`
	Runs     map[string]Run      `json:"runs"`
	Events   []Event             `json:"events"`
	Retained map[string]Retained `json:"retained,omitempty"`
}
type Store struct {
	mu          sync.Mutex
	root        string
	data        map[string]*snapshot
	broken      map[string]error
	subs        map[string]map[chan Event]bool
	now         func() time.Time
	write       func(string, []byte) error
	reserved    map[string]map[string]*reservation
	imageBudget int
	known       map[string]bool
	accessed    map[string]time.Time
	recovered   map[string]bool
	recovery    bool
	Log         func(string, ...any)
}

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func id(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}
func NewStore(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, ErrInvalid
	}
	s := &Store{root: filepath.Join(dataDir, "runs"), data: map[string]*snapshot{}, broken: map[string]error{}, subs: map[string]map[chan Event]bool{}, now: time.Now, write: atomicWrite, known: map[string]bool{}, accessed: map[string]time.Time{}, recovered: map[string]bool{}}
	if err := privateDir(s.root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if safeID.MatchString(e.Name()) {
			s.known[e.Name()] = true
		}
	}
	return s, nil
}
func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	i, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return ErrInvalid
	}
	return nil
}
func atomicWrite(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".run-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) load(key string) (*snapshot, error) {
	if !safeID.MatchString(key) {
		return nil, ErrInvalid
	}
	if err := s.broken[key]; err != nil {
		return nil, err
	}
	s.accessed[key] = s.now()
	if v := s.data[key]; v != nil {
		return v, nil
	}
	dir := filepath.Join(s.root, key)
	if info, err := os.Lstat(dir); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return nil, ErrInvalid
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	v := &snapshot{Epoch: id("e_"), Runs: map[string]Run{}}
	path := filepath.Join(dir, "state.json")
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return nil, ErrInvalid
		}
		f, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		raw, e := io.ReadAll(io.LimitReader(f, MaxStored+MaxRuns*terminalBound+1))
		f.Close()
		if e != nil {
			return nil, e
		}
		if len(raw) > MaxStored+MaxRuns*terminalBound {
			return nil, ErrLimit
		}
		if e = json.Unmarshal(raw, v); e != nil {
			return nil, e
		}
		if !safeID.MatchString(v.Epoch) || v.Runs == nil || len(v.Runs) > MaxRuns || len(v.Events) > 256 || v.Seq < uint64(len(v.Events)) {
			return nil, ErrInvalid
		}
		for i, event := range v.Events {
			want := fmt.Sprintf("%s:%d", v.Epoch, v.Seq-uint64(len(v.Events))+uint64(i)+1)
			if event.Cursor != want || !safeID.MatchString(event.RunID) || !validState(event.State) {
				return nil, ErrInvalid
			}
		}
		for rid, r := range v.Runs {
			if rid != r.ID || r.KeyID != key || !safeID.MatchString(rid) || !validState(r.State) {
				return nil, ErrInvalid
			}
		}
		for rid := range v.Retained {
			if _, ok := v.Runs[rid]; !ok {
				return nil, ErrInvalid
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	s.data[key] = v
	if s.recovery && !s.recovered[key] {
		if err := s.recoverKey(key); err != nil {
			return nil, err
		}
		v = s.data[key]
	}
	return v, nil
}
func validState(st State) bool { return st == Queued || st == Running || st == Waiting || terminal(st) }
func clone(v *snapshot) *snapshot {
	raw, _ := json.Marshal(v)
	var out snapshot
	_ = json.Unmarshal(raw, &out)
	return &out
}
func (s *Store) commit(key string, v *snapshot, event *Event) error {
	rid := ""
	if event != nil {
		rid = event.RunID
	}
	return s.commitFor(key, v, event, rid)
}
func (s *Store) commitFor(key string, v *snapshot, event *Event, rid string) error {
	if event != nil {
		if old, ok := s.data[key].Runs[event.RunID]; ok && old.State == event.State && old.CancelRequested == v.Runs[event.RunID].CancelRequested {
			event = nil
		}
	}
	if err := s.broken[key]; err != nil {
		return err
	}
	for rid := range v.Retained {
		if _, ok := v.Runs[rid]; !ok {
			delete(v.Retained, rid)
		}
	}
	if event != nil {
		v.Seq++
		event.Cursor = fmt.Sprintf("%s:%d", v.Epoch, v.Seq)
		v.Events = append(v.Events, *event)
		if len(v.Events) > 256 {
			v.Events = v.Events[len(v.Events)-256:]
		}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	undo, err := s.retainedBudget(key, v, rid, len(raw))
	if err != nil && rid != "" && terminal(v.Runs[rid].State) {
		old, _ := json.Marshal(s.data[key])
		r := v.Runs[rid]
		r.Output = json.RawMessage(`{"error":"output not retained: budget"}`)
		if len(r.Attempts) > 0 {
			a := &r.Attempts[len(r.Attempts)-1]
			a.Output = nil
			a.Usage.Prompt = ""
			a.Usage.Completion = ""
			a.Usage.Model = boundedReason(a.Usage.Model)
			a.Usage.Code = boundedReason(a.Usage.Code)
			a.Usage.Endpoint = boundedReason(a.Usage.Endpoint)
		}
		v.Runs[rid] = r
		raw, _ = json.Marshal(v)
		if len(raw)-len(old) <= terminalBound && len(raw) <= MaxStored+MaxRuns*terminalBound {
			err = nil
			undo = func() {}
		}
	}
	if err != nil {
		return err
	}
	defer func() {
		if s.data[key] != v {
			undo()
		}
	}()
	if err = privateDir(filepath.Join(s.root, key)); err != nil {
		s.markBroken(key, err)
		return err
	}
	if err = s.write(filepath.Join(s.root, key, "state.json"), raw); err != nil {
		s.markBroken(key, err)
		return err
	}
	s.data[key] = v
	s.known[key] = true
	// Reservations belong to live run identities, never expired retained files.
	for rid := range s.reserved[key] {
		if r, ok := v.Runs[rid]; !ok || terminal(r.State) {
			delete(s.reserved[key], rid)
		}
	}
	if event != nil {
		for ch := range s.subs[key] {
			select {
			case ch <- *event:
			default:
				close(ch)
				delete(s.subs[key], ch)
			}
		}
	}
	return nil
}
func (s *Store) change(key, rid string, fn func(*Run) error, retain ...func(*Retained) error) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return Run{}, err
	}
	v = clone(v)
	r, ok := v.Runs[rid]
	if !ok {
		return Run{}, ErrNotFound
	}
	if err = fn(&r); err != nil {
		return Run{}, err
	}
	for _, update := range retain {
		if v.Retained == nil {
			v.Retained = map[string]Retained{}
		}
		data := v.Retained[rid]
		if err = update(&data); err != nil {
			return Run{}, err
		}
		v.Retained[rid] = data
	}
	lifecycle := r.State != v.Runs[rid].State || r.CancelRequested != v.Runs[rid].CancelRequested
	if lifecycle {
		r.Updated = s.now().UTC()
	}
	if terminal(r.State) && !terminal(v.Runs[rid].State) {
		r.Expires = r.Updated.Add(Retention)
	}
	v.Runs[rid] = r
	ev := Event{RunID: rid, State: r.State, Time: r.Updated}
	if len(r.Attempts) > 0 {
		ev.AttemptID = r.Attempts[len(r.Attempts)-1].ID
	}
	var event *Event
	if lifecycle {
		event = &ev
	}
	err = s.commitFor(key, v, event, rid)
	return copyRun(v.Runs[rid]), err
}
func (s *Store) CreateBatch(key, kind, priority string, inputs []json.RawMessage, cap int) ([]Run, error) {
	if len(inputs) == 0 || len(inputs) > MaxLiveKey {
		return nil, ErrLimit
	}
	for _, input := range inputs {
		if len(input) > MaxInput {
			return nil, ErrLimit
		}
		if !json.Valid(input) || (priority != "interactive" && priority != "planted") {
			return nil, ErrInvalid
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return nil, err
	}
	keyLive, total, queued := 0, 0, 0
	for k, d := range s.data {
		if s.broken[k] != nil {
			continue
		}
		for _, r := range d.Runs {
			if !terminal(r.State) {
				total++
				if k == key {
					keyLive++
					if r.Kind == kind && r.State == Queued {
						queued++
					}
				}
			}
		}
	}
	if keyLive+len(inputs) > MaxLiveKey || total+len(inputs) > MaxLiveHost || len(v.Runs)+len(inputs) > MaxRuns {
		return nil, ErrLimit
	}
	if queued+len(inputs) > cap {
		return nil, ErrQueueLimit
	}
	next := clone(v)
	now := s.now().UTC()
	batchID := id("b_")
	rows := make([]Run, 0, len(inputs))
	for i, input := range inputs {
		r := Run{ID: id("r_"), KeyID: key, Kind: kind, Priority: priority, State: Queued, Created: now.Add(time.Duration(i)), Updated: now, Expires: now.Add(MaxAge), Input: append(json.RawMessage(nil), input...), Attempts: []Attempt{}}
		if kind == "image" {
			r.Batch = &Batch{batchID, i, len(inputs)}
		}
		next.Runs[r.ID] = r
		rows = append(rows, r)
	}
	// A batch has one durable admission point. Its event prompts the image list to refresh all siblings.
	if err = s.commit(key, next, &Event{RunID: rows[0].ID, State: Queued, Time: now}); err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i] = clone(next).Runs[rows[i].ID]
	}
	return rows, nil
}
func (s *Store) Create(key, kind, priority string, input json.RawMessage) (Run, error) {
	rows, err := s.CreateBatch(key, kind, priority, []json.RawMessage{input}, MaxLiveKey)
	if err != nil {
		return Run{}, err
	}
	return rows[0], nil
}

func (s *Store) Get(key, rid string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return Run{}, err
	}
	r, ok := v.Runs[rid]
	if !ok {
		return Run{}, ErrNotFound
	}
	return copyRun(r), nil
}
func (s *Store) List(key string) ([]Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list(key)
}
func (s *Store) list(key string) ([]Summary, error) {
	out := []Summary{}
	if key == "" {
		for k := range s.known {
			last := s.accessed[k]
			if _, err := s.load(k); err != nil {
				s.markBroken(k, err)
			}
			s.accessed[k] = last
		}
	}
	if key != "" {
		if _, err := s.load(key); err != nil {
			return nil, err
		}
	}
	for k, v := range s.data {
		if s.broken[k] != nil {
			continue
		}
		if key != "" && key != k {
			continue
		}
		for _, r := range v.Runs {
			out = append(out, summary(r))
		}
	}
	s.releaseIdle()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Subscribe takes the same lock as commit: replay and registration cannot leave a gap.
func (s *Store) Subscribe(key, cursor string) ([]Event, <-chan Event, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(s.subs[key]) >= 2 {
		return nil, nil, nil, ErrLimit
	}
	seq := uint64(0)
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 || parts[0] != v.Epoch {
			return nil, nil, nil, ErrInvalid
		}
		seq, err = strconv.ParseUint(parts[1], 10, 64)
		if err != nil || seq > v.Seq {
			return nil, nil, nil, ErrInvalid
		}
	}
	replay := []Event{}
	oldest := v.Seq - uint64(len(v.Events))
	if cursor == "" || seq < oldest {
		runs, _ := s.list(key)
		replay = append(replay, Event{Cursor: fmt.Sprintf("%s:%d", v.Epoch, v.Seq), Time: s.now().UTC(), Reset: true, Runs: runs})
	} else {
		for _, e := range v.Events {
			n, _ := strconv.ParseUint(strings.Split(e.Cursor, ":")[1], 10, 64)
			if n > seq {
				replay = append(replay, e)
			}
		}
	}
	ch := make(chan Event, 32)
	if s.subs[key] == nil {
		s.subs[key] = map[chan Event]bool{}
	}
	s.subs[key][ch] = true
	stop := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.subs[key][ch] {
			delete(s.subs[key], ch)
			close(ch)
		}
	}
	return replay, ch, stop, nil
}
