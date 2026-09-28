package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/bridge"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/machine"
	"github.com/infercat/infercat/internal/product"
	runstate "github.com/infercat/infercat/internal/run"
)

func (e *env) machineRead(ctx context.Context, args []string) (int, bool) {
	clean, enabled, err := parseMachineSchema(args)
	if !enabled {
		return 0, false
	}
	pre, rest, splitErr := splitGlobal(clean)
	// Service owns its platform-specific execution and shares internal/machine's envelope.
	if len(rest) > 0 && rest[0] == "service" {
		return 0, false
	}
	command, _ := machineOperation(rest)
	if err == nil && splitErr != nil {
		err = badMachine(splitErr.Error())
	}
	var r machineRequest
	if err == nil {
		r, err = parseMachine(pre, rest, e.remoteTarget.Remote())
		command = r.command
	}
	if err != nil {
		return machine.Write(e.out, command, machine.Host{}, nil, err), true
	}
	if e.remoteTarget.Remote() {
		if err := remoteMachineRequest(r); err != nil {
			return machine.Write(e.out, command, machine.Host{}, nil, err), true
		}
	}
	if command == "watch" {
		return e.machineWatch(ctx, r), true
	}
	host, data, err := e.executeMachine(ctx, r)
	return machine.Write(e.out, command, host, data, err), true
}

func (e *env) hostClient(ctx context.Context, dir string) (*admin.Client, machine.Host, json.RawMessage, error) {
	client, err := e.adminClient(ctx, dir)
	if err != nil {
		return nil, machine.Host{}, nil, err
	}
	raw, err := client.Call(ctx, "GET", "/status", nil)
	if err != nil {
		client.Close()
		return nil, machine.Host{}, nil, err
	}
	var host machine.Host
	if err = json.Unmarshal(raw, &host); err != nil {
		client.Close()
		return nil, host, nil, admin.ErrResponse
	}
	return client, host, raw, nil
}

func (e *env) executeMachine(ctx context.Context, r machineRequest) (machine.Host, json.RawMessage, error) {
	own := machine.Host{Version: product.Version}
	if r.command == "version" {
		data, err := json.Marshal(struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			Date    string `json:"date"`
			Schemas []int  `json:"schemas"`
		}{product.Version, product.Commit, product.Date, []int{machine.Schema}})
		return own, data, err
	}
	if r.command == "api" {
		data, err := json.Marshal(machineOperations)
		return own, data, err
	}
	client, host, status, err := e.hostClient(ctx, r.dir)
	if !e.remoteTarget.Remote() && errors.Is(err, admin.ErrNoDaemon) && (r.command == "keys.list" || r.command == "keys.add") {
		return e.offlineMachine(ctx, r)
	}
	if err != nil {
		return host, nil, err
	}
	defer client.Close()
	if r.command == "console.open" {
		raw, err := e.machineConsole(ctx, r.dir, status)
		return host, raw, err
	}
	if r.path == "/status" {
		return host, status, nil
	}
	if r.command == "stored.clear" {
		raw, err := client.Call(ctx, "GET", r.path, nil)
		if err != nil {
			return host, nil, err
		}
		var stored runstate.StoredData
		if err = json.Unmarshal(raw, &stored); err != nil {
			return host, nil, admin.ErrResponse
		}
		if stored.Cursor != r.expect {
			return host, nil, &admin.APIError{Status: 409, Message: "stored data changed; refresh before confirming"}
		}
		r.body, err = json.Marshal(runstate.ClearExpectation{Cursor: r.expect, Terminal: stored.Terminal, Images: stored.ClearImages, Cleanup: stored.RetryCleanup})
		if err != nil {
			return host, nil, err
		}
		r.method = "DELETE"
	}
	if r.command == "expose.on" || r.command == "expose.off" {
		if err := bridge.SetEnabled(r.dir, r.command == "expose.on"); err != nil {
			return host, nil, err
		}
	}
	raw, err := client.Call(ctx, r.method, r.path, r.body)
	return host, raw, err
}

// The stopped-host exceptions use the same local handlers and response shapes. They never
// run after a dispatched call failed, and a stopped host's identity must already exist to mint.
func (e *env) offlineMachine(ctx context.Context, r machineRequest) (machine.Host, json.RawMessage, error) {
	host := machine.Host{Version: product.Version}
	cfg, err := loadConfig(r.dir)
	if err != nil {
		return host, nil, err
	}
	host.Name = hostDisplayName(cfg.Name)
	store, err := keys.NewFileStore(r.dir)
	if err != nil {
		return host, nil, err
	}
	addr := ""
	if r.command == "keys.add" {
		addr, err = e.plat.savedAddr(r.dir)
		if errors.Is(err, os.ErrNotExist) {
			return host, nil, errors.New("this host has no address yet — run infercat serve once first")
		}
		if err != nil {
			return host, nil, err
		}
		if addr == "" {
			return host, nil, errors.New("this host has no address yet — run infercat serve once first")
		}
	}
	h := e.consoleAPI(store, addr, nil, consoleSettings{DataDir: r.dir})
	req := httptest.NewRequest(r.method, r.path, strings.NewReader(string(r.body))).WithContext(ctx)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code >= 400 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(out.Body.Bytes(), &failure)
		if failure.Error == "" {
			failure.Error = strings.TrimSpace(out.Body.String())
		}
		return host, nil, &admin.APIError{Status: out.Code, Message: failure.Error}
	}
	return host, json.RawMessage(out.Body.Bytes()), nil
}

// New inspection verbs also work at a terminal; existing human renderers are unchanged.
func (e *env) cmdInspect(ctx context.Context, pre, command string, args []string) error {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" {
			e.machineHelp(command)
			return nil
		}
	}
	r, err := parseMachine(pre, append([]string{command}, args...), e.remoteTarget.Remote())
	if err != nil {
		if machine.Classify(r.command, err).Exit == 2 {
			fmt.Fprintln(e.errw, err)
			return errUsage
		}
		return err
	}
	_, body, err := e.executeMachine(ctx, r)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(e.out, strings.TrimSpace(string(body)))
	return err
}

func (e *env) machineHelp(command string) {
	fmt.Fprintln(e.out, "Usage:")
	for _, op := range machineOperations {
		if op.Operation == command || strings.HasPrefix(op.Operation, command+".") {
			fmt.Fprintln(e.out, "  infercat "+strings.Join(op.Argv, " "))
		}
	}
	fmt.Fprintln(e.out, "\nContract and examples: docs/CLI-JSON.md")
}
