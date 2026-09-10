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
	Epoch  string         `json:"epoch"`
	Seq    uint64         `json:"seq"`
	Runs   map[string]Run `json:"runs"`
	Events []Event        `json:"events"`
}
type Store struct {
	mu     sync.Mutex
	root   string
	data   map[string]*snapshot
	broken map[string]error
	subs   map[string]map[chan Event]bool
	now    func() time.Time
	write  func(string, []byte) error
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
	s := &Store{root: filepath.Join(dataDir, "runs"), data: map[string]*snapshot{}, broken: map[string]error{}, subs: map[string]map[chan Event]bool{}, now: time.Now, write: atomicWrite}
	if err := privateDir(s.root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if safeID.MatchString(e.Name()) {
			_, err = s.load(e.Name())
			if err != nil {
				s.broken[e.Name()] = err
			}
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
		raw, e := io.ReadAll(io.LimitReader(f, MaxStored+1))
		f.Close()
		if e != nil {
			return nil, e
		}
		if len(raw) > MaxStored {
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
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	s.data[key] = v
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
	if event != nil {
		v.Seq++
		event.Cursor = fmt.Sprintf("%s:%d", v.Epoch, v.Seq)
		v.Events = append(v.Events, *event)
		if len(v.Events) > 256 || v.Seq < uint64(len(v.Events)) {
			v.Events = v.Events[len(v.Events)-256:]
		}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(raw) > MaxStored {
		return ErrLimit
	}
	if err = privateDir(filepath.Join(s.root, key)); err != nil {
		return err
	}
	if err = s.write(filepath.Join(s.root, key, "state.json"), raw); err != nil {
		s.broken[key] = err
		return err
	}
	s.data[key] = v
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
func (s *Store) change(key, rid string, fn func(*Run) error) (Run, error) {
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
	r.Updated = s.now().UTC()
	if terminal(r.State) && !terminal(v.Runs[rid].State) {
		r.Expires = r.Updated.Add(Retention)
	}
	v.Runs[rid] = r
	ev := Event{RunID: rid, State: r.State, Time: r.Updated}
	if len(r.Attempts) > 0 {
		ev.AttemptID = r.Attempts[len(r.Attempts)-1].ID
	}
	return r, s.commit(key, v, &ev)
}
func (s *Store) Create(key, kind, priority string, input json.RawMessage) (Run, error) {
	if len(input) > MaxInput {
		return Run{}, ErrLimit
	}
	if !json.Valid(input) || (priority != "interactive" && priority != "planted") {
		return Run{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return Run{}, err
	}
	keyLive, total := 0, 0
	for k, d := range s.data {
		for _, r := range d.Runs {
			if !terminal(r.State) {
				total++
				if k == key {
					keyLive++
				}
			}
		}
	}
	if keyLive >= MaxLiveKey || total >= MaxLiveHost || len(v.Runs) >= MaxRuns {
		return Run{}, ErrLimit
	}
	now := s.now().UTC()
	r := Run{ID: id("r_"), KeyID: key, Kind: kind, Priority: priority, State: Queued, Created: now, Updated: now, Expires: now.Add(MaxAge), Input: append(json.RawMessage(nil), input...), Attempts: []Attempt{}}
	v = clone(v)
	v.Runs[r.ID] = r
	return r, s.commit(key, v, &Event{RunID: r.ID, State: r.State, Time: now})
}
func (s *Store) Get(key, rid string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return Run{}, err
	}
	r, ok := clone(v).Runs[rid]
	if !ok {
		return Run{}, ErrNotFound
	}
	return r, nil
}
func (s *Store) List(key string) ([]Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list(key)
}
func (s *Store) list(key string) ([]Summary, error) {
	out := []Summary{}
	if key != "" {
		if _, err := s.load(key); err != nil {
			return nil, err
		}
	}
	for k, v := range s.data {
		if key != "" && key != k {
			continue
		}
		for _, r := range v.Runs {
			out = append(out, summary(r))
		}
	}
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
