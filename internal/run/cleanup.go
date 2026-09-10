package run

import (
	"os"
	"path/filepath"
	"strings"
)

// Durable ids authorize deletion only inside this key's images directory.
func (s *Store) restoreCleanup(key string, v *snapshot) {
	if s.imageCleanup == nil {
		s.imageCleanup = map[string]bool{}
	}
	for _, rid := range v.Cleanup {
		path := s.artifactPath(key, rid)
		s.imageCleanup[path] = true
		delete(s.imageOrphans, path)
	}
}
func (s *Store) drainCleanup(key string) error {
	v := s.data[key]
	if v == nil || len(v.Cleanup) == 0 {
		return nil
	}
	next := clone(v)
	next.Cleanup = nil
	for _, rid := range v.Cleanup {
		path := s.artifactPath(key, rid)
		s.unlinkImage(path)
		if s.imageCleanup[path] {
			next.Cleanup = append(next.Cleanup, rid)
		}
	}
	if len(next.Cleanup) != len(v.Cleanup) {
		return s.commit(key, next, nil)
	}
	return nil
}

// Both atomicWrite callers hold s.mu; remnants cannot belong to an active write.
func (s *Store) cleanStateTemps(key string) {
	dir := filepath.Join(s.root, key)
	info, err := os.Lstat(dir)
	if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		s.log("state temp scan refused for %s", key)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			s.log("state temp scan for %s: %v", key, err)
		}
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".run-") && !entry.IsDir() {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
				s.log("state temp cleanup for %s: %v", key, err)
			}
		}
	}
}
