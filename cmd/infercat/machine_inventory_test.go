package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/machine"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

func TestMachineOperationDocs(t *testing.T) {
	code, envelope, _ := machineCall(t, context.Background(), "api", "--json")
	if code != 0 {
		t.Fatal(code)
	}
	var operations []operationSpec
	if err := json.Unmarshal(envelope["data"], &operations); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"CLI-JSON.md", "CLI-JSON.zh-CN.md"} {
		header := "| Operation | argv after `infercat` | Admin route(s) | Schema |\n|---|---|---|---|\n"
		if strings.Contains(name, "zh-CN") {
			header = "| 操作 | `infercat` 后的 argv | 管理路由 | Schema |\n|---|---|---|---|\n"
		}
		var table strings.Builder
		table.WriteString(header)
		for _, op := range operations {
			routes := strings.Join(op.Routes, "; ")
			if routes == "" {
				routes = "—"
			}
			fmt.Fprintf(&table, "| `%s` | `%s` | %s | 1 |\n", op.Operation, strings.ReplaceAll(strings.Join(op.Argv, " "), "|", `\|`), routes)
		}
		path := filepath.Join("..", "..", "docs", name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		start, end := "<!-- cli-operations:start -->\n", "<!-- cli-operations:end -->"
		before, rest, ok := strings.Cut(string(raw), start)
		if !ok {
			t.Fatal("missing start marker")
		}
		_, after, ok := strings.Cut(rest, end)
		if !ok {
			t.Fatal("missing end marker")
		}
		want := before + start + table.String() + end + after
		if *updateCLI {
			if err := os.WriteFile(path, []byte(want), 0644); err != nil {
				t.Fatal(err)
			}
		} else if string(raw) != want {
			t.Fatalf("%s operation table differs from api --json; run make cli-contract", name)
		}
	}
}

func payloadType(op string) reflect.Type {
	if strings.HasPrefix(op, "service.") {
		return reflect.TypeOf(serviceStatus{})
	}
	var value any
	switch op {
	case "status", "remote.status", "expose.status", "watch":
		value = admin.Status{}
	case "keys.list":
		value = []consoleKey{}
	case "keys.get":
		value = consoleKey{}
	case "keys.add", "keys.rotate":
		value = inviteJSON{}
	case "keys.limits", "keys.pause", "keys.resume", "keys.revoke", "expose.on", "expose.off":
		value = struct {
			OK bool `json:"ok"`
		}{}
	case "remote.on", "remote.off", "remote.rotate":
		value = admin.RemoteResult{}
	case "usage":
		value = usage.Report{}
	case "console.open":
		value = struct {
			Opened bool `json:"opened"`
		}{}
	case "engine":
		value = upstream.Info{}
	case "settings.get", "settings.set":
		value = consoleSettings{}
	case "runs.list":
		value = admin.RunsResult{}
	case "stored.get":
		value = runstate.StoredData{}
	case "stored.clear":
		value = admin.StoredClearResult{}
	case "api":
		value = []operationSpec{}
	case "version":
		value = struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			Date    string `json:"date"`
			Schemas []int  `json:"schemas"`
		}{}
	default:
		panic("no payload type for " + op)
	}
	return reflect.TypeOf(value)
}

func wireType(t reflect.Type) string {
	if t == reflect.TypeOf(time.Time{}) {
		return "string(timestamp)"
	}
	if t == reflect.TypeOf(json.RawMessage{}) {
		return "json"
	}
	switch t.Kind() {
	case reflect.Pointer:
		return wireType(t.Elem()) + "?"
	case reflect.Slice, reflect.Array:
		return "array<" + wireType(t.Elem()) + ">"
	case reflect.Map:
		return "map<" + wireType(t.Elem()) + ">"
	case reflect.Struct:
		return "object"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Interface:
		return "json"
	default:
		return "integer"
	}
}

func inventory(op string, t reflect.Type) []string {
	var lines []string
	var walk func(reflect.Type, string, string, map[reflect.Type]bool)
	walk = func(t reflect.Type, path, presence string, seen map[reflect.Type]bool) {
		lines = append(lines, fmt.Sprintf("%s %s %s %s", op, path, wireType(t), presence))
		if t == reflect.TypeOf(time.Time{}) || t == reflect.TypeOf(json.RawMessage{}) {
			return
		}
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if seen[t] {
			return
		}
		next := map[reflect.Type]bool{}
		for k, v := range seen {
			next[k] = v
		}
		next[t] = true
		switch t.Kind() {
		case reflect.Slice, reflect.Array:
			walk(t.Elem(), path+"[]", "element", next)
		case reflect.Map:
			walk(t.Elem(), path+"{}", "value", next)
		case reflect.Struct:
			for _, f := range reflect.VisibleFields(t) {
				if f.PkgPath != "" {
					continue
				}
				parts := strings.Split(f.Tag.Get("json"), ",")
				name := parts[0]
				if name == "-" || f.Anonymous && name == "" {
					continue
				}
				// Fields promoted in Go through a JSON-named embedded object stay nested.
				parent := t
				nested := false
				for _, index := range f.Index[:len(f.Index)-1] {
					ancestor := parent.Field(index)
					tag := strings.Split(ancestor.Tag.Get("json"), ",")[0]
					if tag != "" {
						nested = true
						break
					}
					parent = ancestor.Type
					for parent.Kind() == reflect.Pointer {
						parent = parent.Elem()
					}
				}
				if nested {
					continue
				}
				if name == "" {
					name = f.Name
				}

				p := "always"
				if slices.Contains(parts, "omitzero") || slices.Contains(parts, "omitempty") && f.Type.Kind() != reflect.Struct {
					p = "omitempty"
				}
				if op != "api" && op != "version" && p == "always" && (f.Type.Kind() == reflect.Slice || f.Type.Kind() == reflect.Map || f.Type.Kind() == reflect.Pointer) {
					p += "+nullable"
				}
				walk(f.Type, path+"."+name, p, next)
			}
		}
	}
	walk(t, "data", "always", map[reflect.Type]bool{})
	slices.Sort(lines)
	return slices.Compact(lines)
}

func checkInventory(previous, current string) error {
	fields := map[string]string{}
	for _, line := range strings.Split(current, "\n") {
		p := strings.Fields(line)
		if len(p) == 4 {
			fields[p[0]+" "+p[1]] = p[2] + " " + p[3]
		}
	}
	for _, line := range strings.Split(previous, "\n") {
		p := strings.Fields(line)
		if len(p) != 4 {
			continue
		}
		key := p[0] + " " + p[1]
		want := p[2] + " " + p[3]
		if got, ok := fields[key]; !ok || got != want {
			return fmt.Errorf("schema 1 field removed or changed: %s (%s -> %s)", key, want, got)
		}
	}
	return nil
}

func TestMachineFieldInventory(t *testing.T) {
	lines := []string{"# CLI JSON schema 1: operation path type presence; route null/omission fidelity is intentional."}
	for _, op := range machineOperations {
		lines = append(lines, inventory(op.Operation, payloadType(op.Operation))...)
	}
	for _, line := range inventory("watch.event", reflect.TypeOf(usage.Event{})) {
		if !strings.Contains(line, " data.prompt ") && !strings.Contains(line, " data.completion ") {
			lines = append(lines, line)
		}
	}
	lines = append(lines, inventory("error", reflect.TypeOf(machine.Failure{}))...)
	generated := strings.Join(lines, "\n") + "\n"
	path := filepath.Join("testdata", "cli-schema-1.txt")
	if *updateCLI {
		if err := os.WriteFile(path, []byte(generated), 0644); err != nil {
			t.Fatal(err)
		}
	}
	baseline, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkInventory(string(baseline), generated); err != nil {
		t.Fatal(err)
	}
	if string(baseline) != generated {
		t.Fatal("field inventory changed; regenerate with make cli-contract, then review additions")
	}
	// The previous commit guards against regenerating away a breaking change. CI fetches
	// the parent; the initial schema creation has no prior file and is the sole exception.
	previous, err := osexec.Command("git", "show", "HEAD^:cmd/infercat/testdata/cli-schema-1.txt").Output()
	if err == nil {
		if err := checkInventory(string(previous), generated); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMachineInventoryRejectsBreakingEdits(t *testing.T) {
	old := "status data.count integer always\n"
	for _, bad := range []string{"", "status data.renamed integer always\n", "status data.count string always\n", "status data.count integer omitempty\n"} {
		if checkInventory(old, bad) == nil {
			t.Fatal("breaking change accepted", bad)
		}
	}
	if err := checkInventory(old, old+"status data.extra string omitempty\n"); err != nil {
		t.Fatal(err)
	}
}
