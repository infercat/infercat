package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/machine"
	"github.com/infercat/infercat/internal/usage"
)

func TestMachineWatchRealHTTP(t *testing.T) {
	t.Parallel()
	f := newMachineFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rd, wr := io.Pipe()
	defer rd.Close()
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		done <- run(ctx, []string{"--data-dir", f.dir, "watch", "--json", "--interval", "1h"}, wr, &stderr, forbiddenInput{}, false, newPlatform())
		wr.Close()
	}()
	dec := json.NewDecoder(rd)
	read := func() map[string]json.RawMessage {
		t.Helper()
		var line map[string]json.RawMessage
		if err := dec.Decode(&line); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(line)
		if bytes.Contains(raw, []byte("PRIVATE")) || bytes.Contains(raw, []byte(`"prompt"`)) || bytes.Contains(raw, []byte(`"completion"`)) {
			t.Fatal("content in watch", string(raw))
		}
		return line
	}
	hello := read()
	if string(hello["type"]) != `"hello"` || string(hello["schema"]) != "1" || string(hello["interval_ms"]) != "3600000" {
		t.Fatal(hello)
	}
	initial := read()
	if string(initial["type"]) != `"status"` || initial["at"] == nil || !bytes.Contains(initial["data"], []byte(`"name":"test host"`)) {
		t.Fatal(initial)
	}
	f.hub.Record(ctx, usage.Event{KeyID: f.id, Prompt: "PRIVATE PROMPT", Completion: "PRIVATE COMPLETION", Status: 200, CompletionTokens: 7})
	status, event := true, false
	for !status || !event {
		line := read()
		switch string(line["type"]) {
		case `"status"`:
			status = true
			if line["at"] == nil || !bytes.Contains(line["data"], []byte(`"name":"test host"`)) {
				t.Fatal(line)
			}
		case `"event"`:
			event = true
			var e usage.Event
			if json.Unmarshal(line["data"], &e) != nil || e.CompletionTokens != 7 || e.KeyID != f.id {
				t.Fatal(line)
			}
		}
	}
	if err := f.server.Close(); err != nil {
		t.Fatal(err)
	}
	for {
		line := read()
		if string(line["type"]) == `"gone"` {
			if string(line["reason"]) != `"host_stopped"` {
				t.Fatal(line)
			}
			break
		}
	}
	if code := <-done; code != 69 {
		t.Fatal(code)
	}
	if stderr.Len() != 0 {
		t.Fatal("machine watch stderr", stderr.String())
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Fatal("output after gone", err)
	}
}

type watchProbe struct {
	emit func(context.Context, func(json.RawMessage), func()) error
}

func (w watchProbe) Events(ctx context.Context, fn func(json.RawMessage), ready func()) error {
	return w.emit(ctx, fn, ready)
}
func (watchProbe) Call(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"kept":0}`), nil
}

type stalledWatchWriter struct {
	blocked, release chan struct{}
	once             sync.Once
	lines            chan []byte
}

func (w *stalledWatchWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"type":"event"`)) {
		w.once.Do(func() { close(w.blocked); <-w.release })
	}
	w.lines <- bytes.Clone(p)
	return len(p), nil
}

func TestMachineWatchCountsOnlyItsOwnDropsAndJoins(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &stalledWatchWriter{blocked: make(chan struct{}), release: make(chan struct{}), lines: make(chan []byte, 1024)}
	produced := make(chan struct{})
	readerExited := make(chan struct{})
	source := watchProbe{emit: func(ctx context.Context, fn func(json.RawMessage), ready func()) error {
		defer close(readerExited)
		ready()
		fn(json.RawMessage(`{"status":200}`))
		<-w.blocked
		for i := 0; i < 300; i++ {
			fn(json.RawMessage(`{"status":200}`))
		}
		close(produced)
		<-ctx.Done()
		return ctx.Err()
	}}
	e := &env{out: w}
	done := make(chan int, 1)
	go func() {
		done <- e.watchHost(ctx, machineRequest{interval: time.Hour}, source, machine.Host{}, json.RawMessage(`{"initial":true}`))
	}()
	select {
	case <-produced:
	case <-time.After(3 * time.Second):
		t.Fatal("reader blocked on stdout")
	}
	close(w.release)
	for {
		select {
		case raw := <-w.lines:
			if strings.Contains(string(raw), `"type":"dropped"`) {
				var line struct{ Count int }
				if json.Unmarshal(raw, &line) != nil || line.Count != 44 {
					t.Fatal(string(raw))
				}
				cancel()
				if code := <-done; code != 0 {
					t.Fatal(code)
				}
				select {
				case <-readerExited:
				default:
					t.Fatal("reader outlived watch")
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("local drop count missing")
		}
	}
}

func TestMachineWatchDistinguishesDisconnectFromMalformedReply(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		reason string
		exit   int
	}{
		{"disconnect", admin.ErrDisconnected, "host_stopped", 69},
		{"malformed", admin.ErrResponse, "invalid_response", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			e := &env{out: &out}
			source := watchProbe{emit: func(context.Context, func(json.RawMessage), func()) error { return tc.err }}
			if code := e.watchHost(context.Background(), machineRequest{interval: time.Hour}, source, machine.Host{}, json.RawMessage(`{"initial":true}`)); code != tc.exit {
				t.Fatal(code)
			}
			var line struct{ Type, Reason string }
			if err := json.Unmarshal(out.Bytes(), &line); err != nil || line.Type != "gone" || line.Reason != tc.reason {
				t.Fatal(out.String(), err)
			}
		})
	}
}

func TestMachineWatchPollsAfterImmediateSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var out lockedBuffer
		e := &env{out: &out}
		source := watchProbe{emit: func(ctx context.Context, _ func(json.RawMessage), ready func()) error {
			ready()
			<-ctx.Done()
			return ctx.Err()
		}}
		done := make(chan int, 1)
		go func() {
			done <- e.watchHost(ctx, machineRequest{interval: 2 * time.Second}, source, machine.Host{}, json.RawMessage(`{"initial":true}`))
		}()
		synctest.Wait()
		if !strings.Contains(out.String(), `"initial":true`) || strings.Contains(out.String(), `"kept"`) {
			t.Fatal(out.String())
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if strings.Count(out.String(), `"type":"status"`) != 2 || !strings.Contains(out.String(), `"kept":0`) {
			t.Fatal(out.String())
		}
		cancel()
		synctest.Wait()
		if code := <-done; code != 0 {
			t.Fatal(code)
		}
	})
}
