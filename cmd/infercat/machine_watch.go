package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/machine"
)

type watchSource interface {
	Call(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
	Events(context.Context, func(json.RawMessage), func()) error
}

func (e *env) machineWatch(ctx context.Context, r machineRequest) int {
	client, host, _, err := hostClient(ctx, r.dir)
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		failure := machine.Classify("watch", err)
		if json.NewEncoder(e.out).Encode(map[string]any{"schema": machine.Schema, "type": "gone", "reason": failure.Code}) != nil {
			return 1
		}
		return failure.Exit
	}
	return e.watchHost(ctx, r, client, host)
}

func (e *env) watchHost(ctx context.Context, r machineRequest, client watchSource, host machine.Host) int {
	enc := json.NewEncoder(e.out)
	enc.SetEscapeHTML(false)
	write := func(kind string, fields map[string]any) error {
		fields["schema"] = machine.Schema
		fields["type"] = kind
		return enc.Encode(fields)
	}
	gone := func(err error) int {
		if ctx.Err() != nil {
			return 0
		}
		if err == nil || errors.Is(err, admin.ErrDisconnected) {
			err = admin.ErrNoDaemon
		}
		failure := machine.Classify("watch", err)
		if write("gone", map[string]any{"reason": failure.Code}) != nil {
			return 1
		}
		return failure.Exit
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan json.RawMessage, 256)
	ended := make(chan struct{})
	ready := make(chan struct{})
	var streamErr error
	var dropped atomic.Uint64
	go func() {
		streamErr = client.Events(ctx, func(event json.RawMessage) {
			select {
			case events <- event:
			default:
				dropped.Add(1)
			}
		}, func() { close(ready) })
		close(events)
		close(ended)
	}()
	// Closing the HTTP context joins the sole reader; it never waits for stdout.
	defer func() { cancel(); <-ended }()
	select {
	case <-ready:
	case <-ended:
		return gone(streamErr)
	case <-ctx.Done():
		return 0
	}
	if write("hello", map[string]any{"host": host, "interval_ms": r.interval.Milliseconds(), "events": true}) != nil {
		return 1
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	flushDrops := func() error {
		n := dropped.Swap(0)
		if n == 0 {
			return nil
		}
		return write("dropped", map[string]any{"count": n})
	}
	for {
		select {
		case <-ctx.Done():
			return 0
		case event, ok := <-events:
			if err := flushDrops(); err != nil {
				return 1
			}
			if !ok {
				<-ended
				return gone(streamErr)
			}
			if write("event", map[string]any{"data": event}) != nil {
				return 1
			}
		case <-ticker.C:
			raw, err := client.Call(ctx, "GET", "/status", nil)
			if err != nil {
				return gone(err)
			}
			if err = flushDrops(); err != nil {
				return 1
			}
			if write("status", map[string]any{"at": time.Now().UTC(), "data": raw}) != nil {
				return 1
			}
		}
	}
}
