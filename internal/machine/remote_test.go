package machine

import (
	"bytes"
	"encoding/json"
	"github.com/infercat/infercat/internal/admin"
	"testing"
)

func TestRemoteFailures(t *testing.T) {
	for _, tc := range []struct {
		err   error
		code  string
		exit  int
		retry string
	}{{&admin.APIError{Status: 429, Message: "Too Many Requests", RetryAfter: "60"}, "rate_limited", 75, "60"}, {admin.ErrRemoteUnavailable, "not_available_remotely", 1, ""}} {
		var b bytes.Buffer
		if exit := Write(&b, "keys.add", Host{}, nil, tc.err); exit != tc.exit {
			t.Fatal(exit)
		}
		var v struct{ Error Failure }
		if err := json.Unmarshal(b.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v.Error.Code != tc.code || v.Error.RetryAfter != tc.retry {
			t.Fatal(b.String())
		}
	}
}
