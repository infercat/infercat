package main

import (
	"context"
	"github.com/infercat/infercat/internal/dirlock"
	"strings"
	"testing"
)

func TestServeRefusesLockedDataDirBeforeTunnel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f, err := dirlock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	plat := testPlatform(fakeAddr, nil)
	called := false
	plat.startTunnel = func(context.Context, tunnelOptions) (tunnelServer, error) { called = true; return nil, nil }
	r := exec(t, plat, "--data-dir", dir, "serve", "--console", "off")
	if r.code == 0 || called || !strings.Contains(r.err, "data directory is in use") {
		t.Fatal(r, called)
	}
}
