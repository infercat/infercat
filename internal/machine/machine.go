// Package machine owns the CLI's versioned envelope and exit codes. Data belongs to the
// host route: optional fields, nulls, ordering and whitespace are never reconstructed here.
package machine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/infercat/infercat/internal/admin"
)

const Schema = 1

type Host struct {
	Version string `json:"version"`
	Name    string `json:"name"`
}

// Failure also lets local operations (such as service management) use the same exit policy.
type Failure struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Exit       int    `json:"-"`
	RetryAfter string `json:"retry_after,omitempty"`
}

func (e *Failure) Error() string { return e.Message }

// Parse removes --json or --json=1 before the literal-argument delimiter. It never reads
// stdin or terminal state. valueFlags names additional value-taking flags without dashes.
// Unsupported schemas still receive the schema-1 error envelope.
func Parse(args []string, valueFlags ...string) (rest []string, enabled bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if a == "--json" || strings.HasPrefix(a, "--json=") {
			if enabled {
				err = &Failure{Code: "invalid_arguments", Message: "--json may be specified only once", Exit: 2}
			}
			enabled = true
			if a != "--json" && a != "--json=1" {
				err = &Failure{Code: "unsupported_schema", Message: "supported JSON schema: 1", Exit: 2}
			}
			continue
		}
		rest = append(rest, a)
		// Flag and selector values remain literal, even when spelled --json.
		name := strings.TrimLeft(a, "-")
		if strings.HasPrefix(a, "-") && (name == "data-dir" || name == "host" || name == "host-file" || slices.Contains(valueFlags, name)) && i+1 < len(args) {
			i++
			rest = append(rest, args[i])
		}
	}
	return
}

// Classify maps transport and route failures, never the English message's spelling.
func Classify(command string, err error) *Failure {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure
	}
	switch {
	case errors.Is(err, admin.ErrRemoteUnavailable):
		return &Failure{Code: "not_available_remotely", Message: err.Error(), Exit: 1}
	case errors.Is(err, admin.ErrNoDaemon):
		return &Failure{Code: "host_stopped", Message: err.Error(), Exit: 69}
	case errors.Is(err, admin.ErrTimeout):
		return &Failure{Code: "host_not_responding", Message: err.Error(), Exit: 75}
	case errors.Is(err, admin.ErrResponse):
		return &Failure{Code: "invalid_response", Message: err.Error(), Exit: 1}
	}
	var api *admin.APIError
	if errors.As(err, &api) {
		code := "admin_error"
		switch api.Status {
		case http.StatusTooManyRequests:
			return &Failure{Code: "rate_limited", Message: api.Message, Exit: 75, RetryAfter: api.RetryAfter}
		case http.StatusBadRequest:
			code = "invalid_request"
		case http.StatusUnauthorized:
			code = "unauthorized"
		case http.StatusForbidden:
			code = "forbidden"
		case http.StatusNotFound:
			code = "not_found"
			if strings.HasPrefix(command, "keys.") {
				code = "key_not_found"
			}
		case http.StatusConflict:
			code = "conflict"
		}
		return &Failure{Code: code, Message: api.Message, Exit: 1}
	}
	return &Failure{Code: "command_failed", Message: err.Error(), Exit: 1}
}

// Write emits exactly one object. Raw data is copied byte for byte, apart from whitespace
// outside its JSON value. json.Marshal on an enclosing RawMessage would compact it.
func Write(w io.Writer, command string, host Host, data json.RawMessage, err error) int {
	if err == nil && !json.Valid(data) {
		err = admin.ErrResponse
	}
	if err != nil {
		failure := Classify(command, err)
		if json.NewEncoder(w).Encode(struct {
			Schema  int      `json:"schema"`
			Command string   `json:"command"`
			Error   *Failure `json:"error"`
		}{Schema, command, failure}) != nil {
			return 1
		}
		return failure.Exit
	}
	cmd, _ := json.Marshal(command)
	h, _ := json.Marshal(host)
	if _, err := fmt.Fprintf(w, "{\"schema\":%d,\"command\":%s,\"host\":%s,\"data\":%s}\n", Schema, cmd, h, strings.TrimSpace(string(data))); err != nil {
		return 1
	}
	return 0
}
