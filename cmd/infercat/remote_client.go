package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/machine"
)

func remoteMachineRequest(r machineRequest) error {
	switch r.command {
	case "version", "api", "console.open":
		return admin.ErrRemoteUnavailable
	}
	if r.command == "watch" {
		if r.interval < time.Second {
			return badMachine("remote watch interval must be at least 1s")
		}
		return nil
	}
	return admin.ValidateRemoteCall(r.method, r.path, r.body)
}
func (e *env) remoteError(args []string, err error) int {
	clean, asJSON, _ := parseMachineSchema(args)
	_, rest, _ := splitGlobal(clean)
	op, _ := machineOperation(rest)
	if asJSON {
		return machine.Write(e.out, op, machine.Host{}, nil, err)
	}
	fmt.Fprintln(e.errw, err)
	return machine.Classify(op, err).Exit
}
func (e *env) runRemote(ctx context.Context, args []string, err error) int {
	if err != nil {
		return e.remoteError(args, err)
	}
	clean, _, schemaErr := parseMachineSchema(args)
	pre, rest, splitErr := splitGlobal(clean)
	if schemaErr != nil {
		return e.remoteError(args, schemaErr)
	}
	if splitErr != nil {
		return e.remoteError(args, badMachine("invalid remote arguments"))
	}
	op, _ := machineOperation(rest)
	switch op {
	case "status", "watch", "engine", "usage", "keys.list", "keys.get", "keys.add", "keys.limits", "keys.pause", "keys.resume", "keys.revoke", "keys.rotate", "remote.status", "remote.on", "remote.off", "remote.rotate", "settings.get", "settings.set", "runs.list", "stored.get", "stored.clear", "expose":
	default:
		return e.remoteError(args, admin.ErrRemoteUnavailable)
	}
	// Refuse local-only options before even opening a credential file. Do not scan literal operands.
	for i := 1; i < len(rest); i++ {
		a := rest[i]
		if a == "--" {
			break
		}
		name, _, has := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if strings.HasPrefix(a, "-") {
			if (op == "usage" && (name == "key" || name == "since")) || strings.HasPrefix(op, "keys.") && (name == "force" || name == "agent") || op == "expose" && (name == "on" || name == "off" || name == "register") {
				return e.remoteError(args, admin.ErrRemoteUnavailable)
			}
			if !has {
				for _, v := range append([]string{"data-dir"}, machineValueFlags...) {
					if name == v {
						i++
						break
					}
				}
			}
		}
	}
	if e.adminClientFactory == nil {
		e.adminClientFactory = func(ctx context.Context, _ string) (*admin.Client, error) {
			code, err := e.remoteTarget.Code()
			if err != nil {
				return nil, err
			}
			return admin.NewRemoteClient(ctx, code)
		}
	}
	if code, handled := e.machineRead(ctx, args); handled {
		return code
	}
	if op == "settings.set" {
		for _, a := range rest[2:] {
			if strings.HasPrefix(strings.ToLower(a), "console=") {
				return e.remoteError(args, admin.ErrRemoteUnavailable)
			}
		}
	}
	switch rest[0] {
	case "status":
		err = e.cmdStatus(ctx, pre, rest[1:])
	case "keys":
		err = e.cmdKeys(ctx, pre, rest[1:])
	case "remote":
		err = e.cmdRemote(ctx, pre, rest[1:])
	case "usage":
		err = e.cmdUsage(ctx, pre, rest[1:])
	case "expose":
		err = e.cmdInspect(ctx, pre, "expose", rest[1:])
	default:
		err = e.cmdInspect(ctx, pre, rest[0], rest[1:])
	}
	if err != nil {
		return e.remoteError(args, err)
	}
	return 0
}
func (e *env) adminDir(dir string) (string, error) {
	if e.remoteTarget.Remote() {
		return "", nil
	}
	return resolveDataDir(dir)
}

// Remote watch has one session and no events endpoint. Delay begins after each completed call.
func (e *env) watchRemote(ctx context.Context, r machineRequest, client watchSource, host machine.Host, initial json.RawMessage) int {
	enc := json.NewEncoder(e.out)
	if enc.Encode(map[string]any{"schema": 1, "type": "hello", "host": host, "interval_ms": r.interval.Milliseconds(), "events": false}) != nil {
		return 1
	}
	raw := initial
	for {
		if enc.Encode(map[string]any{"schema": 1, "type": "status", "at": time.Now().UTC(), "data": raw}) != nil {
			return 1
		}
		timer := time.NewTimer(r.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0
		case <-timer.C:
		}
		var err error
		raw, err = client.Call(ctx, "GET", "/status", nil)
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			return e.remoteWatchGone(err)
		}
	}
}
func (e *env) remoteWatchGone(err error) int {
	f := machine.Classify("watch", err)
	if json.NewEncoder(e.out).Encode(map[string]any{"schema": 1, "type": "gone", "reason": f.Code, "error": f}) != nil {
		return 1
	}
	return f.Exit
}
