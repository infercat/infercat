package machine

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/infercat/infercat/internal/admin"
)

func TestEnvelopePreservesRouteBytes(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage("\n{ \"future\": [ null, 0 ],\n  \"escaped\": \"<tag>\\u0021\", \"empty\": [] }\n")
	var out bytes.Buffer
	if code := Write(&out, "status", Host{}, raw, nil); code != 0 {
		t.Fatal(code)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope) != 4 || string(envelope["schema"]) != "1" || string(envelope["command"]) != `"status"` || string(envelope["host"]) != `{"version":"","name":""}` {
		t.Fatal(out.String())
	}
	if !bytes.Equal(envelope["data"], bytes.TrimSpace(raw)) {
		t.Fatalf("route changed: %s", out.Bytes())
	}
	d := json.NewDecoder(&out)
	if err := d.Decode(new(any)); err != nil {
		t.Fatal(err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		t.Fatal("extra stdout", err)
	}
}

func TestFailureCodes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, command, code string
		err                 error
		exit                int
	}{
		{"usage", "version", "invalid_arguments", &Failure{Code: "invalid_arguments", Message: "bad flag", Exit: 2}, 2},
		{"confirmation", "keys.revoke", "confirmation_required", &Failure{Code: "confirmation_required", Message: "pass --yes", Exit: 2}, 2},
		{"missing", "status", "host_stopped", admin.ErrNoDaemon, 69},
		{"timeout", "status", "host_not_responding", admin.ErrTimeout, 75},
		{"malformed", "status", "invalid_response", admin.ErrResponse, 1},
		{"key", "keys.pause", "key_not_found", &admin.APIError{Status: 404, Message: "no key k_a"}, 1},
		{"other", "engine", "not_found", &admin.APIError{Status: 404, Message: "missing"}, 1},
		{"conflict", "stored.clear", "conflict", &admin.APIError{Status: 409, Message: "changed"}, 1},
		{"request", "settings.set", "invalid_request", &admin.APIError{Status: 400, Message: "bad value"}, 1},
		{"unauthorized", "status", "unauthorized", &admin.APIError{Status: 401, Message: "denied"}, 1},
		{"forbidden", "keys.limits", "forbidden", &admin.APIError{Status: 403, Message: "local only"}, 1},
		{"admin", "engine", "admin_error", &admin.APIError{Status: 503, Message: "unavailable"}, 1},
		{"local", "version", "command_failed", errors.New("failed"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := Write(&out, tc.command, Host{}, nil, tc.err); code != tc.exit {
				t.Fatal(code)
			}
			var e struct {
				Schema  int
				Command string
				Error   struct{ Code, Message string }
			}
			if err := json.Unmarshal(out.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			if e.Schema != 1 || e.Command != tc.command || e.Error.Code != tc.code || e.Error.Message != tc.err.Error() {
				t.Fatal(out.String())
			}
			if strings.Contains(out.String(), `"host"`) || strings.Contains(out.String(), `"data"`) || strings.Contains(out.String(), `"Exit"`) {
				t.Fatal("error envelope fields", out.String())
			}
		})
	}
}

func TestParseSchemaAndLiteralArguments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args, clean []string
		enabled     bool
		code        string
	}{
		{[]string{"status", "--json"}, []string{"status"}, true, ""},
		{[]string{"--json=1", "status"}, []string{"status"}, true, ""},
		{[]string{"version", "--json=2"}, []string{"version"}, true, "unsupported_schema"},
		{[]string{"version", "--json=false"}, []string{"version"}, true, "unsupported_schema"},
		{[]string{"--json", "--json=1", "version"}, []string{"version"}, true, "invalid_arguments"},
		{[]string{"keys", "add", "--", "--json"}, []string{"keys", "add", "--", "--json"}, false, ""},
		{[]string{"--data-dir", "--json", "version"}, []string{"--data-dir", "--json", "version"}, false, ""},
	} {
		clean, enabled, err := Parse(tc.args)
		if !reflect.DeepEqual(clean, tc.clean) || enabled != tc.enabled {
			t.Fatal(tc.args, clean, enabled)
		}
		if tc.code == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err == nil || Classify("version", err).Code != tc.code {
			t.Fatal(err)
		}
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestInvalidDataAndBrokenOutput(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "{", "{} {}"} {
		var out bytes.Buffer
		if Write(&out, "status", Host{}, json.RawMessage(raw), nil) != 1 || !strings.Contains(out.String(), `"code":"invalid_response"`) {
			t.Fatal(out.String())
		}
	}
	if Write(failedWriter{}, "status", Host{}, json.RawMessage(`{}`), nil) != 1 || Write(failedWriter{}, "status", Host{}, nil, admin.ErrNoDaemon) != 1 {
		t.Fatal("output failure succeeded")
	}
}
