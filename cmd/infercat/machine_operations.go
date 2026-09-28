package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

type operationSpec struct {
	Operation string   `json:"operation"`
	Argv      []string `json:"argv"`
	Routes    []string `json:"routes"`
	Schemas   []int    `json:"schemas"`
}

func operation(name, argv string, routes ...string) operationSpec {
	if routes == nil {
		routes = []string{}
	}
	return operationSpec{name, strings.Fields(argv), routes, []int{1}}
}

var machineOperations = []operationSpec{
	operation("status", "status --json", "GET /status"),
	operation("watch", "watch --json [--interval 2s]", "GET /status", "GET /events"),
	operation("keys.list", "keys list --json", "GET /keys"),
	operation("keys.get", "keys show ID --json", "GET /keys/{id}"),
	operation("keys.add", "keys add --json [limits] -- NAME", "POST /keys"),
	operation("keys.limits", "keys limits ID --json [limits]", "PATCH /keys/{id}"),
	operation("keys.pause", "keys pause ID --json", "POST /keys/{id}/pause"),
	operation("keys.resume", "keys resume ID --json", "POST /keys/{id}/resume"),
	operation("keys.revoke", "keys revoke ID --yes --json", "POST /keys/{id}/revoke"),
	operation("keys.rotate", "keys rotate ID --json", "POST /keys/{id}/rotate"),
	operation("usage", "usage --json --window today|week [--key ID]", "GET /usage"),
	operation("engine", "engine --json", "GET /engine"),
	operation("settings.get", "settings --json", "GET /settings"),
	operation("settings.set", "settings set --json -- k=v…", "PATCH /settings"),
	operation("runs.list", "runs list --json [--key ID]", "GET /runs"),
	operation("stored.get", "stored ID --json", "GET /stored"),
	operation("stored.clear", "stored clear ID --expect CURSOR --yes --json", "GET /stored", "DELETE /stored"),
	operation("remote.status", "remote status --json", "GET /status"),
	operation("remote.on", "remote on --json", "POST /remote/enable"),
	operation("remote.off", "remote off --json", "POST /remote/off"),
	operation("remote.rotate", "remote rotate --json", "POST /remote/rotate"),
	operation("expose.status", "expose --json", "GET /status"),
	operation("expose.on", "expose --on --json", "POST /reload"),
	operation("expose.off", "expose --off --json", "POST /reload"),
	operation("service.status", "service status --json"),
	operation("service.install", "service install --json [--start-at-login=false]"),
	operation("service.uninstall", "service uninstall --json"),
	operation("service.start", "service start --json"),
	operation("service.stop", "service stop --json"),
	operation("service.restart", "service restart --json"),
	operation("service.login", "service login on|off --json"),
	operation("version", "version --json"),
	operation("api", "api --json"),
}

func settingsBody(args []string) (json.RawMessage, error) {
	fields := map[string]any{}
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, badMachine("settings require k=v")
		}
		if _, exists := fields[key]; exists {
			return nil, badMachine("duplicate setting: " + key)
		}
		switch key {
		case "name", "web_url", "console":
			fields[key] = value
		case "slots":
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, badMachine("slots must be an integer")
			}
			fields[key] = n
		case "log_requests":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return nil, badMachine("log_requests must be a boolean")
			}
			fields[key] = b
		default:
			return nil, badMachine("unknown setting: " + key)
		}
	}
	return json.Marshal(fields)
}
