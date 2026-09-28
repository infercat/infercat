package main

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
)

func TestMachineOperationsSelectOneClient(t *testing.T) {
	t.Parallel()
	for _, op := range machineOperations {
		if len(op.Routes) == 0 || op.Operation == "watch" {
			continue
		}
		t.Run(op.Operation, func(t *testing.T) {
			t.Parallel()
			f := newMachineFixture(t)
			stored, err := f.runs.Stored(f.id)
			if err != nil {
				t.Fatal(err)
			}
			if op.Operation == "remote.rotate" {
				c, err := admin.NewClient(f.dir)
				if err != nil {
					t.Fatal(err)
				}
				_, err = c.Call(context.Background(), "POST", "/remote/enable", nil)
				c.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			calls := 0
			e := &env{out: &out, errw: io.Discard, plat: newPlatform(), openBrowser: func(context.Context, string) error { return nil }, adminClientFactory: func(_ context.Context, dir string) (*admin.Client, error) {
				calls++
				if dir != f.dir {
					t.Error("wrong selected data directory")
				}
				return admin.NewClient(dir)
			}}
			code, handled := e.machineRead(context.Background(), append([]string{"--data-dir", f.dir}, operationArgs(op.Operation, f.id, stored.Cursor)...))
			if !handled || code != 0 || calls != 1 {
				t.Fatal(op.Operation, code, calls, out.String())
			}
		})
	}
}

type machineWriteFunc func([]byte) (int, error)

func (f machineWriteFunc) Write(p []byte) (int, error) { return f(p) }

func TestMachineWatchSelectsOneClient(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	var out bytes.Buffer
	e := &env{out: machineWriteFunc(func(p []byte) (int, error) {
		n, err := out.Write(p)
		if bytes.Contains(p, []byte(`"type":"status"`)) {
			cancel()
		}
		return n, err
	}), adminClientFactory: func(_ context.Context, dir string) (*admin.Client, error) { calls++; return admin.NewClient(dir) }}
	if code := e.machineWatch(ctx, machineRequest{dir: f.dir, interval: time.Hour}); code != 0 || calls != 1 {
		t.Fatal(code, calls)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"type":"hello"`)) || !bytes.Contains(out.Bytes(), []byte(`"type":"status"`)) {
		t.Fatal(out.String())
	}
}
