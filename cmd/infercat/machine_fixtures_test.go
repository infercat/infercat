package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/machine"
	"github.com/infercat/infercat/internal/usage"
)

var fixtureTime = time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)

// Construct field-shape fixtures from the same Go payload types as the inventory. These
// are decoder examples, not captured host data; no runtime identity or credential is used.
func contractSample(t reflect.Type, name string, seen map[reflect.Type]bool) reflect.Value {
	v := reflect.New(t).Elem()
	if seen[t] {
		return v
	}
	if t == reflect.TypeOf(time.Time{}) {
		return reflect.ValueOf(fixtureTime)
	}
	if t == reflect.TypeOf(json.RawMessage{}) {
		return reflect.ValueOf(json.RawMessage(`{"fixture":true}`))
	}
	seen[t] = true
	defer delete(seen, t)
	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath == "" {
				v.Field(i).Set(contractSample(f.Type, strings.Split(f.Tag.Get("json"), ",")[0], seen))
			}
		}
	case reflect.Pointer:
		v.Set(reflect.New(t.Elem()))
		v.Elem().Set(contractSample(t.Elem(), name, seen))
	case reflect.Slice:
		v.Set(reflect.MakeSlice(t, 1, 1))
		v.Index(0).Set(contractSample(t.Elem(), name, seen))
	case reflect.Map:
		v.Set(reflect.MakeMap(t))
		v.SetMapIndex(contractSample(t.Key(), "map_key", seen), contractSample(t.Elem(), name, seen))
	case reflect.String:
		value := "example"
		switch name {
		case "id", "key_id":
			value = "k_example"
		case "name":
			value = "Example host"
		case "version":
			value = "0.1.6-fixture"
		case "mode":
			value = "host"
		case "status":
			value = "active"
		case "state":
			value = "completed"
		case "kind":
			value = "image"
		case "class":
			value = "tokens"
		case "unit":
			value = "tokens"
		case "date":
			value = "2026-09-28"
		case "invite":
			value = "synthetic-invite-not-valid-for-connection"
		case "link", "url", "web_url", "default_web_url":
			value = "https://example.invalid/"
		}
		v.SetString(value)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if name == "requests" {
			v.SetInt(2)
		} else {
			v.SetInt(1)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	}
	return v
}

func TestMachineVendorFixtures(t *testing.T) {
	dir := filepath.Join("testdata", "contract")
	expected := map[string]bool{}
	write := func(name string, body []byte) {
		t.Helper()
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, bytes.TrimSpace(body), "", "  "); err != nil {
			t.Fatal(name, err)
		}
		formatted.WriteByte('\n')
		path := filepath.Join(dir, name+".json")
		expected[filepath.Base(path)] = true
		if *updateCLI {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, formatted.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, formatted.Bytes()) {
			t.Fatalf("%s differs; regenerate with make cli-contract (%v)", path, err)
		}
	}
	for _, op := range machineOperations {
		for _, full := range []bool{false, true} {
			typ := payloadType(op.Operation)
			v := reflect.Zero(typ)
			variant := "empty"
			if full {
				v = contractSample(typ, "data", map[reflect.Type]bool{})
				variant = "populated"
			} else if typ.Kind() == reflect.Slice {
				v = reflect.MakeSlice(typ, 0, 0)
			}
			if !full && op.Operation == "version" {
				v = reflect.New(typ).Elem()
				f := v.FieldByName("Schemas")
				f.Set(reflect.MakeSlice(f.Type(), 0, 0))
			}
			payload := v.Interface()
			if op.Operation == "api" && full {
				payload = machineOperations
			}
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(op.Operation, err)
			}
			var envelope bytes.Buffer
			if op.Operation == "watch" {
				body, err := json.Marshal(map[string]any{"schema": machine.Schema, "type": "status", "at": fixtureTime, "data": json.RawMessage(data)})
				if err != nil {
					t.Fatal(err)
				}
				write("watch.status."+variant, body)
			} else {
				if machine.Write(&envelope, op.Operation, machine.Host{Version: "0.1.6-fixture", Name: "Example host"}, data, nil) != 0 {
					t.Fatal(op.Operation)
				}
				write(op.Operation+"."+variant, envelope.Bytes())
			}
		}
	}
	for name, value := range map[string]any{
		"watch.hello":   map[string]any{"schema": machine.Schema, "type": "hello", "host": machine.Host{Version: "0.1.6-fixture", Name: "Example host"}, "interval_ms": 2000, "events": true},
		"watch.event":   map[string]any{"schema": machine.Schema, "type": "event", "data": usage.Event{TS: fixtureTime, KeyID: "k_example", Endpoint: "/v1/chat/completions", Status: 200, PromptTokens: 3, CompletionTokens: 7}},
		"watch.dropped": map[string]any{"schema": machine.Schema, "type": "dropped", "count": 4},
		"watch.gone":    map[string]any{"schema": machine.Schema, "type": "gone", "reason": "host_stopped"},
	} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		write(name, body)
	}
	var failure bytes.Buffer
	machine.Write(&failure, "keys.revoke", machine.Host{}, nil, &machine.Failure{Code: "confirmation_required", Message: "pass --yes to confirm", Exit: 2})
	write("error", failure.Bytes())
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if !expected[filepath.Base(path)] {
			t.Errorf("stale contract fixture: %s", path)
		}
	}
}
