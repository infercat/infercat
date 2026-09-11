package keys

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/infercat/infercat/internal/fsx"
)

// ErrNotFound is returned when no key matches the given id or name.
var ErrNotFound = errors.New("no such key")

// FileVersion is the schema version of keys.json.
const FileVersion = 1

// FileName is the name of the key file inside the data dir.
const FileName = "keys.json"

type fileDoc struct {
	Version int    `json:"version"`
	Keys    []*Key `json:"keys"`
}

// stamp is the cheap change detector for hot reload: modification time plus size.
type stamp struct {
	mod  time.Time
	size int64
}

// FileStore implements Store over keys.json.
//
// Reads are hot: the file is re-stat'ed at most once per second and re-read when the stamp
// changes, so `keys add` in one process is visible to a running gateway in another without a
// restart. Writes are atomic (temp file + rename) and the file is kept at mode 0600 because it
// holds the hash of every friend's secret (docs/PRINCIPLES.md, Protection 2).
type FileStore struct {
	path    string
	changes chan struct{}

	mu        sync.Mutex
	keys      []*Key
	st        stamp
	loaded    bool
	lastCheck time.Time

	// now is the clock; tests replace it to exercise the once-per-second reload throttle.
	now func() time.Time
}

// NewFileStore opens (and does not create) keys.json under dataDir. A missing file is an empty
// store; it is written on the first Add.
func NewFileStore(dataDir string) (*FileStore, error) {
	s := &FileStore{path: filepath.Join(dataDir, FileName), now: time.Now}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s, s.reload(true)
}

// Changes coalesces successful commits. Consumers must re-read the current snapshot.
// Notification never blocks a committed mutation or calls out under the store mutex.
func (s *FileStore) Changes() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changes == nil {
		s.changes = make(chan struct{}, 1)
	}
	return s.changes
}

// Reload re-reads keys.json now, past the once-per-second throttle. The CLI pokes a running host
// through the admin socket after every key write so a pause, resume, revoke, or rotate is in
// force before the command returns (ticket 009 promise 9).
func (s *FileStore) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reload(true)
}

// reload re-reads keys.json if its stamp changed. Callers hold s.mu. Unless force is set the
// stat is skipped when the last check was less than a second ago.
func (s *FileStore) reload(force bool) error {
	now := s.now()
	if !force && s.loaded && now.Sub(s.lastCheck) < time.Second {
		return nil
	}
	s.lastCheck = now
	fi, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.keys, s.st, s.loaded = nil, stamp{}, true
			return nil
		}
		return err
	}
	cur := stamp{fi.ModTime(), fi.Size()}
	if s.loaded && cur == s.st {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var doc fileDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("%s: %w", s.path, err)
	}
	if doc.Version != FileVersion {
		return fmt.Errorf("%s: version %d is not understood by this build (want %d)", s.path, doc.Version, FileVersion)
	}
	for _, k := range doc.Keys {
		if k != nil {
			k.Limits = AudioDefaults(k.Limits)
		}
	}
	s.keys, s.st, s.loaded = doc.Keys, cur, true
	return nil
}

func cloneKey(k *Key) *Key {
	c := *k
	c.Limits.Models = append([]string(nil), k.Limits.Models...)
	return &c
}

// Lookup resolves a presented secret in constant time with respect to which key matched. A miss
// re-stats the file first, past the once-per-second throttle, so a key minted a moment ago by
// `keys add` in another process never answers 401 (ticket 005 fix 10a): the miss is the rare
// path and one stat is cheap.
func (s *FileStore) Lookup(ctx context.Context, secret string) (*Key, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(false); err != nil {
		return nil, false, err
	}
	want := []byte(HashSecret(secret))
	found := s.match(want)
	if found == nil {
		if err := s.reload(true); err != nil {
			return nil, false, err
		}
		found = s.match(want)
	}
	if found == nil {
		return nil, false, nil
	}
	return cloneKey(found), true, nil
}

// match scans every key so the time taken does not depend on which one matched. Callers hold s.mu.
func (s *FileStore) match(want []byte) *Key {
	var found *Key
	for _, k := range s.keys {
		if subtle.ConstantTimeCompare([]byte(k.SecretHash), want) == 1 {
			found = k
		}
	}
	return found
}

// List returns every key, oldest first.
func (s *FileStore) List(ctx context.Context) ([]*Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(false); err != nil {
		return nil, err
	}
	out := make([]*Key, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, cloneKey(k))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// WithDefaults fills the zero fields of l from DefaultLimits. MaxContext 0 (= the upstream's
// context) and an empty Models list (= every model) are meaningful values and are left alone.
func WithDefaults(l Limits) Limits {
	l = AudioDefaults(l)
	d := DefaultLimits()
	if l.RPM == 0 {
		l.RPM = d.RPM
	}
	if l.TPM == 0 {
		l.TPM = d.TPM
	}
	if l.MaxConcurrent == 0 {
		l.MaxConcurrent = d.MaxConcurrent
	}
	if l.MaxOutputTokens == 0 {
		l.MaxOutputTokens = d.MaxOutputTokens
	}
	if l.DailyTokens == 0 {
		l.DailyTokens = d.DailyTokens
	}
	return l
}

// Add creates a key with limits l (zero fields take the defaults) and returns the plaintext
// secret, which is never stored and never shown again.
func (s *FileStore) Add(ctx context.Context, name string, l Limits) (*Key, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", errors.New("a key needs a name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(true); err != nil {
		return nil, "", err
	}
	secret, err := NewSecret()
	if err != nil {
		return nil, "", err
	}
	id, err := s.newID()
	if err != nil {
		return nil, "", err
	}
	k := &Key{
		ID:         id,
		Name:       name,
		SecretHash: HashSecret(secret),
		Status:     Active,
		CreatedAt:  s.now().UTC().Truncate(time.Second),
		Limits:     WithDefaults(l),
	}
	prev := s.keys
	s.keys = append(append([]*Key(nil), s.keys...), k)
	if err := s.save(); err != nil {
		s.keys = prev // a refused write never mutates the store
		return nil, "", err
	}
	return cloneKey(k), secret, nil
}

// SetStatus pauses, resumes, or revokes a key.
func (s *FileStore) SetStatus(ctx context.Context, id string, st Status) error {
	switch st {
	case Active, Paused, Revoked:
	default:
		return fmt.Errorf("unknown status %q", st)
	}
	return s.mutate(id, func(k *Key) error { k.Status = st; return nil })
}

// Rotate issues a new secret for an existing key, keeping its id, name, limits, and history.
func (s *FileStore) Rotate(ctx context.Context, id string) (string, error) {
	secret, err := NewSecret()
	if err != nil {
		return "", err
	}
	if err := s.mutate(id, func(k *Key) error { k.SecretHash = HashSecret(secret); return nil }); err != nil {
		return "", err
	}
	return secret, nil
}

// SetLimits replaces a key's limits wholesale. Unlike Add it does not apply defaults: a zero
// here is the host explicitly saying "no limit", except image fields: zero is default, negative unlimited.
func (s *FileStore) SetLimits(ctx context.Context, id string, l Limits) error {
	return s.SetLimitsAndAgent(ctx, id, l, nil)
}

// SetLimitsAndAgent commits a capability change and its limits as one mutation.
func (s *FileStore) SetLimitsAndAgent(ctx context.Context, id string, l Limits, agent *bool) error {
	return s.mutate(id, func(k *Key) error {
		k.Limits = ImageDefaults(l)
		if agent != nil {
			k.Agent = *agent
		}
		return nil
	})
}

// Find resolves an id, or the exact name of a key when that name is unique.
func (s *FileStore) Find(ctx context.Context, ref string) (*Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(false); err != nil {
		return nil, err
	}
	i, err := s.index(ref)
	if err != nil {
		return nil, err
	}
	return cloneKey(s.keys[i]), nil
}

// index locates a key by id, else by name. A name belongs to whoever is still using it: a revoked
// key keeps its id but stops answering to its name, so `keys add bob` after revoking bob leaves
// `keys pause bob` unambiguous (ticket 009 promise 3, which lets a revoked name be reused).
// A name that only a revoked key holds still resolves, so a mistake can be inspected. Callers
// hold s.mu.
func (s *FileStore) index(ref string) (int, error) {
	for i, k := range s.keys {
		if k.ID == ref {
			return i, nil
		}
	}
	live, all, nLive, nAll := -1, -1, 0, 0
	for i, k := range s.keys {
		if k.Name != ref {
			continue
		}
		all, nAll = i, nAll+1
		if k.Status != Revoked {
			live, nLive = i, nLive+1
		}
	}
	switch {
	case nLive == 1:
		return live, nil
	case nLive > 1:
		return -1, fmt.Errorf("%q names %d keys; use the key id instead", ref, nLive)
	case nAll == 1:
		return all, nil
	case nAll > 1:
		return -1, fmt.Errorf("%q names %d revoked keys; use the key id instead", ref, nAll)
	}
	return -1, fmt.Errorf("%w: %q", ErrNotFound, ref)
}

// mutate applies f to one key and persists the result, rolling back if the write fails.
func (s *FileStore) mutate(ref string, f func(*Key) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(true); err != nil {
		return err
	}
	i, err := s.index(ref)
	if err != nil {
		return err
	}
	before := *s.keys[i]
	next := cloneKey(s.keys[i])
	if err := f(next); err != nil {
		return err
	}
	s.keys[i] = next
	if err := s.save(); err != nil {
		s.keys[i] = &before // a refused write never mutates the store
		return err
	}
	return nil
}

func (s *FileStore) newID() (string, error) {
	for try := 0; try < 100; try++ {
		var b [3]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		id := "k_" + hex.EncodeToString(b[:])
		if _, err := s.index(id); errors.Is(err, ErrNotFound) {
			return id, nil
		}
	}
	return "", errors.New("could not find a free key id")
}

func (s *FileStore) save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(fileDoc{Version: FileVersion, Keys: s.keys}, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := fsx.WriteFile(s.path, b, 0600); err != nil {
		return err
	}
	if fi, err := os.Stat(s.path); err == nil {
		s.st = stamp{fi.ModTime(), fi.Size()}
	}
	s.lastCheck = s.now()
	s.loaded = true
	if s.changes != nil {
		select {
		case s.changes <- struct{}{}:
		default:
		}
	}
	return nil
}
