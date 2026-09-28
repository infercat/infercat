package machine

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRemoteSelectorPreservesOperandsAndRedacts(t *testing.T) {
	for _, args := range [][]string{{"--host", "sensitive-code", "status", "--json"}, {"status", "--json", "--host=sensitive-code"}} {
		rest, target, err := ParseTarget(args, nil)
		if err != nil || !target.Remote() || !reflect.DeepEqual(rest, []string{"status", "--json"}) {
			t.Fatal(rest, err)
		}
		if strings.Contains(fmt.Sprint(target)+fmt.Sprintf("%#v", target), "sensitive") {
			t.Fatal("code leaked")
		}
	}
	for _, args := range [][]string{{"status", "--host"}, {"--host=x", "--host-file=secret-path", "status"}, {"--host-file="}} {
		_, _, err := ParseTarget(args, nil)
		if err == nil || strings.Contains(err.Error(), "secret-path") {
			t.Fatal(err)
		}
	}
	args := []string{"keys", "add", "--models", "--host", "--", "--host-file", "literal"}
	rest, target, err := ParseTarget(args, map[string]bool{"--models": true})
	if err != nil || target.Remote() || !reflect.DeepEqual(rest, args) {
		t.Fatal(rest, err)
	}
}

func TestSchemaPreservesHostAndSharedFlagValues(t *testing.T) {
	for _, flag := range []string{"--host", "--host-file", "--models", "-models", "--data-dir"} {
		args := []string{"status", flag, "--json", "--json=1"}
		rest, enabled, err := Parse(args, "models")
		if err != nil || !enabled || !reflect.DeepEqual(rest, args[:3]) {
			t.Fatal(flag, rest, enabled, err)
		}
	}
}
