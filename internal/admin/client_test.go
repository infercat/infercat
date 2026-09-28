package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientLocalRoundTripAndStoppedHost(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	if _, err := NewClient(dir); !errors.Is(err, ErrNoDaemon) {
		t.Fatal(err)
	}
	const body = "{ \"kept\" : [null, 0],\n\"future\": \"<ok>\" }\n"
	srv, err := Serve(dir, sample, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/custom" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") == "" {
			t.Error("wrong authenticated request")
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"rpm":0}` {
			t.Error("request changed")
		}
		io.WriteString(w, body)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	c, err := NewClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Call(context.Background(), "PATCH", "/custom", json.RawMessage(`{"rpm":0}`))
	if err != nil || string(raw) != body {
		t.Fatal(string(raw), err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), "GET", "/status", nil); !errors.Is(err, ErrNoDaemon) {
		t.Fatal(err)
	}
}

func TestClientBoundedResponsesAndRouteErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
		message    string
	}{
		{"json", `{"error":"same sentence"}`, 404, nil, "same sentence"},
		{"plain", "not allowed\n", 403, nil, "not allowed"},
		{"empty-error", "", 500, nil, "admin API: 500 Internal Server Error"},
		{"malformed", "{", 200, ErrResponse, ""},
		{"two-objects", "{} {}", 200, ErrResponse, ""},
		{"too-large", strings.Repeat(" ", maxResponse) + "{}", 200, ErrResponse, ""},
		{"error-too-large", strings.Repeat("x", maxResponse+1), 400, ErrResponse, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			defer s.Close()
			c := &Client{http: s.Client(), base: s.URL, token: "test"}
			_, err := c.Call(context.Background(), "GET", "/", nil)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatal(err)
				}
				return
			}
			var api *APIError
			if !errors.As(err, &api) || api.Status != tc.status || api.Message != tc.message {
				t.Fatal(err)
			}
		})
	}
}

func TestClientMutationTimeoutAndBrokenReplyNeverReplay(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"timeout", "broken", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			dir := shortDir(t)
			s, err := Serve(dir, sample, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "timeout":
					<-r.Context().Done()
				case "broken":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
				case "redirect":
					w.Header().Set("Location", "/other")
					w.WriteHeader(307)
				}
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			c, err := NewClient(dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_, err = c.Call(ctx, "POST", "/action", json.RawMessage(`{}`))
			if err == nil || calls.Load() != 1 {
				t.Fatal("mutation replayed or succeeded", calls.Load(), err)
			}
			if mode == "timeout" && !errors.Is(err, ErrTimeout) {
				t.Fatal(err)
			}
			if mode == "broken" && !errors.Is(err, ErrResponse) {
				t.Fatal(err)
			}
			if mode == "redirect" {
				var api *APIError
				if !errors.As(err, &api) || api.Status != 307 {
					t.Fatal(err)
				}
			}
		})
	}
}
