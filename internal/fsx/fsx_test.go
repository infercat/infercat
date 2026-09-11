package fsx

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReplacementBytesModesAndFailureCleanup(t *testing.T) {
	for name, write := range map[string]func(string, []byte, os.FileMode) error{"file": WriteFile, "directory": WriteFileSyncDir} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state")
			for _, mode := range []os.FileMode{0600, 0640} {
				want := []byte{0, byte(mode), '\n', 255}
				if err := write(path, want, mode); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatal("replacement bytes", got, err)
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatal("replacement mode", info, err)
				}
			}
			if err := os.Mkdir(filepath.Join(dir, "blocked"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := write(filepath.Join(dir, "blocked"), []byte("refused"), 0600); err == nil {
				t.Fatal("replaced a directory")
			}
			left, err := filepath.Glob(filepath.Join(dir, ".run-*"))
			if err != nil || len(left) != 0 {
				t.Fatal("temporary files leaked", left, err)
			}
		})
	}
}
