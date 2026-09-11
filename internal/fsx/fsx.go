// Package fsx writes a complete replacement through a synced sibling file.
package fsx

import (
	"os"
	"path/filepath"
)

// WriteFile replaces path atomically; its parent directory must already exist.
func WriteFile(path string, b []byte, mode os.FileMode) error {
	// Run-store recovery recognizes this prefix; keep it shared and fixed.
	f, err := os.CreateTemp(filepath.Dir(path), ".run-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// WriteFileSyncDir also syncs the renamed directory entry. A directory-sync
// error is returned after the replacement, as required by the run-store seam.
func WriteFileSyncDir(path string, b []byte, mode os.FileMode) error {
	if err := WriteFile(path, b, mode); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
