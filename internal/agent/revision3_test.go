package agent

import (
	"errors"
	runstate "github.com/infercat/infercat/internal/run"
	"os"
	"path/filepath"
	"testing"
)

func Test116CResultLinesAcceptCRLF(t *testing.T) {
	for _, raw := range []string{"HTTP/1.1 200 OK\r\nheader: value", "line one\r\nline two", "search snippet\r\nnext", "embedded\rreturn"} {
		s := invalidStepNote()
		s.Result = oneLine(raw)
		if !s.Valid() {
			t.Fatal(raw, s)
		}
	}
}
func Test116CHomelessWorkspaceAndRefusal(t *testing.T) {
	t.Setenv("HOME", "")
	dir := t.TempDir()
	if _, err := workspaceFor(dir, "../outside"); !errors.Is(err, runstate.ErrAgentUnavailable) {
		t.Fatal("workspace key traversal", err)
	}
	p, err := workspaceFor(dir, "key")
	if err != nil || p != filepath.Join(dir, "agent", "workspaces", "key") {
		t.Fatal(p, err)
	}
	blocked := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocked, "agent"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceFor(blocked, "key"); !errors.Is(err, runstate.ErrAgentUnavailable) {
		t.Fatal(err)
	}
}
