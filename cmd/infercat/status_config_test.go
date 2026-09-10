package main

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
)

func TestStatusNoConfigHome(t *testing.T) {
	if dir := os.Getenv("INFERCAT_STATUS_FIXTURE"); dir != "" {
		var out, errw lockedBuffer
		code := run(context.Background(), []string{"status", "--data-dir", dir}, &out, &errw, nil, false, testPlatform(fakeAddr, nil))
		if code != 0 || !strings.Contains(out.String(), "agents: unknown (no config dir)") || !strings.Contains(out.String(), "queue") {
			t.Fatal(code, out.String(), errw.String())
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var watched, watchErr lockedBuffer
		done := make(chan int, 1)
		go func() {
			done <- run(ctx, []string{"status", "--watch", "--data-dir", dir}, &watched, &watchErr, nil, false, testPlatform(fakeAddr, nil))
		}()
		waitUntil(t, "degraded watch", func() bool {
			return strings.Contains(watched.String(), "agents: unknown (no config dir)") && strings.Contains(watched.String(), "queue")
		})
		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Fatal(code, watchErr.String())
			}
		case <-time.After(3 * time.Second):
			t.Fatal("watch did not stop")
		}
		return
	}
	short, err := os.MkdirTemp("", "i147-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	dir, err := filepath.EvalSymlinks(short)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := admin.Serve(dir, func() admin.Status { return admin.Status{Mode: "host"} }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cmd := osexec.Command(os.Args[0], "-test.run=^TestStatusNoConfigHome$")
	cmd.Env = []string{"INFERCAT_STATUS_FIXTURE=" + dir, "PATH=" + os.Getenv("PATH"), "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("no-config environment: %v: %s", err, out)
	}
}
