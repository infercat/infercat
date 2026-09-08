package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

// The request line (029 promises 1, 2, 5): the ticket's example, and the three ways a request
// ends badly; never prompt content, whatever the event carries.
func TestRequestLine(t *testing.T) {
	ts := time.Date(2026, 9, 3, 12, 1, 0, 0, time.Local)
	ok := usage.Event{TS: ts, KeyID: "k_1", Endpoint: "/v1/chat/completions", Model: "gemma-4-E2B", Status: 200, Stream: true,
		PromptTokens: 38, CompletionTokens: 412, TTFTMS: 61, TotalMS: 4100, Prompt: "never shown"}
	for _, c := range []struct {
		ev   usage.Event
		want string
	}{
		{ok, "12:01:04  alice  chat  gemma-4-E2B  38→412 tok  ttft 61ms  4.1s  ok"},
		{usage.Event{TS: ts, Endpoint: "/v1/chat/completions", Model: "m", Status: 429, Code: "rate_limited", TotalMS: 2}, "12:01:00  alice  chat  m  2ms  429 rate_limited"},
		{usage.Event{TS: ts, Endpoint: "/v1/chat/completions", Model: "m", Status: 503, Code: "queue_timeout", QueuedMS: 30000, TotalMS: 30012}, "12:01:30  alice  chat  m  queued 30.0s  30.0s  503 queue_timeout"},
		{usage.Event{TS: ts, Endpoint: "/v1/chat/completions", Model: "m", Status: 499, Code: "client_closed", PromptTokens: 12, CompletionTokens: 3, TTFTMS: 80, TotalMS: 900}, "12:01:00  alice  chat  m  12→3 tok  ttft 80ms  900ms  client_closed"},
		{usage.Event{TS: ts, Endpoint: "/me", Status: 200, TotalMS: 1}, "12:01:00  alice  me  1ms  ok"},
		{usage.Event{TS: ts, Endpoint: "/v1/models", Status: 200, TotalMS: 12}, "12:01:00  alice  models  12ms  ok"},
	} {
		if got := requestLine(c.ev, "alice"); got != c.want {
			t.Errorf("requestLine:\n got %q\nwant %q", got, c.want)
		}
	}
	if got := requestLine(ok, "alice"); strings.Contains(got, "never shown") {
		t.Fatal("the line carried the prompt")
	}
}

// /metrics as the two engines write it: llama.cpp bare names, vLLM labelled series.
func TestParseMetrics(t *testing.T) {
	m := parseMetrics(strings.NewReader(`# HELP llamacpp:requests_processing Number of requests processing.
# TYPE llamacpp:requests_processing gauge
llamacpp:requests_processing 2
llamacpp:requests_deferred 3
process_resident_memory_bytes 2.35384832e+08
vllm:num_requests_running{engine="0",model_name="entropy-v2 gemma"} 1.0
vllm:num_requests_running{engine="1",model_name="x"} 2.0
vllm:kv_cache_usage_perc{engine="0",model_name="x"} 0.25
garbage line
`))
	want := map[string]float64{"llamacpp:requests_processing": 2, "llamacpp:requests_deferred": 3, "process_resident_memory_bytes": 235384832,
		"vllm:num_requests_running": 3, "vllm:kv_cache_usage_perc": 0.25}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v; want %v", k, m[k], v)
		}
	}
}

// The status block (029 promises 3, 4, 5, 6): sessions with their path, handshake, bytes and
// age; the queue's peak; what the engine says or that it says nothing; the process; and the
// bridge's own block.
func TestStatusBlockShowsSessionsEngineAndProcess(t *testing.T) {
	now := time.Now()
	st := admin.Status{Product: "Infercat", Version: "t", UptimeS: 90, Mode: "host",
		Upstream: admin.Upstream{Kind: "llama.cpp", URL: "http://127.0.0.1:8080", Healthy: true, ModelContext: 4096, Slots: 2},
		Tunnel: admin.Tunnel{Addr: "tcABC", Region: "New York City", Clients: 1, RxBytes: 3000, TxBytes: 5 << 20, Sessions: []admin.Session{
			{Key: "fd7a:115c:a1e0:ab12:4843:cd96:6263:e3a2", Path: "unknown", Conns: 2, RxBytes: 1000, TxBytes: 4 << 20, LastByte: now.Add(-40 * time.Second), Since: now.Add(-3 * time.Minute), Active: true},
			{Key: "fd7a:115c:a1e0:ab12:4843:cd96:1111:2222", Path: "unknown", Conns: 1, RxBytes: 2000, TxBytes: 1 << 20, LastByte: now.Add(-5 * time.Minute), Since: now.Add(-20 * time.Minute), Active: true},
			{Key: "fd7a:115c:a1e0:ab12:4843:cd96:dead:beef", Path: "unknown", Since: now.Add(-2 * time.Hour)},
		}},
		Queue:   admin.Queue{InFlight: 2, Waiting: 3},
		Engine:  admin.Engine{SlotsPeak: 2, TokensPerS: 118.4, Metrics: true, Busy: 2, Waiting: 1},
		Process: admin.Process{Goroutines: 84, HeapBytes: 12 << 20, SysBytes: 48 << 20},
	}
	var out bytes.Buffer
	writeStatus(&out, st)
	s := out.String()
	squash := func(s string) string { return strings.Join(strings.Fields(s), " ") } // the session table is tab-aligned
	for _, want := range []string{
		"sessions  2 active of 3 seen  ·  in 3 KB  out 5.0 MB  ·  paths: the client's to measure, not visible to a host",
		"…6263:e3a2  2 conns  in 1000 B  out 4.0 MB  last byte just now  age 3m",
		"…1111:2222  1 conn  in 2 KB  out 1.0 MB  last byte 5m ago  age 20m",
		"queue     2 in flight, 3 waiting  (peak 2 in flight, sampled)",
		"engine    118 tok/s over the last minute  ·  engine says 2 busy, 1 waiting  ·  memory not reported",
		"process   84 goroutines  heap 12.0 MB  sys 48.0 MB  rss —",
	} {
		if !strings.Contains(squash(s), squash(want)) {
			t.Errorf("status lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "dead:beef") {
		t.Errorf("an inactive session is listed:\n%s", s)
	}
	st.Engine = admin.Engine{TokensPerS: 0}
	out.Reset()
	writeStatus(&out, st)
	if !strings.Contains(out.String(), "engine    0 tok/s over the last minute  ·  no /metrics from this engine") {
		t.Errorf("an engine without /metrics:\n%s", out.String())
	}
	st.Engine = admin.Engine{Metrics: true, MemoryBytes: 235384832, KVCachePct: 25}
	out.Reset()
	writeStatus(&out, st)
	if !strings.Contains(out.String(), "memory 224.5 MB  ·  kv cache 25%") {
		t.Errorf("vLLM's memory and cache:\n%s", out.String())
	}

	bridge := admin.Status{Product: "Infercat", Version: "t", UptimeS: 5, Mode: "bridge", Name: "Max's laptop",
		Tunnel:   admin.Tunnel{Addr: "tcABC", Region: "New York City", Sessions: []admin.Session{{Key: "host", Path: "relayed", Via: "nyc", RTTMS: 27.4, HandshakeMS: 512, Since: now.Add(-5 * time.Second), Active: true}}},
		Upstream: admin.Upstream{Kind: "bridge", URL: "http://127.0.0.1:11435", Healthy: true},
		Queue:    admin.Queue{InFlight: 1}}
	out.Reset()
	writeStatus(&out, bridge)
	for _, want := range []string{"up 5s  ·  bridge", "host      Max's laptop  relay New York City  tcABC", "path      relayed via nyc · 27.4 ms  (handshake took 512 ms; session 5s old)", "local     http://127.0.0.1:11435  1 in flight"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("bridge status lacks %q:\n%s", want, out.String())
		}
	}
	bridge.Tunnel.Sessions[0].Active = false
	out.Reset()
	writeStatus(&out, bridge)
	if !strings.Contains(out.String(), "path      lost — reconnecting") {
		t.Errorf("a bridge between sessions:\n%s", out.String())
	}
}

// `status --watch` (029 promise 1) against a live admin socket: the block, then one line per
// event as it happens, redrawn with a plain ANSI clear; Ctrl-C ends it with exit 0.
func TestStatusWatchStreamsRequests(t *testing.T) {
	dir, err := os.MkdirTemp("", "bn029-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	hub := admin.NewEvents(nil)
	st := admin.Status{Mode: "host", Upstream: admin.Upstream{Kind: "llama.cpp", URL: "http://e", Healthy: true, Slots: 2},
		Keys: []admin.Key{{ID: "k_1", Name: "alice", Status: "active"}}}
	adm, err := admin.Serve(dir, func() admin.Status { return st }, nil, hub)
	if err != nil {
		t.Fatal(err)
	}
	defer adm.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errw bytes.Buffer
	code := make(chan int, 1)
	go func() {
		code <- run(ctx, []string{"status", "--data-dir", dir, "--watch", "--interval", "50ms"}, &out, &errw, nil, true, testPlatform(fakeAddr, nil))
	}()
	waitFor := func(want string) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !strings.Contains(out.String(), want); {
			if time.Now().After(deadline) {
				t.Fatalf("watch never showed %q:\n%s%s", want, out.String(), errw.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitFor("upstream  llama.cpp  http://e  healthy")
	time.Sleep(200 * time.Millisecond) // the watcher's stream must be open before the event
	hub.Record(ctx, usage.Event{TS: time.Now(), KeyID: "k_1", Endpoint: "/v1/chat/completions", Model: "m", Status: 200, PromptTokens: 5, CompletionTokens: 7, TTFTMS: 9, TotalMS: 1200, Prompt: "hidden"})
	waitFor("alice  chat  m  5→7 tok  ttft 9ms  1.2s  ok")
	if !strings.Contains(out.String(), "\x1b[H\x1b[2J") {
		t.Fatal("the watch did not redraw with an ANSI clear")
	}
	if strings.Contains(out.String(), "hidden") {
		t.Fatal("the watch printed prompt content")
	}
	cancel()
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("exit %d\n%s%s", c, out.String(), errw.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("status --watch did not stop after Ctrl-C")
	}
	// Without --watch nothing streams, and without a host the message names the way out.
	if r := exec(t, testPlatform(fakeAddr, nil), "status", "--data-dir", dir); r.code != 0 || strings.Contains(r.out, "\x1b[") {
		t.Fatalf("plain status: exit %d\n%s", r.code, r.out)
	}
}

// `serve --log-requests` (029 promise 2): the same line on the host's terminal, off by default.
func TestServeLogRequestsPrintsTheLine(t *testing.T) {
	dir, err := os.MkdirTemp("", "bn029-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store, err := keys.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	k, _, err := store.Add(context.Background(), "alice", keys.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	engine := fakeEngine(t)
	for _, on := range []bool{true, false} {
		gw := newFakeGateway()
		rec := make(chan usage.Recorder, 1)
		plat := testPlatform(fakeAddr, nil)
		plat.startTunnel = func(ctx context.Context, o tunnelOptions) (tunnelServer, error) { return fakeTunnel{}, nil }
		plat.newGateway = func(_ gatewayOptions, _ upstream.Upstream, _ keys.Store, r usage.Recorder, _ func(string, ...any)) (gatewayServer, error) {
			rec <- r
			return gw, nil
		}
		args := []string{"serve", "--data-dir", dir, "--upstream", engine}
		if on {
			args = append(args, "--log-requests")
		}
		ctx, cancel := context.WithCancel(context.Background())
		var out, errw lockedBuffer
		code := make(chan int, 1)
		go func() { code <- run(ctx, args, &out, &errw, nil, false, plat) }()
		select {
		case <-gw.serving:
		case <-time.After(10 * time.Second):
			t.Fatalf("serve never reached the gateway:\n%s%s", out.String(), errw.String())
		}
		r := <-rec
		r.Record(ctx, usage.Event{TS: time.Now(), KeyID: k.ID, Endpoint: "/v1/chat/completions", Model: "m", Status: 200, PromptTokens: 3, CompletionTokens: 4, TotalMS: 500, Prompt: "hidden"})
		// The line is printed by the recorder's own goroutine; wait for it with a deadline instead of a
		// fixed sleep, which a loaded CI runner turned into a flake (2026-09-09).
		deadline := time.Now().Add(5 * time.Second)
		for on && !strings.Contains(out.String(), "alice  chat  m  3→4 tok  500ms  ok") && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if !on {
			time.Sleep(200 * time.Millisecond)
		}
		cancel()
		select {
		case <-code:
		case <-time.After(15 * time.Second):
			t.Fatal("serve did not stop")
		}
		if got := strings.Contains(out.String(), "alice  chat  m  3→4 tok  500ms  ok"); got != on {
			t.Fatalf("--log-requests=%v: line printed = %v\n%s%s", on, got, out.String(), errw.String())
		}
		if strings.Contains(out.String()+errw.String(), "hidden") {
			t.Fatal("the terminal showed prompt content")
		}
	}
}

// lockedBuffer is a bytes.Buffer safe to read while the command goroutine writes it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }
