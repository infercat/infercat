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
func TestCorruptRecordRefuses(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "admin.json"), []byte(`{"hash":"invalid"}`), 0600)
	if _, e := Open(dir); e == nil {
		t.Fatal("corrupt accepted")
	}
}
