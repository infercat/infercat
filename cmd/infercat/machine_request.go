package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/machine"
)

// Reuse registered limit flags so schema extraction cannot mistake their values for flags.
var machineValueFlags = func() []string {
	names := []string{"window", "key", "expect", "interval", "since"}
	fs := flag.NewFlagSet("limits", flag.ContinueOnError)
	limitFlags(fs)
	fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
	return names
}()

func parseMachineSchema(args []string) ([]string, bool, error) {
	return machine.Parse(args, machineValueFlags...)
}

type machineRequest struct {
	command, dir, method, path string
	body                       json.RawMessage
	args                       []string
	key, expect                string
	interval                   time.Duration
	yes                        bool
}

func badMachine(message string) error {
	return &machine.Failure{Code: "invalid_arguments", Message: message, Exit: 2}
}

func machineOperation(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	command, rest := args[0], args[1:]
	switch command {
	case "keys", "remote", "runs":
		if len(rest) == 0 {
			return command, rest
		}
		command += "." + rest[0]
		rest = rest[1:]
	case "settings":
		command += ".get"
		if len(rest) > 0 && rest[0] == "set" {
			command = "settings.set"
			rest = rest[1:]
		}
	case "stored":
		command += ".get"
		if len(rest) > 0 && rest[0] == "clear" {
			command = "stored.clear"
			rest = rest[1:]
		}
	}
	switch command {
	case "console":
		command = "console.open"
	case "keys.show":
		command = "keys.get"
	case "keys.ls":
		command = "keys.list"
	}
	return command, rest
}

func parseMachine(pre string, args []string, remote ...bool) (machineRequest, error) {
	command, args := machineOperation(args)
	r := machineRequest{command: command, method: "GET", interval: 2 * time.Second}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&r.dir, "data-dir", pre, dataDirUsage)
	if strings.HasPrefix(command, "remote.") {
		fs.Bool("no-qr", false, "")
	}
	var apply func(keys.Limits) keys.Limits
	var force, agent, on, off bool
	window := "today"
	want := 0
	switch command {
	case "status", "version", "api", "engine", "settings.get", "keys.list", "remote.status", "remote.on", "remote.off", "remote.rotate":
	case "keys.add", "keys.limits":
		want = 1
		apply = limitFlags(fs)
		fs.BoolVar(&agent, "agent", false, "")
		if command == "keys.add" {
			fs.BoolVar(&force, "force", false, "")
			fs.Bool("no-qr", false, "")
		}
	case "keys.get", "keys.pause", "keys.resume", "keys.rotate", "keys.revoke", "stored.get", "stored.clear":
		want = 1
		if command == "keys.rotate" {
			fs.Bool("no-qr", false, "")
		}
		if command == "keys.revoke" || command == "stored.clear" {
			fs.BoolVar(&r.yes, "yes", false, "")
		}
		if command == "stored.clear" {
			fs.StringVar(&r.expect, "expect", "", "")
		}
	case "console.open":
		// --print is deliberately not registered: the URL contains an admin secret.
	case "usage":
		fs.StringVar(&window, "window", "today", "")
		fs.StringVar(&r.key, "key", "", "")
	case "runs.list":
		fs.StringVar(&r.key, "key", "", "")
	case "settings.set":
		want = -1
	case "watch":
		fs.DurationVar(&r.interval, "interval", 2*time.Second, "")
	case "expose":
		fs.BoolVar(&on, "on", false, "")
		fs.BoolVar(&off, "off", false, "")
	default:
		return r, badMachine("unknown machine operation: " + command)
	}
	free := command == "keys.add" || command == "settings.set"
	if free {
		i := 0
		for i < len(args) && args[i] != "--" {
			i++
		}
		if i == len(args) {
			return r, badMachine("free text must follow --")
		}
		if err := fs.Parse(args[:i]); err != nil {
			return r, badMachine(err.Error())
		}
		if fs.NArg() != 0 {
			return r, badMachine("free text must follow --")
		}
		r.args = args[i+1:]
	} else {
		ordered, err := reorder(fs, args)
		if err != nil {
			return r, badMachine(err.Error())
		}
		if err = fs.Parse(ordered); err != nil {
			return r, badMachine(err.Error())
		}
		r.args = fs.Args()
	}
	if (want >= 0 && len(r.args) != want) || (want < 0 && len(r.args) == 0) {
		return r, badMachine(fmt.Sprintf("wrong arguments for %s", command))
	}
	for _, arg := range r.args {
		if strings.HasPrefix(strings.TrimSpace(arg), "-") {
			return r, badMachine("names and IDs must not begin with -")
		}
	}
	if command == "keys.revoke" || command == "stored.clear" {
		if !r.yes {
			return r, &machine.Failure{Code: "confirmation_required", Message: "pass --yes to confirm", Exit: 2}
		}
	}
	if command == "stored.clear" && r.expect == "" {
		return r, badMachine("--expect CURSOR is required")
	}
	if r.interval <= 0 {
		return r, badMachine("--interval must be positive")
	}
	var err error
	if len(remote) == 0 || !remote[0] {
		r.dir, err = resolveDataDir(r.dir)
	} else {
		r.dir = ""
	}
	if err != nil {
		return r, err
	}
	if len(r.args) > 0 && (strings.HasPrefix(command, "keys.") && command != "keys.add" || strings.HasPrefix(command, "stored.")) {
		r.key = r.args[0]
	}
	if r.key != "" && !validMachineKey(r.key) {
		return r, badMachine("machine operations require a key ID")
	}
	switch command {
	case "status", "remote.status", "console.open":
		r.path = "/status"
	case "engine":
		r.path = "/engine"
	case "settings.get":
		r.path = "/settings"
	case "settings.set":
		r.method = "PATCH"
		r.path = "/settings"
		r.body, err = settingsBody(r.args)
	case "keys.list":
		r.path = "/keys"
	case "keys.get":
		r.path = "/keys/" + url.PathEscape(r.key)
	case "keys.add":
		r.method = "POST"
		r.path = "/keys"
		body := map[string]any{"name": r.args[0], "limits": apply(keys.Limits{})}
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "force" {
				body["force"] = force
			}
			if f.Name == "agent" {
				body["agent"] = agent
			}
		})
		r.body, err = json.Marshal(body)
	case "keys.limits":
		r.method = "PATCH"
		r.path = "/keys/" + url.PathEscape(r.key)
		body := selectedLimits(fs, apply(keys.Limits{}))
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "agent" {
				body["agent"] = agent
			}
		})
		r.body, err = json.Marshal(body)
	case "keys.pause", "keys.resume", "keys.revoke", "keys.rotate":
		r.method = "POST"
		r.path = "/keys/" + url.PathEscape(r.key) + "/" + strings.TrimPrefix(command, "keys.")
	case "remote.on", "remote.off", "remote.rotate":
		action := strings.TrimPrefix(command, "remote.")
		if action == "on" {
			action = "enable"
		}
		r.method = "POST"
		r.path = "/remote/" + action
	case "usage":
		if window != "today" && window != "week" {
			return r, badMachine("--window must be today or week")
		}
		r.path = "/usage?window=" + window
		if r.key != "" {
			r.path += "&key_id=" + url.QueryEscape(r.key)
		}
	case "runs.list":
		r.path = "/runs"
		if r.key != "" {
			r.path += "?key_id=" + url.QueryEscape(r.key)
		}
	case "stored.get", "stored.clear":
		r.path = "/stored?key_id=" + url.QueryEscape(r.key)
	case "expose":
		if on && off {
			return r, badMachine("use --on or --off")
		}
		r.command = "expose.status"
		r.path = "/status"
		if on || off {
			r.method = "POST"
			r.path = "/reload"
			r.command = "expose.on"
			if off {
				r.command = "expose.off"
			}
		}
	}
	return r, err
}

func validMachineKey(s string) bool {
	if !strings.HasPrefix(s, "k_") || len(s) < 3 || len(s) > 64 {
		return false
	}
	for _, c := range s[2:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func selectedLimits(fs *flag.FlagSet, l keys.Limits) map[string]any {
	raw, _ := json.Marshal(l)
	var all map[string]json.RawMessage
	_ = json.Unmarshal(raw, &all)
	out := map[string]any{}
	fs.Visit(func(f *flag.Flag) {
		name := strings.ReplaceAll(f.Name, "-", "_")
		if value, ok := all[name]; ok {
			out[name] = value
		} else if name == "models" {
			out[name] = nil
		}
	})
	return out
}
