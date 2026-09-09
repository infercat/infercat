package adminkey

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLifecycleAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if s.State().Enabled {
		t.Fatal("default on")
	}
	code, e := s.Mint(false)
	if e != nil || len(code) != 43 {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "admin.json"))
	st, _ := os.Stat(filepath.Join(dir, "admin.json"))
	if bytes.Contains(raw, []byte(code)) || st.Mode().Perm() != 0600 {
		t.Fatal("secret or permissions")
	}
	again, e := Open(dir)
	if e != nil || !again.Authenticate(code) || again.Authenticate("wrong") {
		t.Fatal("restart authentication", e)
	}
	if !again.State().InUse {
		t.Fatal("not seen")
	}
	again.seen = time.Now().Add(-11 * time.Minute)
	if again.State().InUse {
		t.Fatal("stale session")
	}
	next, e := again.Mint(true)
	if e != nil || again.Authenticate(code) || !again.Authenticate(next) {
		t.Fatal("rotation", e)
	}
	if e = again.Disable(); e != nil {
		t.Fatal(e)
	}
	again, e = Open(dir)
	if e != nil || again.State().Enabled || again.Authenticate(next) {
		t.Fatal("off persisted", e)
	}
}
func TestFailedCommitKeepsCode(t *testing.T) {
	s, _ := Open(t.TempDir())
	code, e := s.Mint(false)
	if e != nil {
		t.Fatal(e)
	}
	s.path = filepath.Join(t.TempDir(), "missing", "admin.json")
	if _, e = s.Mint(true); e == nil || !s.Authenticate(code) {
		t.Fatal("failed rotation changed code")
	}
}
func TestConcurrentReadersAndRotation(t *testing.T) {
	s, _ := Open(t.TempDir())
	code, _ := s.Mint(false)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.Authenticate(code)
				s.State()
			}
		}()
	}
	s.Mint(true)
	wg.Wait()
}
func TestDamagedRecordStartsDisabledAndRepairsExplicitly(t *testing.T) {
	for _, raw := range []string{`{"hash":"invalid"}`, `{"hash":`, `null`} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "admin.json")
			os.WriteFile(path, []byte(raw), 0600)
			s, e := Open(dir)
			if e != nil || s.State().Enabled || s.State().WarningFile != path || s.Warning() == "" {
				t.Fatal(s, e)
			}
			kept, _ := os.ReadFile(path)
			if string(kept) != raw {
				t.Fatal("opening overwrote damaged file")
			}
			code, e := s.Mint(false)
			if e != nil || !s.Authenticate(code) || s.Warning() != "" {
				t.Fatal("repair", e)
			}
			again, e := Open(dir)
			if e != nil || !again.Authenticate(code) || again.Warning() != "" {
				t.Fatal("restart", e)
			}
		})
	}
}
func TestUnreadableRecordAndFailedRepair(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.json")
	os.Mkdir(path, 0700)
	os.WriteFile(filepath.Join(path, "keep"), []byte("keep"), 0600)
	s, e := Open(dir)
	if e != nil || s.State().Enabled || s.Warning() == "" {
		t.Fatal(s, e)
	}
	if _, e = s.Mint(false); e == nil || s.State().Enabled || s.Warning() == "" {
		t.Fatal("failed repair was hidden")
	}
	if e = s.Disable(); e == nil || s.Warning() == "" {
		t.Fatal("failed off cleared diagnostic")
	}
	if _, e = os.Stat(filepath.Join(path, "keep")); e != nil {
		t.Fatal("damaged artifact removed")
	}
}

func TestUnreadableAdminFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.json")
	os.WriteFile(path, []byte(`{"hash":"invalid"}`), 0000)
	defer os.Chmod(path, 0600)
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("this platform/user can read mode-000 files")
	}
	s, err := Open(dir)
	if err != nil || s.State().Enabled || s.State().WarningFile != path {
		t.Fatal(s, err)
	}
	code, err := s.Mint(false)
	if err != nil || !s.Authenticate(code) || s.Warning() != "" {
		t.Fatal("repair", err)
	}
}
