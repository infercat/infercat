package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

func TestAgentRuntimeOptionalAndMissingInstallDoesNotBlockServe(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		dir, err := os.MkdirTemp("", "ic116-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		gw := newFakeGateway()
		plat := testPlatform(fakeAddr, nil)
		plat.startTunnel = func(context.Context, tunnelOptions) (tunnelServer, error) { return fakeTunnel{}, nil }
		plat.newGateway = func(gatewayOptions, upstream.Upstream, keys.Store, usage.Recorder, func(string, ...any)) (gatewayServer, error) {
			return gw, nil
		}
		args := []string{"serve", "--console", "off", "--data-dir", dir, "--upstream", fakeEngine(t)}
		if enabled {
			args = append(args, "--agent")
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var out, errw lockedBuffer
		done := make(chan int, 1)
		go func() { done <- run(ctx, args, &out, &errw, nil, false, plat) }()
		select {
		case <-gw.serving:
		case <-time.After(5 * time.Second):
			t.Fatalf("serve blocked: %s", errw.String())
		}
		st, err := admin.Fetch(ctx, dir)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown blocked")
		}
		if err != nil {
			t.Fatal(err)
		}
		if enabled && (st.Agent == nil || st.Agent.State != "failed" || st.Agent.LastError == "") {
			t.Fatalf("missing runtime status: %+v", st.Agent)
		}
		if !enabled && st.Agent != nil {
			t.Fatal("runtime enabled by default")
		}
	}
}
