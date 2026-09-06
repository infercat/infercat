package gateway

// Ticket 006: one named test per promise. The numbers in the names are the ticket's.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/2185Lab/infercat/internal/keys"
	"github.com/2185Lab/infercat/internal/upstream"
)

// waitUntil polls cond for up to d.
func waitUntil(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %s for %s", d, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ---- 1. alias bypass ----

func TestOverrideKeysStripped(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.MaxOutputTokens = 100 })
	overrides := `"n_predict":100000,"max_new_tokens":100000,"min_tokens":90000,"ignore_eos":true,"n":4,"n_cmpl":4,"best_of":8,` +
		`"n_probs":50,"id_slot":0,"slot_id":0,"n_keep":-1,"n_discard":0,"n_ctx":1000000,"priority":100,"lora":[{"id":0,"scale":1}],` +
		`"max_completion_tokens":"1000000"`
	sampling := `"temperature":0.7,"top_p":0.9,"top_k":40,"seed":7,"stop":["\n"],"presence_penalty":0.5,"frequency_penalty":0.25,` +
		`"logit_bias":{"50256":-100},"response_format":{"type":"json_object"},"tools":[{"type":"function","function":{"name":"f"}}],` +
		`"cache_prompt":false,"samplers":["top_k","top_p"],"reasoning_format":"deepseek"`
	if r := h.post("/v1/chat/completions", chatBody("m1", 2, overrides+","+sampling)); r.status != 200 {
		t.Fatalf("chat: %d %s", r.status, r.body)
	}
	body := h.up.body(t)
	for _, k := range overrideKeys {
		if _, ok := body[k]; ok {
			t.Fatalf("%q reached the upstream", k)
		}
	}
	if _, ok := body["max_completion_tokens"]; ok {
		t.Fatal("a non-numeric max_completion_tokens reached the upstream")
	}
	if body["max_tokens"] != json.Number("100") {
		t.Fatalf("max_tokens must be the key's cap once n_predict is gone: %v", body["max_tokens"])
	}
	for _, k := range []string{"temperature", "top_p", "top_k", "seed", "stop", "presence_penalty", "frequency_penalty", "logit_bias", "response_format", "tools", "cache_prompt", "samplers", "reasoning_format"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("sampling key %q was stripped", k)
		}
	}
	if body["temperature"] != json.Number("0.7") || body["seed"] != json.Number("7") || body["cache_prompt"] != false {
		t.Fatalf("sampling values changed: %v", body)
	}
	if !strings.Contains(globalLogs.String(), "removed [best_of id_slot ignore_eos") {
		t.Fatal("the host's log must name the removed keys")
	}
	// Embeddings go through the same strip.
	h.up.set("json", `{"object":"list","data":[],"usage":{"prompt_tokens":1}}`)
	if r := h.post("/v1/embeddings", `{"input":"x","id_slot":1,"lora":[],"n_ctx":5}`); r.status != 200 {
		t.Fatalf("embeddings: %d %s", r.status, r.body)
	}
	body = h.up.body(t)
	if _, ok := body["id_slot"]; ok {
		t.Fatalf("embeddings body still carries id_slot: %v", body)
	}
	// Numeric max_tokens is clamped, not stripped, and a string max_tokens becomes the cap.
	h.up.set("json")
	h.post("/v1/chat/completions", chatBody("m1", 2, `"max_tokens":"9999999","n_predict":9999999`))
	if got := h.up.body(t)["max_tokens"]; got != json.Number("100") {
		t.Fatalf("string max_tokens: upstream saw %v", got)
	}
}

// ---- 2. admission before body ----

func TestAdmissionBeforeBody(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.slots(4)
	h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 2 })
	open := h.up.gateTokenize()
	defer open()
	// One 4 MiB body (just under the cap), sent by 50 concurrent requests from the one key.
	body := chatBody("m1", 2, `"pad":"`+strings.Repeat("x", 4<<20-256)+`"`)
	if int64(len(body)) > 4<<20 {
		t.Fatalf("test body is %d bytes", len(body))
	}
	const n = 50
	results := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+testSecret)
			req.Header.Set("Content-Type", "application/json")
			res, err := h.srv.Client().Do(req)
			if err != nil {
				// The server answered 429 and closed under the unread 4 MiB body; the transport may
				// report that as a write error rather than hand over the response. Still a rejection.
				results <- -1
				return
			}
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			results <- res.StatusCode
		}()
	}
	// Server-side truth: 48 rejections are recorded while the two admitted requests sit in tokenize
	// with their bodies buffered — and nothing else is.
	evs := h.rec.waitFor(t, n-2)
	for _, e := range evs {
		if e.Status != 429 || e.Code != "concurrency_limited" {
			t.Fatalf("expected concurrency_limited, got %+v", e)
		}
	}
	waitUntil(t, 3*time.Second, "both admitted requests to reach tokenize", func() bool { return h.up.countNow.Load() == 2 })
	if b := h.gw.bodies.Load(); b != 2 {
		t.Fatalf("bodies buffered while 48 were rejected: %d, want 2", b)
	}
	if c := h.gw.Counters("k_alice1"); c.InFlight != 2 {
		t.Fatalf("in flight: %+v", c)
	}
	open()
	ok, rejected := 0, 0
	for i := 0; i < n; i++ {
		if s := <-results; s == 200 {
			ok++
		} else {
			rejected++
		}
	}
	if ok != 2 || rejected != n-2 {
		t.Fatalf("outcomes: %d ok, %d rejected", ok, rejected)
	}
	if m := h.up.countMax.Load(); m != 2 {
		t.Fatalf("tokenize calls in flight at once: %d, want 2", m)
	}
	if got := h.up.requests.Load(); got != 2 {
		t.Fatalf("engine requests: %d, want 2", got)
	}
	h.rec.waitFor(t, n)
	if b := h.gw.bodies.Load(); b != 0 {
		t.Fatalf("bodies still buffered after the burst: %d", b)
	}
	if c := h.gw.Counters("k_alice1"); c.InFlight != 0 || c.RPMUsed != 2 {
		t.Fatalf("after the burst: %+v", c)
	}
}

// A rejection after admission but before the queue (here context_too_long, after a real tokenize)
// is un-counted: RPM does not move and the per-key slot is back. A queue timeout, after the queue
// was joined, counts.
func TestPreQueueRejectionIsUncounted(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.queueTimeout = 100 * time.Millisecond
	h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 10 })
	h.setKey(func(k *keys.Key) { k.Limits.MaxOutputTokens = 4; k.Limits.RPM = 5 })
	h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 50, "")), CodeContextTooLong)
	h.expectErr(h.post("/v1/embeddings", `{"input":`), CodeInvalidRequest)
	if c := h.gw.Counters("k_alice1"); c.RPMUsed != 0 || c.InFlight != 0 {
		t.Fatalf("pre-queue rejections must not count: %+v", c)
	}
	h.up.set("sse", sseEvents(20, true)...)
	h.store.set("second", &keys.Key{ID: "k_bob", Name: "bob", Status: keys.Active})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader(chatBody("m1", 1, `"stream":true`)))
	req.Header.Set("Authorization", "Bearer second")
	res, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	<-h.up.started
	h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeQueueTimeout)
	if c := h.gw.Counters("k_alice1"); c.RPMUsed != 1 || c.InFlight != 0 {
		t.Fatalf("a queue timeout counts as a request: %+v", c)
	}
}

// ---- 3. write deadline ----

func TestStalledReaderFreesSlots(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.writeTimeout = 300 * time.Millisecond
	// 64 events of 256 KiB, no gap: more than any loopback socket buffer once the client stops reading.
	big := strings.Repeat("y", 256<<10)
	var events []string
	for i := 0; i < 64; i++ {
		events = append(events, `{"choices":[{"index":0,"delta":{"content":"`+big+`"}}]}`)
	}
	h.up.set("sse", events...)
	h.up.mu.Lock()
	h.up.gap = 0
	h.up.mu.Unlock()
	res, err := h.streamReq(context.Background(), chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	br := bufio.NewReader(res.Body)
	for i := 0; i < 2; i++ {
		if _, err := readEvent(br); err != nil {
			t.Fatal(err)
		}
	}
	// Stop reading. The gateway must give up on us within the write deadline and free everything.
	start := time.Now()
	waitUntil(t, 5*time.Second, "the stalled stream to release its slots", func() bool {
		inF, _ := h.gw.Queue()
		return inF == 0 && h.gw.Counters("k_alice1").InFlight == 0
	})
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("slots released only after %s", d)
	}
	select {
	case <-h.up.cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream was not cancelled after the client stalled")
	}
	ev := h.rec.last(t)
	if ev.Status != 200 || ev.CompletionTokens < 2 {
		t.Fatalf("stalled stream event: %+v", ev)
	}
	// A second request from the same key goes straight through: nothing is pinned.
	h.up.set("json")
	if r := h.post("/v1/chat/completions", chatBody("m1", 1, "")); r.status != 200 {
		t.Fatalf("after the stall: %d %s", r.status, r.body)
	}
}

// ---- 4. read deadline ----

func rawConn(t *testing.T, srvURL string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srvURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func TestBodyReadDeadline(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.readTimeout = 200 * time.Millisecond

	// A valid key that never finishes its body: 400 within the deadline, slots and buffers freed.
	conn := rawConn(t, h.srv.URL)
	fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"model\":", testSecret)
	start := time.Now()
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no response to a stalled body: %v", err)
	}
	b, _ := io.ReadAll(res.Body)
	if d := time.Since(start); res.StatusCode != 400 || !strings.Contains(string(b), "invalid_request") || !strings.Contains(string(b), "not received") || d > 2*time.Second {
		t.Fatalf("stalled body: %d %s after %s", res.StatusCode, b, d)
	}
	ev := h.rec.last(t)
	if ev.Status != 400 || ev.Code != "invalid_request" || ev.KeyID != "k_alice1" {
		t.Fatalf("stalled body event: %+v", ev)
	}
	if c := h.gw.Counters("k_alice1"); c.InFlight != 0 {
		t.Fatalf("slot leaked: %+v", c)
	}
	if h.gw.bodies.Load() != 0 {
		t.Fatal("body buffer leaked")
	}

	// No valid key and a body that never arrives: 401 at once, and the connection — which net/http
	// would otherwise hold open discarding the unread body — is closed within the deadline.
	conn = rawConn(t, h.srv.URL)
	fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer nope\r\nContent-Length: 100\r\n\r\n")
	res, err = http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || res.StatusCode != 401 {
		t.Fatalf("unauthenticated stalled body: %v %v", err, res)
	}
	_, _ = io.ReadAll(res.Body)
	start = time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection still open after a stalled unauthenticated body")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("server held the connection open for %s waiting for a body it will never read", time.Since(start))
	}

	// The deadline is cleared once the body is in hand: a stream longer than it is not cut.
	h.up.set("sse", sseEvents(10, true)...) // 10 × 50 ms = 500 ms > 200 ms
	sres, err := h.streamReq(context.Background(), chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(sres.Body)
	sres.Body.Close()
	if !strings.HasSuffix(strings.TrimSpace(string(got)), "data: [DONE]") {
		t.Fatalf("a stream longer than the read deadline was cut: %q", got)
	}
	if ev := h.rec.last(t); ev.Code != "" || ev.CompletionTokens != 10 {
		t.Fatalf("long stream event: %+v", ev)
	}
}

// ---- 5. /v1/models metered by concurrency, never counted against RPM (014 promise 5) ----

func TestModelsMetered(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.queueTimeout = 2 * time.Second
	h.up.set("sse", sseEvents(40, true)...) // 2 s if left alone
	h.store.set("second", &keys.Key{ID: "k_bob", Name: "bob", Status: keys.Active})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader(chatBody("m1", 1, `"stream":true`)))
	req.Header.Set("Authorization", "Bearer second")
	res, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	<-h.up.started
	// bob holds the only global slot; alice's model list does not wait for it.
	start := time.Now()
	if r := h.get("/v1/models"); r.status != 200 || time.Since(start) > time.Second {
		t.Fatalf("models behind a busy engine: %d after %s", r.status, time.Since(start))
	}
	cancel()
	h.rec.waitFor(t, 2)

	// Per-key concurrency applies: alice's own chat request, parked in tokenize, blocks her list.
	open := h.up.gateTokenize()
	defer open()
	h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 1 })
	done := make(chan resp, 1)
	go func() { done <- h.post("/v1/chat/completions", chatBody("m1", 1, "")) }()
	waitUntil(t, 3*time.Second, "alice's chat to reach tokenize", func() bool { return h.up.countNow.Load() == 1 })
	h.expectErr(h.get("/v1/models"), CodeConcurrencyLimited)
	open()
	<-done

	// RPM is the friend's message allowance (014 promise 5, settle table): a list call spends none
	// of it however often it is made, /me spends none, and only the chat call moves the number the
	// friend's meter shows. A key with RPM 1 can list twice and still have its one message.
	h.store.set("third", &keys.Key{ID: "k_cat", Name: "cat", Status: keys.Active, Limits: keys.Limits{RPM: 1}})
	for i := range 2 {
		if r := h.do(http.MethodGet, "/v1/models", "Bearer third", ""); r.status != 200 {
			t.Fatalf("models call %d: %d %s", i+1, r.status, r.body)
		}
	}
	if r := h.do(http.MethodGet, "/me", "Bearer third", ""); r.status != 200 {
		t.Fatalf("/me under rpm: %d", r.status)
	}
	if c := h.gw.Counters("k_cat"); c.RPMUsed != 0 || c.InFlight != 0 {
		t.Fatalf("list calls must not count against RPM, and must free their slot: %+v", c)
	}
	// The one message the key is allowed still gets through, and it is the thing that counts.
	h.up.set("json")
	if r := h.do(http.MethodPost, "/v1/chat/completions", "Bearer third", chatBody("m1", 1, "")); r.status != 200 {
		t.Fatalf("chat after two list calls: %d %s", r.status, r.body)
	}
	if c := h.gw.Counters("k_cat"); c.RPMUsed != 1 {
		t.Fatalf("the chat call is the one that counts: %+v", c)
	}
	h.expectErr(h.do(http.MethodPost, "/v1/chat/completions", "Bearer third", chatBody("m1", 1, "")), CodeRateLimited)
}

// ---- 6. no redirects upstream ----

func TestUpstreamRedirectNotFollowed(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.set("redirect")
	r := h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h.expectErr(r, CodeUpstreamError)
	if !strings.Contains(r.message, "HTTP 302") {
		t.Fatalf("message: %s", r.message)
	}
	h.post("/v1/embeddings", `{"input":"x"}`)
	h.rec.waitFor(t, 2)
	if n := h.up.landed.Load(); n != 0 {
		t.Fatalf("the gateway followed the engine's redirect %d time(s)", n)
	}
	if c := h.gw.Counters("k_alice1"); c.TodayTokens != 0 || c.InFlight != 0 {
		t.Fatalf("redirect charged tokens or leaked a slot: %+v", c)
	}
}

// ---- 7. audit truth ----

func TestAuditTruth(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	// (a) paused and revoked rejections name the key and move last_seen.
	h.setKey(func(k *keys.Key) { k.Status = keys.Paused })
	h.expectErr(h.get("/me"), CodeKeyPaused)
	ev := h.rec.last(t)
	if ev.KeyID != "k_alice1" || ev.Status != 403 || ev.Code != "key_paused" || ev.Endpoint != "/me" {
		t.Fatalf("paused event: %+v", ev)
	}
	if h.gw.Counters("k_alice1").LastSeen.IsZero() {
		t.Fatal("a paused key's last_seen must move")
	}
	h.setKey(func(k *keys.Key) { k.Status = keys.Revoked })
	h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeKeyRevoked)
	if ev := h.rec.waitFor(t, 2)[1]; ev.KeyID != "k_alice1" || ev.Code != "key_revoked" {
		t.Fatalf("revoked event: %+v", ev)
	}
	// (b) unauthenticated requests record nothing, whatever path they carry.
	h.expectErr(h.do(http.MethodGet, "/"+strings.Repeat("a", 5000), "Bearer nope", ""), CodeInvalidKey)
	h.expectErr(h.do(http.MethodPost, "/v1/chat/completions", "", chatBody("m1", 1, "")), CodeInvalidKey)
	time.Sleep(30 * time.Millisecond)
	if evs := h.rec.waitFor(t, 2); len(evs) != 2 {
		t.Fatalf("401s recorded usage events: %+v", evs[2:])
	}
	// A keyed 404 is recorded with its path capped at 64 bytes.
	h.setKey(func(k *keys.Key) { k.Status = keys.Active })
	h.expectErr(h.get("/"+strings.Repeat("b", 5000)), CodeNotFound)
	ev = h.rec.waitFor(t, 3)[2]
	if ev.Status != 404 || len(ev.Endpoint) != maxEndpointLen || !strings.HasPrefix(ev.Endpoint, "/bbb") {
		t.Fatalf("404 event endpoint (%d bytes): %+v", len(ev.Endpoint), ev)
	}
}

// ---- 8. upstream 4xx ----

func TestUpstream4xxIsTheFriends(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.set("status:400", `{"error":{"code":400,"message":"invalid grammar: unexpected token","type":"invalid_request_error"}}`)
	r := h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h.expectErr(r, CodeInvalidRequest)
	if !strings.Contains(r.message, "HTTP 400") || !strings.Contains(r.message, "invalid grammar: unexpected token") || strings.Contains(r.message, `{"error"`) {
		t.Fatalf("400 message: %s", r.message)
	}
	h.up.set("status:422", `{"object":"error","message":"image input is not supported by this model","type":"BadRequestError","code":422}`)
	r = h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h.expectErr(r, CodeInvalidRequest)
	if !strings.Contains(r.message, "HTTP 422") || !strings.Contains(r.message, "image input is not supported") {
		t.Fatalf("422 message: %s", r.message)
	}
	// Stream requests too: the 4xx arrives before any SSE byte, so it is a plain error response.
	r = h.post("/v1/chat/completions", chatBody("m1", 1, `"stream":true`))
	h.expectErr(r, CodeInvalidRequest)

	// 036: the one 400 that is the gateway's own case — the engine's context check, which the
	// pre-check can still miss by a token at the ceiling — is 422 context_too_long in the gateway's
	// words: the friend shortens instead of retrying and never reads the engine's sentence (which
	// goes to the host's log). Both engines' shapes as read on 2026-09-03, stream and not.
	h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 8192 })
	for _, shape := range []string{
		`{"error":{"code":400,"message":"request (8201 tokens) exceeds the available context size (8192 tokens), try increasing it","type":"exceed_context_size_error","n_prompt_tokens":8201,"n_ctx":8192}}`,
		`{"error":{"message":"This model's maximum context length is 8192 tokens. However, you requested 16 output tokens and your prompt contains at least 8185 input tokens, for a total of at least 8201 tokens. Please reduce the length of the input prompt or the number of requested output tokens. (parameter=input_tokens, value=8185)","type":"BadRequestError","param":"input_tokens","code":400}}`,
	} {
		h.up.set("status:400", shape)
		for _, extra := range []string{"", `"stream":true`} {
			r := h.post("/v1/chat/completions", chatBody("m1", 3, extra))
			h.expectErr(r, CodeContextTooLong)
			if !strings.Contains(r.message, "context (8192)") || strings.Contains(r.message, "maximum context length") || strings.Contains(r.message, "exceeds the available") {
				t.Fatalf("the friend must get the gateway's sentence, not the engine's: %s", r.message)
			}
		}
	}
	if c := h.gw.Counters("k_alice1"); c.TPMUsed != 0 || c.InFlight != 0 {
		t.Fatalf("an engine 400 mapped to 422 is the engine's row — counted, charged 0: %+v", c)
	}
	// vLLM's other 400 about lengths — max_tokens alone over max_model_len — is the friend's request, not its length.
	h.up.set("status:400", `{"error":{"message":"max_tokens=9000 cannot be greater than max_model_len=max_total_tokens=8192. Please request fewer output tokens. (parameter=max_tokens, value=9000)","type":"BadRequestError","param":"max_tokens","code":400}}`)
	h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 3, "")), CodeInvalidRequest)
	// The host's problems stay 502: 5xx, 401, 404, and an unparseable error body.
	for _, c := range []struct{ mode, body string }{
		{"status:500", `{"error":{"message":"engine exploded"}}`},
		{"status:401", `{"error":{"message":"invalid api key"}}`},
		{"status:404", `not here`},
	} {
		h.up.set(c.mode, c.body)
		r = h.post("/v1/chat/completions", chatBody("m1", 1, ""))
		h.expectErr(r, CodeUpstreamError)
		if !strings.Contains(r.message, strings.TrimPrefix(c.mode, "status:")) {
			t.Fatalf("%s message: %s", c.mode, r.message)
		}
	}
	if c := h.gw.Counters("k_alice1"); c.TodayTokens != 0 || c.InFlight != 0 {
		t.Fatalf("4xx charged tokens or leaked a slot: %+v", c)
	}
	ev := h.rec.last(t)
	if ev.Status != 502 || ev.Code != "upstream_error" {
		t.Fatalf("last event: %+v", ev)
	}
}

// ---- 10. bounded waiting queue ----

func TestWaitingQueueBounded(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.queueTimeout = 5 * time.Second
	h.up.set("sse", sseEvents(6, true)...) // 300 ms per stream
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	res1, err := h.streamReq(ctx1, chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	defer res1.Body.Close()
	if _, err := readEvent(bufio.NewReader(res1.Body)); err != nil { // running: holds the only slot
		t.Fatal(err)
	}
	// Four more at once: max(2, 2×Slots) = 2 may wait; the other two are refused on the spot.
	type outcome struct {
		r resp
		d time.Duration
	}
	outs := make(chan outcome, 4)
	for i := 0; i < 4; i++ {
		go func() {
			start := time.Now()
			r := h.post("/v1/chat/completions", chatBody("m1", 1, `"stream":true`))
			outs <- outcome{r, time.Since(start)}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case o := <-outs:
			h.expectErr(o.r, CodeQueueTimeout)
			if !strings.Contains(o.r.message, "2 requests already waiting") || o.d > time.Second {
				t.Fatalf("overflow must be refused at once with the waiting count: %q after %s", o.r.message, o.d)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("overflow requests were not refused immediately")
		}
	}
	if inF, w := h.gw.Queue(); inF != 1 || w != 2 {
		t.Fatalf("queue: %d running, %d waiting", inF, w)
	}
	cancel1()
	for i := 0; i < 2; i++ {
		select {
		case o := <-outs:
			if o.r.status != 200 {
				t.Fatalf("a waiting request must be served once the slot frees: %d %s", o.r.status, o.r.body)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waiting requests never completed")
		}
	}
	if inF, w := h.gw.Queue(); inF != 0 || w != 0 {
		t.Fatalf("queue after: %d running, %d waiting", inF, w)
	}
	// The two refused on the spot never held a place: they do not count against RPM (DESIGN §1.4).
	if c := h.gw.Counters("k_alice1"); c.InFlight != 0 || c.RPMUsed != 3 {
		t.Fatalf("per-key slot leaked, or a refused waiter counted: %+v", c)
	}
}

// ---- 11. /me discloses prompt logging ----

func TestMeLogPromptsDisclosure(t *testing.T) {
	for _, on := range []bool{false, true} {
		h := newHarness(t, Config{LogPrompts: on}, nil)
		var m struct {
			Host struct {
				LogPrompts *bool `json:"log_prompts"`
			} `json:"host"`
		}
		r := h.get("/me")
		if err := json.Unmarshal(r.body, &m); err != nil || r.status != 200 {
			t.Fatalf("/me: %d %s (%v)", r.status, r.body, err)
		}
		if m.Host.LogPrompts == nil || *m.Host.LogPrompts != on {
			t.Fatalf("LogPrompts=%v: /me host.log_prompts = %v", on, m.Host.LogPrompts)
		}
	}
}

// ---- the one exit ----

// A panic in the middle of the pipeline (here inside tokenize, with the per-key slot and the body
// held) still leaves through finish: net/http recovers the panic per connection, the deferred finish
// releases everything and records the event, and the key is usable on the next request.
func TestPanicLeavesThroughFinish(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 1; k.Limits.RPM = 5 })
	h.up.mu.Lock()
	h.up.countPanic = true
	h.up.mu.Unlock()
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader(chatBody("m1", 2, "")))
	req.Header.Set("Authorization", "Bearer "+testSecret)
	if res, err := h.srv.Client().Do(req); err == nil {
		res.Body.Close()
		t.Fatalf("a panicking handler must not produce a response: %d", res.StatusCode)
	}
	ev := h.rec.last(t)
	if ev.KeyID != "k_alice1" || ev.Status != 0 || ev.Endpoint != "/v1/chat/completions" {
		t.Fatalf("panic event: %+v", ev)
	}
	if c := h.gw.Counters("k_alice1"); c.InFlight != 0 || c.RPMUsed != 0 || c.TPMUsed != 0 {
		t.Fatalf("panic leaked a slot or a reservation: %+v", c)
	}
	if h.gw.bodies.Load() != 0 {
		t.Fatal("panic leaked the body buffer")
	}
	h.up.mu.Lock()
	h.up.countPanic = false
	h.up.mu.Unlock()
	if r := h.post("/v1/chat/completions", chatBody("m1", 2, "")); r.status != 200 {
		t.Fatalf("after the panic: %d %s", r.status, r.body)
	}
}
