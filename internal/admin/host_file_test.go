//go:build !windows

package admin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestHostFilePrivateAndNoLinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "code")
	code := testAdminCode(t)
	if err := os.WriteFile(path, []byte(code+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadAdminCode(path); err != nil || strings.TrimSpace(got) != code {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0644, 0660, 0400, 0700} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadAdminCode(path); !errors.Is(err, ErrHostFile) {
			t.Fatalf("mode %o accepted: %v", mode, err)
		}
	}
	os.Chmod(path, 0600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{link, dir, filepath.Join(dir, "secret-missing-path")} {
		if _, err := ReadAdminCode(p); !errors.Is(err, ErrHostFile) || strings.Contains(err.Error(), p) {
			t.Fatal(err)
		}
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAdminCode(fifo); !errors.Is(err, ErrHostFile) {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte(strings.Repeat("x", 16385)), 0600)
	if _, err := ReadAdminCode(path); !errors.Is(err, ErrHostFile) {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("secret-not-a-code"), 0600)
	if _, err := ReadAdminCode(path); !errors.Is(err, ErrAdminCode) || strings.Contains(err.Error(), "secret-not-a-code") {
		t.Fatal(err)
	}
}
