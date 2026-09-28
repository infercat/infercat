// Package machine owns the CLI's versioned envelope and exit codes. Data belongs to the
// host route: optional fields, nulls, ordering and whitespace are never reconstructed here.
package machine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	Code    string `json:"code"`
	Message string `json:"message"`
	Exit    int    `json:"-"`
}

func (e *Failure) Error() string { return e.Message }

// Parse removes --json or --json=1 before the literal-argument delimiter. It never reads
// stdin or terminal state. Unsupported schemas still receive the schema-1 error envelope.
func Parse(args []string) (rest []string, enabled bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if a == "--json" || strings.HasPrefix(a, "--json=") {
			if enabled {
				err = &Failure{"invalid_arguments", "--json may be specified only once", 2}
			}
			enabled = true
			if a != "--json" && a != "--json=1" {
				err = &Failure{"unsupported_schema", "supported JSON schema: 1", 2}
			}
			continue
		}
		rest = append(rest, a)
		// A data-directory value is an argv value, even if its spelling is --json.
		if (a == "--data-dir" || a == "-data-dir") && i+1 < len(args) {
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
	case errors.Is(err, admin.ErrNoDaemon):
		return &Failure{"host_stopped", err.Error(), 69}
	case errors.Is(err, admin.ErrTimeout):
		return &Failure{"host_not_responding", err.Error(), 75}
	case errors.Is(err, admin.ErrResponse):
		return &Failure{"invalid_response", err.Error(), 1}
	}
	var api *admin.APIError
	if errors.As(err, &api) {
		code := "admin_error"
		switch api.Status {
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
		return &Failure{code, api.Message, 1}
	}
	return &Failure{"command_failed", err.Error(), 1}
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
