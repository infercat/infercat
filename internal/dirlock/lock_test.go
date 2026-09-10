package dirlock

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExclusiveAndRelease(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Acquire(dir); err == nil {
		other.Close()
		t.Fatal("second owner acquired lock")
	}
	first.Close()
	next, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
	if _, err := os.Stat(filepath.Join(dir, "host.lock")); err != nil {
		t.Fatal("lock inode must remain", err)
	}
}
func TestLockHolder(t *testing.T) {
	dir := os.Getenv("INFERCAT_LOCK_TEST_DIR")
	if dir == "" {
		return
	}
	f, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Println("locked")
	var b [1]byte
	os.Stdin.Read(b[:])
}
func TestProcessDeathReleasesLock(t *testing.T) {
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(binary, "-test.run=^TestLockHolder$")
	c.Env = append(os.Environ(), "INFERCAT_LOCK_TEST_DIR="+dir)
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Process.Kill()
	scanner := bufio.NewScanner(out)
	if !scanner.Scan() || scanner.Text() != "locked" {
		t.Fatal("child never acquired lock")
	}
	if f, err := Acquire(dir); err == nil {
		f.Close()
		t.Fatal("acquired child's lock")
	}
	if err := c.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	c.Wait()
	f, err := Acquire(dir)
	if err != nil {
		t.Fatal("crash left lock held:", err)
	}
	f.Close()
}
