// Package adminkey owns the single opt-in remote-console bearer, separate from friend keys.
package adminkey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type State struct {
	Enabled bool      `json:"enabled"`
	Since   time.Time `json:"since,omitempty"`
	InUse   bool      `json:"in_use"`
}
type record struct {
	Hash  string    `json:"hash"`
	Since time.Time `json:"since"`
}
type Store struct {
	mu    sync.Mutex
	path  string
	value record
	seen  time.Time
}

func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "admin.json")}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &s.value); err != nil {
		return nil, err
	}
	h, e := hex.DecodeString(strings.TrimPrefix(s.value.Hash, "sha256:"))
	if e != nil || len(h) != sha256.Size || !strings.HasPrefix(s.value.Hash, "sha256:") {
		return nil, errors.New("invalid admin.json hash")
	}
	if err = os.Chmod(s.path, 0600); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return State{Enabled: s.value.Hash != "", Since: s.value.Since, InUse: !s.seen.IsZero() && time.Since(s.seen) < 10*time.Minute}
}
func (s *Store) Authenticate(secret string) bool {
	sum := sha256.Sum256([]byte(secret))
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, _ := hex.DecodeString(strings.TrimPrefix(s.value.Hash, "sha256:"))
	if len(stored) != sha256.Size || subtle.ConstantTimeCompare(sum[:], stored) != 1 {
		return false
	}
	s.seen = time.Now()
	return true
}

// Mint commits before returning the only plaintext copy. A failed write preserves the old code.
func (s *Store) Mint(rotate bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if (s.value.Hash != "") != rotate {
		return "", errors.New("remote state changed; refresh before trying again")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(secret))
	next := record{Hash: "sha256:" + hex.EncodeToString(sum[:]), Since: time.Now().UTC()}
	b, err := json.Marshal(next)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".admin-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(f.Name(), s.path)
	}
	if err != nil {
		return "", err
	}
	s.value = next
	s.seen = time.Time{}
	return secret, nil
}
func (s *Store) Disable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.value = record{}
	s.seen = time.Time{}
	return nil
}
