package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/infercat/infercat/internal/gateway"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

func receive(t *testing.T, c *websocket.Conn) frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var f frame
	if err = json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}
func transmit(t *testing.T, c *websocket.Conn, f frame) {
	t.Helper()
	b, _ := json.Marshal(f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if len(b) > MaxFrame {
		t.Fatal("frame cap exceeded")
	}
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}
func bridgeServer(t *testing.T) (Config, <-chan *websocket.Conn) {
	t.Helper()
	connections := make(chan *websocket.Conn, 10)
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			http.Error(w, "unauthorized", 401)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(MaxFrame)
		connections <- c
	}))
	oldClient := http.DefaultClient
	http.DefaultClient = s.Client()
	t.Cleanup(func() { http.DefaultClient = oldClient; s.Close() })
	return Config{Endpoint: s.URL, Host: "test", Token: strings.Repeat("a", 64)}, connections
}
func connect(t *testing.T, ch <-chan *websocket.Conn) *websocket.Conn {
	t.Helper()
	select {
	case c := <-ch:
		t.Cleanup(func() { c.CloseNow() })
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no connection")
		return nil
	}
}
func startSession(t *testing.T, c Config, h http.Handler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- session(ctx, c, h) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("session did not stop")
		}
	})
}
func request(t *testing.T, c *websocket.Conn, id, secret string, body []byte) (frame, []byte) {
	t.Helper()
	transmit(t, c, frame{Type: "request", ID: id, Method: "POST", Path: "/v1/chat/completions", Headers: map[string]string{"authorization": "Bearer " + secret, "content-type": "application/json"}})
	for len(body) > 0 {
		n := min(ChunkSize, len(body))
		transmit(t, c, frame{Type: "body", ID: id, Data: body[:n]})
		body = body[n:]
	}
	transmit(t, c, frame{Type: "end", ID: id})
	head := receive(t, c)
	if head.Type != "response" {
		t.Fatalf("first frame: %+v", head)
	}
	var out []byte
	for {
		f := receive(t, c)
		if f.ID != id {
			t.Fatal("wrong id")
		}
		if f.Type == "end" {
			break
		}
		if f.Type != "data" {
			t.Fatalf("frame: %+v", f)
		}
		out = append(out, f.Data...)
		transmit(t, c, frame{Type: "ack", ID: id})
	}
	transmit(t, c, frame{Type: "ready", ID: id})
	return head, out
}
func TestFramingRoundTripAtCap(t *testing.T) {
	c, ch := bridgeServer(t)
	body := bytes.Repeat([]byte("z"), MaxBody)
	startSession(t, c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(got, body) {
			t.Error("request body changed")
		}
		if r.Header.Get("Authorization") != "Bearer friend" {
			t.Error("bearer lost")
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Set-Cookie", "private")
		w.WriteHeader(201)
		_, _ = w.Write(got)
	}))
	conn := connect(t, ch)
	head, out := request(t, conn, "roundtrip", "friend", body)
	if head.Status != 201 || head.Headers["content-type"] != "application/octet-stream" || head.Headers["set-cookie"] != "" || !bytes.Equal(body, out) {
		t.Fatal("round trip failed")
	}
	// Same socket can carry a subsequent request after ready.
	_, out = request(t, conn, "second", "friend", body)
	if !bytes.Equal(out, body) {
		t.Fatal("second request failed")
	}
}
func TestCoalescingAndBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var sent []frame
	ack := make(chan struct{}, 1)
	first := make(chan struct{}, 1)
	w := newResponse(ctx, "stream", func(f frame) error {
		mu.Lock()
		defer mu.Unlock()
		f.Data = bytes.Clone(f.Data)
		sent = append(sent, f)
		if f.Type == "data" {
			first <- struct{}{}
		}
		return nil
	}, ack)
	for i := 0; i < 10; i++ {
		_, _ = w.Write([]byte("data: token\n\n"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("timer did not flush")
	}
	finished := make(chan error, 1)
	go func() { finished <- w.finish() }()
	select {
	case <-finished:
		t.Fatal("finished before downstream acknowledged")
	case <-time.After(20 * time.Millisecond):
	}
	ack <- struct{}{}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 3 || sent[0].Type != "response" || sent[1].Type != "data" || len(sent[1].Data) != 130 || sent[2].Type != "end" {
		t.Fatalf("coalescing frames: %d", len(sent))
	}
}
func TestReloadReconnectAndOff(t *testing.T) {
	c, ch := bridgeServer(t)
	dir := t.TempDir()
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	var manager Manager
	t.Cleanup(manager.Close)
	reload := func() {
		t.Helper()
		if err := manager.Reload(context.Background(), dir, http.NotFoundHandler(), t.Logf); err != nil {
			t.Fatal(err)
		}
	}
	reload()
	conn := connect(t, ch)
	reload()
	select {
	case <-ch:
		t.Fatal("unchanged reload opened another socket")
	case <-time.After(30 * time.Millisecond):
	}
	conn.CloseNow()
	conn = connect(t, ch)
	if err := os.Remove(filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	reload()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("off left socket open")
	}
}
func TestConfigRefusalPreservesState(t *testing.T) {
	dir := t.TempDir()
	c := Config{Endpoint: "https://example.test", Host: "h", Token: strings.Repeat("a", 64)}
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	other := c
	other.Host = "other"
	if Save(dir, other) == nil {
		t.Fatal("overwrote token")
	}
	got, err := Load(dir)
	if err != nil || got != c {
		t.Fatal("state changed")
	}
	st, _ := os.Stat(filepath.Join(dir, FileName))
	if st.Mode().Perm() != 0600 {
		t.Fatal("token permissions")
	}
	for _, endpoint := range []string{"http://example.test", "https://user:pass@example.test", "https://example.test/path", "https://example.test?token=x"} {
		c.Endpoint = endpoint
		if c.Validate() == nil {
			t.Fatal("accepted invalid endpoint")
		}
	}
}

type engine struct{}

func (engine) Info() upstream.Info {
	return upstream.Info{Kind: upstream.Generic, Health: upstream.Health{OK: true}, Slots: 1, Models: []string{"test"}, ModelContext: 4096}
}
func (engine) CountTokens(context.Context, string, []byte) (int, bool, error) { return 1, true, nil }
func (engine) Do(context.Context, string, string, []byte, bool) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n"))}, nil
}

type eventRecorder struct{ events chan usage.Event }

func (r eventRecorder) Record(_ context.Context, e usage.Event) { r.events <- e }
func TestRealGatewaySharedLimitsRevocationAndVia(t *testing.T) {
	ctx := context.Background()
	store, err := keys.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k, secret, err := store.Add(ctx, "friend", keys.Limits{RPM: 1, MaxOutputTokens: 16})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan usage.Event, 10)
	gw := gateway.New(gateway.Config{}, engine{}, store, Recorder{Next: eventRecorder{events}}, t.Logf)
	c, ch := bridgeServer(t)
	startSession(t, c, gw.Handler())
	conn := connect(t, ch)
	body := []byte(`{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true,"max_tokens":8}`)
	head, out := request(t, conn, "first", secret, body)
	if head.Status != 200 || !bytes.Contains(out, []byte("hello")) {
		t.Fatalf("bridge: %d %s", head.Status, out)
	}
	if e := <-events; e.Via != "bridge" {
		t.Fatal("bridge usage missing via")
	}
	direct := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	direct.Header.Set("Authorization", "Bearer "+secret)
	rw := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rw, direct)
	if rw.Code != 429 {
		t.Fatalf("shared RPM limit bypassed: %d %s", rw.Code, rw.Body.String())
	}
	if e := <-events; e.Via != "" {
		t.Fatal("direct request marked bridge")
	}
	if err = store.SetStatus(ctx, k.ID, keys.Revoked); err != nil {
		t.Fatal(err)
	}
	head, _ = request(t, conn, "revoked", secret, body)
	if head.Status != 401 && head.Status != 403 {
		t.Fatalf("revoked: %d", head.Status)
	}
}
func TestRegistrationDoesNotFollowRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/register" {
			http.Redirect(w, r, "/target", 307)
		} else {
			targetCalls.Add(1)
		}
	}))
	defer s.Close()
	old := http.DefaultTransport
	http.DefaultTransport = s.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = old })
	if _, err := Register(context.Background(), s.URL, "a-valid-registration-code"); err == nil {
		t.Fatal("accepted redirect")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("forwarded code")
	}
}

// A response consumer can cancel a job while the gateway is waiting for upstream data.
// The shared socket must remain usable, and the gateway must observe request cancellation.
func TestRequestCancelKeepsSessionAndStopsHandler(t *testing.T) {
	c, ch := bridgeServer(t)
	cancelled := make(chan struct{})
	startSession(t, c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/wait" {
			w.WriteHeader(200)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		_, _ = w.Write([]byte("next job"))
	}))
	conn := connect(t, ch)
	transmit(t, conn, frame{Type: "request", ID: "cancel-me", Method: "GET", Path: "/v1/wait"})
	transmit(t, conn, frame{Type: "end", ID: "cancel-me"})
	if f := receive(t, conn); f.Type != "response" {
		t.Fatalf("expected response, got %s", f.Type)
	}
	transmit(t, conn, frame{Type: "cancel", ID: "cancel-me"})
	for {
		f := receive(t, conn)
		if f.Type == "ready" {
			break
		}
		if f.ID != "cancel-me" {
			t.Fatal("wrong cancellation id")
		}
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("handler context survived cancel")
	}
	head, out := request(t, conn, "next", "friend", nil)
	if head.Status != 200 || string(out) != "next job" {
		t.Fatal("next job failed after cancellation")
	}
}

func TestAckTimeoutCancelsOnlyRequest(t *testing.T) {
	c, ch := bridgeServer(t)
	cancelled := make(chan struct{})
	startSession(t, c, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/stall" {
			rw := w.(*response)
			rw.mu.Lock()
			rw.ackWait = 20 * time.Millisecond
			rw.mu.Unlock()
			_, _ = w.Write(bytes.Repeat([]byte("x"), ChunkSize))
			<-r.Context().Done()
			close(cancelled)
			return
		}
		_, _ = w.Write([]byte("next job"))
	}))
	conn := connect(t, ch)
	transmit(t, conn, frame{Type: "request", ID: "stall", Method: "GET", Path: "/v1/stall"})
	transmit(t, conn, frame{Type: "end", ID: "stall"})
	if f := receive(t, conn); f.Type != "response" {
		t.Fatal("missing response")
	}
	if f := receive(t, conn); f.Type != "data" {
		t.Fatal("missing chunk")
	}
	// Deliberately withhold this one chunk's ack; the fixture shortens only this writer's wait.
	if f := receive(t, conn); f.Type != "cancel" || f.ID != "stall" {
		t.Fatalf("timeout frame: %s %s", f.Type, f.ID)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("timeout did not cancel handler")
	}
	transmit(t, conn, frame{Type: "cancel", ID: "stall"})
	if f := receive(t, conn); f.Type != "ready" {
		t.Fatalf("expected ready, got %s", f.Type)
	}
	head, out := request(t, conn, "next", "friend", nil)
	if head.Status != 200 || string(out) != "next job" {
		t.Fatal("ack timeout closed session")
	}
}

type cancellableEngine struct {
	engine
	entered, cancelled chan struct{}
	calls              atomic.Int32
}

func (e *cancellableEngine) Do(ctx context.Context, method, path string, body []byte, stream bool) (*http.Response, error) {
	if e.calls.Add(1) == 1 {
		close(e.entered)
		<-ctx.Done()
		close(e.cancelled)
		return nil, ctx.Err()
	}
	return e.engine.Do(ctx, method, path, body, stream)
}
func TestCancelStopsRealGatewayUpstreamAndReleasesKey(t *testing.T) {
	ctx := context.Background()
	store, err := keys.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, secret, err := store.Add(ctx, "friend", keys.Limits{MaxConcurrent: 1, MaxOutputTokens: 16})
	if err != nil {
		t.Fatal(err)
	}
	up := &cancellableEngine{entered: make(chan struct{}), cancelled: make(chan struct{})}
	gw := gateway.New(gateway.Config{}, up, store, Recorder{Next: eventRecorder{make(chan usage.Event, 4)}}, t.Logf)
	c, ch := bridgeServer(t)
	startSession(t, c, gw.Handler())
	conn := connect(t, ch)
	body := []byte(`{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true,"max_tokens":8}`)
	transmit(t, conn, frame{Type: "request", ID: "upstream", Method: "POST", Path: "/v1/chat/completions", Headers: map[string]string{"authorization": "Bearer " + secret, "content-type": "application/json"}})
	transmit(t, conn, frame{Type: "body", ID: "upstream", Data: body})
	transmit(t, conn, frame{Type: "end", ID: "upstream"})
	select {
	case <-up.entered:
	case <-time.After(time.Second):
		t.Fatal("upstream was not entered")
	}
	transmit(t, conn, frame{Type: "cancel", ID: "upstream"})
	for {
		f := receive(t, conn)
		if f.Type == "ready" {
			break
		}
		if f.ID != "upstream" {
			t.Fatal("wrong cancelled id")
		}
	}
	select {
	case <-up.cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not stop")
	}
	if gw.Counters(key.ID).InFlight != 0 {
		t.Fatal("cancel retained key admission")
	}
	head, out := request(t, conn, "next", secret, body)
	if head.Status != 200 || !bytes.Contains(out, []byte("hello")) {
		t.Fatalf("next gateway request: %d %s", head.Status, out)
	}
}
