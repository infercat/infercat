package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
)

func testAdminCode(t *testing.T) string {
	t.Helper()
	// The real address encoder keeps this fixture aligned with tailcat's wire format.
	pk := tailcat.NewPrivateKey()
	pk.Public.RegionID = 1
	return "ia1." + string(pk.Public.Addr()) + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
}

type testRemoteSession struct {
	address       string
	opens, closes atomic.Int32
}

func (s *testRemoteSession) Open(ctx context.Context) (net.Conn, error) {
	s.opens.Add(1)
	return (&net.Dialer{}).DialContext(ctx, "tcp", s.address)
}
func (s *testRemoteSession) Close() error { s.closes.Add(1); return nil }
func testRemote(t *testing.T, h http.HandlerFunc) (*Client, *testRemoteSession) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s := &testRemoteSession{address: srv.Listener.Addr().String()}
	c, err := newRemoteClient(context.Background(), testAdminCode(t), func(ctx context.Context, _ string) (remoteSession, error) {
		d, ok := ctx.Deadline()
		if !ok || time.Until(d) > 20*time.Second {
			t.Fatal("unbounded handshake")
		}
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, s
}
func TestRemoteClientSessionReuseAndRouteBytes(t *testing.T) {
	t.Parallel()
	const body = "{ \"future\": [null, 0],\n\"text\": \"<ok>\" }\n"
	c, s := testRemote(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/console/api/status" || r.Host != "infercat" || r.Header.Get("Authorization") != "Bearer "+base64.RawURLEncoding.EncodeToString(make([]byte, 32)) {
			t.Error("wrong target/auth")
		}
		io.WriteString(w, body)
	})
	for range 3 {
		b, err := c.Call(context.Background(), "GET", "/status", nil)
		if err != nil || string(b) != body {
			t.Fatal(string(b), err)
		}
	}
	if !c.Remote() || s.opens.Load() != 3 || c.http.Timeout != 15*time.Second {
		t.Fatal("session/timeout contract")
	}
}
func TestRemoteRefusesBeforeDialAndBeforeIO(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"ia1.secret", "ic1.host.secret", strings.Repeat("x", 16385)} {
		_, err := newRemoteClient(context.Background(), code, func(context.Context, string) (remoteSession, error) {
			t.Fatal("dialed malformed code")
			return nil, nil
		})
		if !errors.Is(err, ErrAdminCode) || strings.Contains(err.Error(), code) {
			t.Fatal(err)
		}
	}
	c, s := testRemote(t, func(w http.ResponseWriter, r *http.Request) { t.Error("refused route reached host") })
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/events", ""}, {"POST", "/reload", "{}"}, {"GET", "/usage?key_id=k_a", ""}, {"GET", "/usage?since=2026", ""}, {"PATCH", "/settings", `{"Console":"off"}`}, {"POST", "/keys", `{"Force":false}`}, {"PATCH", "/keys/k_a", `{"agent":null}`}, {"GET", "//evil/status", ""}, {"GET", "/status#secret", ""}, {"GET", "/keys/../status", ""}, {"GET", "/status?window=today", ""},
	} {
		if _, err := c.Call(context.Background(), tc.method, tc.path, json.RawMessage(tc.body)); !errors.Is(err, ErrRemoteUnavailable) {
			t.Fatalf("%s: %v", tc.path, err)
		}
	}
	if s.opens.Load() != 0 {
		t.Fatal("performed I/O")
	}
}
func TestRemoteFailureNoReplayOrCredentialLeak(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"429", "redirect", "broken", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			c, _ := testRemote(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "429":
					w.Header().Set("Retry-After", "60")
					http.Error(w, "Too Many Requests", 429)
				case "redirect":
					http.Redirect(w, r, "http://elsewhere.invalid/secret", 307)
				case "broken":
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
				case "timeout":
					io.Copy(io.Discard, r.Body)
					<-r.Context().Done()
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, err := c.Call(ctx, "POST", "/keys", json.RawMessage(`{"name":"x"}`))
			if err == nil || calls.Load() != 1 {
				t.Fatal(calls.Load(), err)
			}
			var api *APIError
			if mode == "429" && (!errors.As(err, &api) || api.Status != 429 || api.RetryAfter != "60") {
				t.Fatal(err)
			}
			if mode == "timeout" && !errors.Is(err, ErrTimeout) {
				t.Fatal(err)
			}
		})
	}
	code := testAdminCode(t)
	_, err := newRemoteClient(context.Background(), code, func(context.Context, string) (remoteSession, error) { return nil, fmt.Errorf("leaked %s", code) })
	if err == nil || strings.Contains(err.Error(), code) {
		t.Fatal(err)
	}
}

func TestRemoteRotateReturnedOnceThenOldCredentialRefused(t *testing.T) {
	t.Parallel()
	var rotated atomic.Bool
	c, s := testRemote(t, func(w http.ResponseWriter, r *http.Request) {
		if rotated.Load() {
			http.Error(w, "Unauthorized", 401)
			return
		}
		if r.URL.Path == "/console/api/remote/rotate" && r.Method == "POST" {
			rotated.Store(true)
			io.WriteString(w, `{"invite":"ia1.new.code"}`)
			return
		}
		io.WriteString(w, `{}`)
	})
	body, err := c.Call(context.Background(), "POST", "/remote/rotate", nil)
	if err != nil || string(body) != `{"invite":"ia1.new.code"}` {
		t.Fatal(string(body), err)
	}
	_, err = c.Call(context.Background(), "GET", "/status", nil)
	var api *APIError
	if !errors.As(err, &api) || api.Status != 401 || s.opens.Load() != 2 {
		t.Fatal(err, s.opens.Load())
	}
}
