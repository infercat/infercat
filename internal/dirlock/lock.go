// Package dirlock excludes concurrent host lifecycles in one data directory.
package dirlock

import (
	"fmt"
	"os"
	"path/filepath"
)

// Acquire holds an OS lock until Close or process exit. Never unlink the lock file:
// replacing its inode would let another process lock a different file at the same path.
func Acquire(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "host.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lock(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("data directory is in use or cannot be locked; stop serve before upgrading its identity: %w", err)
	}
	return f, nil
}
