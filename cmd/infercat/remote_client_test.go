package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/machine"
)

func remoteTestEnv(t *testing.T) (*env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	_, target, err := machine.ParseTarget([]string{"--host", "never-log-this-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, errw := new(bytes.Buffer), new(bytes.Buffer)
	return &env{out: out, errw: errw, remoteTarget: target, in: forbiddenInput{}}, out, errw
}
func TestRemoteUnsupportedRefusesBeforeFactory(t *testing.T) {
	for _, args := range [][]string{
		{"service", "stop"}, {"console"}, {"reload"}, {"serve"}, {"setup"}, {"connect", "code"}, {"agent", "run"},
		{"usage", "--key", "k_a"}, {"usage", "--since", "24h"}, {"expose", "--on"}, {"expose", "--off"}, {"expose", "--register", "x"},
		{"keys", "add", "--force=false", "--", "alice"}, {"keys", "limits", "k_a", "--agent=false"}, {"settings", "set", "--", "console=off"},
	} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			e, out, _ := remoteTestEnv(t)
			e.adminClientFactory = func(context.Context, string) (*admin.Client, error) {
				t.Fatal("dialed unsupported action")
				return nil, nil
			}
			got := e.runRemote(context.Background(), append([]string{"--json"}, args...), nil)
			if got != 1 || !strings.Contains(out.String(), `"code":"not_available_remotely"`) {
				t.Fatal(got, out.String())
			}
		})
	}
}
func TestRemoteMachineNoLocalConfigOrOfflineFallback(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, op := range [][]string{{"keys", "list"}, {"keys", "add", "--", "alice"}, {"status"}} {
		e, out, _ := remoteTestEnv(t)
		e.adminClientFactory = func(_ context.Context, dir string) (*admin.Client, error) {
			if dir != "" {
				t.Fatal("resolved local data dir")
			}
			return nil, admin.ErrNoDaemon
		}
		if got := e.runRemote(context.Background(), append([]string{"--json"}, op...), nil); got != 69 {
			t.Fatal(got, out.String())
		}
		if strings.Contains(out.String(), "address yet") {
			t.Fatal("offline fallback")
		}
	}
}
func TestRemoteStatusFactoryOnceAndSchemaBytes(t *testing.T) {
	f := newMachineFixture(t)
	e, out, _ := remoteTestEnv(t)
	calls := 0
	e.adminClientFactory = func(_ context.Context, dir string) (*admin.Client, error) {
		calls++
		if dir != "" {
			t.Fatal(dir)
		}
		return admin.NewClient(f.dir)
	}
	if got := e.runRemote(context.Background(), []string{"status", "--json"}, nil); got != 0 {
		t.Fatal(got, out.String())
	}
	if calls != 1 || !strings.Contains(out.String(), `"name":"test host"`) {
		t.Fatal(calls, out.String())
	}
}
func TestRemoteSelectorDoesNotTouchDefaultDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	var out, errw bytes.Buffer
	got := run(context.Background(), []string{"--host-file", filepath.Join(root, "missing"), "service", "stop", "--json"}, &out, &errw, nil, false, newPlatform())
	if got != 1 || !strings.Contains(out.String(), "not_available_remotely") || errw.Len() != 0 {
		t.Fatal(got, out.String(), errw.String())
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}

type remoteWatchFixture struct {
	calls   int
	times   []time.Time
	failure error
}

func (f *remoteWatchFixture) Call(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
	f.calls++
	f.times = append(f.times, time.Now())
	time.Sleep(3 * time.Second)
	if f.calls == 2 {
		return nil, f.failure
	}
	return json.RawMessage(`{"name":"remote"}`), nil
}
func (f *remoteWatchFixture) Events(context.Context, func(json.RawMessage), func()) error {
	panic("remote events requested")
}
func TestRemoteWatchNoEventsNoCatchupAndRetryAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, out, _ := remoteTestEnv(t)
		f := &remoteWatchFixture{failure: &admin.APIError{Status: 429, Message: "busy", RetryAfter: "60"}}
		code := e.watchRemote(context.Background(), machineRequest{interval: time.Second}, f, machine.Host{Name: "remote"}, json.RawMessage(`{}`))
		if code != 75 || f.calls != 2 || f.times[1].Sub(f.times[0]) != 4*time.Second {
			t.Fatal(code, f.times)
		}
		if !strings.Contains(out.String(), `"events":false`) || !strings.Contains(out.String(), `"retry_after":"60"`) || strings.Contains(out.String(), `"type":"event"`) {
			t.Fatal(out.String())
		}
	})
	e, _, _ := remoteTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := e.watchRemote(ctx, machineRequest{interval: time.Second}, &remoteWatchFixture{failure: errors.New("unused")}, machine.Host{}, json.RawMessage(`{}`)); code != 0 {
		t.Fatal(code)
	}
}
func TestRemoteWatchIntervalRefusesBeforeFactory(t *testing.T) {
	e, out, _ := remoteTestEnv(t)
	e.adminClientFactory = func(context.Context, string) (*admin.Client, error) { t.Fatal("dialed"); return nil, nil }
	if got := e.runRemote(context.Background(), []string{"watch", "--json", "--interval", "999ms"}, nil); got != 2 || !strings.Contains(out.String(), "at least 1s") {
		t.Fatal(got, out.String())
	}
}
