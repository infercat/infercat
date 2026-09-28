package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/infercat/infercat/internal/machine"
)

func TestMachineArgumentsCannotBecomeFlagsOrConfirmActions(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"keys", "add", "alice"}, {"keys", "add", "alice", "--"}, {"keys", "add", "--", "-alice"},
		{"keys", "add", "--", "  -alice"},
		{"keys", "pause", "alice"}, {"usage", "--key=-alice"}, {"keys", "revoke", "k_a"},
		{"stored", "clear", "k_a", "--expect", "old"}, {"stored", "clear", "k_a", "--yes"},
		{"settings", "set", "name=alice"}, {"settings", "set", "--", "slots=x"}, {"settings", "set", "--", "other=1"}, {"settings", "set", "--", "name=a", "name=b"},
		{"watch", "--interval", "0"}, {"expose", "--on", "--off"},
	} {
		r, err := parseMachine(t.TempDir(), args)
		if err == nil || machine.Classify(r.command, err).Exit != 2 {
			t.Fatal(args, err)
		}
	}
	for _, args := range [][]string{{"keys", "revoke", "k_a"}, {"stored", "clear", "k_a", "--expect", "old"}} {
		r, err := parseMachine(t.TempDir(), args)
		if err == nil || machine.Classify(r.command, err).Code != "confirmation_required" {
			t.Fatal(args, err)
		}
	}
	r, err := parseMachine(t.TempDir(), []string{"keys", "add", "--", "Alice --force; $(ignored)"})
	if err != nil || !strings.Contains(string(r.body), `Alice --force; $(ignored)`) {
		t.Fatal(string(r.body), err)
	}
}

func TestMachineLimitsOnlyCarrySelectedExactValues(t *testing.T) {
	t.Parallel()
	r, err := parseMachine(t.TempDir(), []string{"keys", "limits", "k_a", "--daily-tokens", "9007199254740993", "--models=", "--agent=false"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(r.body, &fields) != nil || len(fields) != 3 || string(fields["daily_tokens"]) != "9007199254740993" || string(fields["models"]) != "null" || string(fields["agent"]) != "false" {
		t.Fatal(string(r.body))
	}
}

func TestMachineSchemaLeavesFlagValuesLiteral(t *testing.T) {
	t.Parallel()
	for _, name := range machineValueFlags {
		for _, prefix := range []string{"-", "--"} {
			args := []string{"usage", prefix + name, "--json"}
			clean, enabled, err := parseMachineSchema(args)
			if err != nil || enabled || !reflect.DeepEqual(clean, args) {
				t.Fatal(args, clean, enabled, err)
			}
			clean, enabled, err = parseMachineSchema(append([]string{"--json=1"}, args...))
			if err != nil || !enabled || !reflect.DeepEqual(clean, args) {
				t.Fatal(args, clean, enabled, err)
			}
		}
	}
}
