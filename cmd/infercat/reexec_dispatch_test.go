//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This subprocess calls run itself, not the supervise/agent TestMain shims. The
// guardian may kill its process group and _confine replaces its process on Linux.
func TestReexecRunHelper(t *testing.T) {
	if os.Getenv("INFERCAT_REEXEC_RUN_TEST") != "1" {
		return
	}
	i := slices.Index(os.Args, "--")
	if i < 0 {
		os.Exit(99)
	}
	os.Exit(run(context.Background(), os.Args[i+1:], os.Stdout, os.Stderr, os.Stdin, true, newPlatform()))
}
func TestRunPreservesInternalChildArgv(t *testing.T) {
	childArgs := []string{"ENGINE-STARTED", "--host", "127.0.0.1", "--host-file", "private path", "--json", "--json=2", "--port", "8123", "", "中文 value", "--", "literal"}
	child := append([]string{"/bin/sh", "-c", `printf '%s\000' "$@"`, "argv-printer"}, childArgs...)
	want := []byte(strings.Join(childArgs, "\x00") + "\x00")
	for _, verb := range []string{"_profile-guardian", "_agent-guardian", "_confine"} {
		t.Run(verb, func(t *testing.T) {
			if verb == "_confine" && runtime.GOOS != "linux" {
				t.Skip("_confine executes only on Linux; Darwin uses sandbox-exec")
			}
			args := append([]string{verb}, child...)
			if verb == "_confine" {
				workspace := t.TempDir()
				if err := os.Mkdir(filepath.Join(workspace, "tmp"), 0700); err != nil {
					t.Fatal(err)
				}
				args = append([]string{verb, workspace, t.TempDir(), "--"}, child...)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := osexec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestReexecRunHelper$", "--"}, args...)...)
			cmd.Env = append(os.Environ(), "INFERCAT_REEXEC_RUN_TEST=1")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
			cmd.WaitDelay = time.Second
			life, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer life.Close()
			defer writer.Close()
			health, healthWriter, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer health.Close()
			defer healthWriter.Close()
			cmd.ExtraFiles = []*os.File{life, health}
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			err = cmd.Run()
			if verb == "_confine" && err != nil {
				t.Fatalf("confinement execution failed: %v: %s", err, stderr.String())
			}
			// Guardian intentionally terminates its group after the child exits. Output proves exec.
			if ctx.Err() != nil || !bytes.Equal(out.Bytes(), want) {
				t.Fatalf("%s: error=%v stdout=%q stderr=%q", verb, err, out.Bytes(), stderr.Bytes())
			}
		})
	}
}
