package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/infercat/infercat/internal/tunnel"
	"github.com/tailscale/tailcat"
)

var ErrRemoteUnavailable = errors.New("not available remotely")
var ErrAdminCode = errors.New("invalid admin code (expected ia1 address and secret)")

// ParseAdminCode deliberately never includes input or parser diagnostics in errors.
func ParseAdminCode(code string) (addr, secret string, err error) {
	if len(code) > 16<<10 {
		return "", "", ErrAdminCode
	}
	p := strings.Split(strings.TrimSpace(code), ".")
	if len(p) != 3 || p[0] != "ia1" || len(p[2]) != 43 {
		return "", "", ErrAdminCode
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(p[2])
	if e != nil || len(raw) != 32 {
		return "", "", ErrAdminCode
	}
	if _, e = tailcat.ParseAddr(tailcat.Addr(p[1])); e != nil {
		return "", "", ErrAdminCode
	}
	return p[1], p[2], nil
}

type remoteSession interface {
	Open(context.Context) (net.Conn, error)
	Close() error
}
type remoteDial func(context.Context, string) (remoteSession, error)

// NewRemoteClient owns one tunnel for the command's lifetime. No proxy or local identity is saved.
func NewRemoteClient(ctx context.Context, code string) (*Client, error) {
	return newRemoteClient(ctx, code, func(ctx context.Context, addr string) (remoteSession, error) {
		return tunnel.Dial(ctx, addr, tunnel.ClientOptions{})
	})
}
func newRemoteClient(ctx context.Context, code string, dial remoteDial) (*Client, error) {
	addr, secret, err := ParseAdminCode(code)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	s, err := dial(ctx, addr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, callError(ctx.Err())
		}
		// Relay errors may contain the PSK-bearing address; never print them.
		return nil, errors.New("could not establish the remote admin tunnel")
	}
	tr := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return s.Open(ctx) }}
	hc := &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Client{http: hc, base: "http://infercat/console/api", token: secret, remote: true, close: s.Close}, nil
}
func (c *Client) Remote() bool { return c.remote }
func (c *Client) Close() error {
	c.http.CloseIdleConnections()
	if c.close != nil {
		return c.close()
	}
	return nil
}
func (c *Client) callError(err error) error {
	out := callError(err)
	if c.remote && errors.Is(out, ErrNoDaemon) {
		return ErrResponse
	}
	return out
}

// Mirror the existing console capability boundary, not a second host policy. Refuse before I/O.
// ValidateRemoteCall checks a fully parsed operation before the CLI constructs a tunnel.
func ValidateRemoteCall(method, path string, body json.RawMessage) error {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawPath != "" || strings.ContainsAny(path, "\r\n#") {
		return ErrRemoteUnavailable
	}
	p := u.Path
	allowed := false
	switch p {
	case "/status", "/engine", "/usage", "/runs":
		allowed = method == "GET"
	case "/keys":
		allowed = method == "GET" || method == "POST"
	case "/settings":
		allowed = method == "GET" || method == "PATCH"
	case "/stored":
		allowed = method == "GET" || method == "DELETE"
	case "/remote/enable", "/remote/rotate", "/remote/off":
		allowed = method == "POST"
	default:
		parts := strings.Split(p, "/")
		if len(parts) >= 3 && parts[1] == "keys" && remoteKeyID(parts[2]) {
			if len(parts) == 3 {
				allowed = method == "GET" || method == "PATCH"
			}
			if len(parts) == 4 && method == "POST" {
				switch parts[3] {
				case "pause", "resume", "revoke", "rotate":
					allowed = true
				}
			}
		}
	}
	if !allowed {
		return ErrRemoteUnavailable
	}
	if u.RawQuery != "" {
		q, e := url.ParseQuery(u.RawQuery)
		if e != nil || len(q) != 1 {
			return ErrRemoteUnavailable
		}
		if p == "/usage" && len(q["window"]) == 1 && (q.Get("window") == "today" || q.Get("window") == "week") {
		} else if (p == "/runs" || p == "/stored") && len(q["key_id"]) == 1 && remoteKeyID(q.Get("key_id")) {
		} else {
			return ErrRemoteUnavailable
		}
	}
	if len(body) > 16<<10 {
		return errors.New("remote admin request exceeds 16 KiB")
	}
	if len(body) > 0 {
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil || fields == nil {
			return errors.New("invalid admin request body")
		}
		for field := range fields {
			if p == "/settings" && strings.EqualFold(field, "console") || strings.HasPrefix(p, "/keys") && (strings.EqualFold(field, "force") || strings.EqualFold(field, "agent")) {
				return ErrRemoteUnavailable
			}
		}
	}
	return nil
}
func remoteKeyID(id string) bool {
	if !strings.HasPrefix(id, "k_") || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
