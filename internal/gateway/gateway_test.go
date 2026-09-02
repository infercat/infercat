package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/upstream"
	"github.com/2185Lab/bunny-network/internal/usage"
)

func sseEvents(n int, withUsage bool) []string {
	var ev []string
	for i := 0; i < n; i++ {
		ev = append(ev, fmt.Sprintf(`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"t%d"}}]}`, i))
	}
	if withUsage {
		ev = append(ev, `{"id":"c","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":`+fmt.Sprint(n)+`}}`)
	}
	return ev
}

// ---- auth and routing ----

func TestHealthzNoAuth(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	r := h.do(http.MethodGet, "/healthz", "", "")
	if r.status != 200 || string(r.body) != `{"ok":true}` {
		t.Fatalf("healthz: %d %s", r.status, r.body)
	}
	if len(h.rec.events) != 0 {
		t.Fatal("healthz must not record usage")
	}
}

func TestAuthCodes(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.expectErr(h.do(http.MethodGet, "/me", "", ""), CodeInvalidKey)
	h.expectErr(h.do(http.MethodGet, "/me", "Bearer nope", ""), CodeInvalidKey)
	h.expectErr(h.do(http.MethodGet, "/me", "Basic "+testSecret, ""), CodeInvalidKey)
	h.expectErr(h.do(http.MethodGet, "/me", "Bearer", ""), CodeInvalidKey)
	if r := h.do(http.MethodGet, "/me", "bearer "+testSecret, ""); r.status != 200 {
		t.Fatalf("scheme must be case-insensitive: %d", r.status)
	}
	h.setKey(func(k *keys.Key) { k.Status = keys.Paused })
	h.expectErr(h.get("/me"), CodeKeyPaused)
	h.setKey(func(k *keys.Key) { k.Status = keys.Revoked })
	h.expectErr(h.get("/me"), CodeKeyRevoked)
	h.setKey(func(k *keys.Key) { k.Status = "weird" })
	h.expectErr(h.get("/me"), CodeKeyPaused) // fail closed
	h.setKey(func(k *keys.Key) { k.Status = keys.Active })
	h.store.mu.Lock()
	h.store.err = errors.New("disk on fire")
	h.store.mu.Unlock()
	h.expectErr(h.get("/me"), CodeUpstreamDown)
	h.store.mu.Lock()
	h.store.err = nil
	h.store.mu.Unlock()

	// Every request goes through Lookup (no caching): revoke, then the very next call is refused.
	if r := h.get("/me"); r.status != 200 {
		t.Fatalf("active again: %d", r.status)
	}
	h.setKey(func(k *keys.Key) { k.Status = keys.Revoked })
	h.expectErr(h.get("/me"), CodeKeyRevoked)

	evs := h.rec.waitFor(t, 9)
	if evs[0].Status != 401 || evs[0].Code != "invalid_key" || evs[0].KeyID != "" {
		t.Fatalf("rejected requests record an event: %+v", evs[0])
	}
}

func TestNotFound(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.expectErr(h.get("/v1/nope"), CodeNotFound)
	h.expectErr(h.post("/me", "{}"), CodeNotFound)
	h.expectErr(h.do(http.MethodGet, "/v1/chat/completions", "bearer", ""), CodeNotFound)
}

func TestMeShape(t *testing.T) {
	h := newHarness(t, Config{HostName: "maxbox", RelayRegion: func() string { return "sfo" }}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"m2", "ghost"}; k.Limits.RPM = 20 })
	h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 8192 })
	r := h.get("/me")
	if r.status != 200 {
		t.Fatalf("/me: %d %s", r.status, r.body)
	}
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatal(err)
	}
	keysOf := func(v any) []string {
		var out []string
		for k := range v.(map[string]any) {
			out = append(out, k)
		}
		return out
	}
	want := map[string][]string{
		"":         {"key", "limits", "usage", "host"},
		"key":      {"id", "name", "status"},
		"usage":    {"rpm_used", "tpm_used", "today_tokens", "in_flight"},
		"host":     {"name", "upstream", "models", "relay"},
		"upstream": {"kind", "healthy", "model_context"},
		"relay":    {"region"},
	}
	got := map[string][]string{
		"": keysOf(m), "key": keysOf(m["key"]), "usage": keysOf(m["usage"]), "host": keysOf(m["host"]),
		"upstream": keysOf(m["host"].(map[string]any)["upstream"]), "relay": keysOf(m["host"].(map[string]any)["relay"]),
	}
	for section, w := range want {
		g := got[section]
		if len(g) != len(w) {
			t.Fatalf("/me %q keys: want %v got %v", section, w, g)
		}
		for _, k := range w {
			found := false
			for _, gk := range g {
				found = found || gk == k
			}
			if !found {
				t.Fatalf("/me %q missing key %q in %v", section, k, g)
			}
		}
	}
	host := m["host"].(map[string]any)
	if host["name"] != "maxbox" || host["relay"].(map[string]any)["region"] != "sfo" {
		t.Fatalf("host: %v", host)
	}
	if models := host["models"].([]any); len(models) != 1 || models[0] != "m2" {
		t.Fatalf("models must be filtered by the allowlist: %v", models)
	}
	if up := host["upstream"].(map[string]any); up["kind"] != "llama.cpp" || up["healthy"] != true || up["model_context"] != float64(8192) {
		t.Fatalf("upstream: %v", up)
	}
	if k := m["key"].(map[string]any); k["id"] != "k_alice1" || k["name"] != "alice" || k["status"] != "active" {
		t.Fatalf("key: %v", k)
	}
	if lim := m["limits"].(map[string]any); lim["rpm"] != float64(20) {
		t.Fatalf("limits: %v", lim)
	}
	ev := h.rec.last(t)
	if ev.Endpoint != "/me" || ev.Status != 200 || ev.KeyID != "k_alice1" {
		t.Fatalf("event: %+v", ev)
	}
}

func TestModelsFiltered(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"m3", "m1"} })
	r := h.get("/v1/models")
	if r.status != 200 {
		t.Fatalf("models: %d %s", r.status, r.body)
	}
	var list struct {
		Object string
		Data   []struct{ ID, Object string }
	}
	if err := json.Unmarshal(r.body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Object != "list" || len(list.Data) != 2 || list.Data[0].ID != "m1" || list.Data[1].ID != "m3" || list.Data[0].Object != "model" {
		t.Fatalf("filtered list: %s", r.body)
	}
	if h.up.lastAuth != "" {
		t.Fatalf("the friend's Authorization must not reach the upstream: %q", h.up.lastAuth)
	}
}

// ---- body, model, clamps ----

func TestBodyCap(t *testing.T) {
	h := newHarness(t, Config{MaxBody: 100}, nil)
	big := chatBody("m1", 60, "")
	h.expectErr(h.post("/v1/chat/completions", big), CodeBodyTooLarge)
	if h.up.requests.Load() != 0 {
		t.Fatal("over-cap body must be refused before proxying")
	}
	// Unknown length (chunked) hits the same cap while reading.
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", io.NopCloser(strings.NewReader(big)))
	req.ContentLength = 0
	req.Header.Set("Authorization", "Bearer "+testSecret)
	res, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 413 {
		t.Fatalf("chunked over-cap: %d", res.StatusCode)
	}
	if r := h.post("/v1/chat/completions", chatBody("m1", 3, "")); r.status != 200 {
		t.Fatalf("small body: %d %s", r.status, r.body)
	}
}

func TestInvalidJSON(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.expectErr(h.post("/v1/chat/completions", `{"model":`), CodeInvalidRequest)
	h.expectErr(h.post("/v1/chat/completions", `[1,2]`), CodeInvalidRequest)
	h.expectErr(h.post("/v1/embeddings", `null`), CodeInvalidRequest)
}

func TestModelAllowlistAndFill(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"m2"} })
	h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 2, "")), CodeModelNotAllowed)
	if h.up.requests.Load() != 0 {
		t.Fatal("disallowed model must not be proxied")
	}
	if r := h.post("/v1/chat/completions", chatBody("m2", 2, "")); r.status != 200 {
		t.Fatalf("allowed model: %d %s", r.status, r.body)
	}
	// Missing model: first upstream model the key allows.
	if r := h.post("/v1/chat/completions", chatBody("", 2, "")); r.status != 200 {
		t.Fatalf("missing model: %d %s", r.status, r.body)
	}
	if got := h.up.body(t)["model"]; got != "m2" {
		t.Fatalf("model fill with allowlist: %v", got)
	}
	ev := h.rec.last(t)
	if ev.Model != "m2" {
		t.Fatalf("event model: %+v", ev)
	}
	// No allowlist: upstream's first model.
	h.setKey(func(k *keys.Key) { k.Limits.Models = nil })
	h.post("/v1/chat/completions", chatBody("", 2, ""))
	if got := h.up.body(t)["model"]; got != "m1" {
		t.Fatalf("model fill without allowlist: %v", got)
	}
	// Allowlist names a model the upstream does not list: the key's first allowed.
	h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"ghost"} })
	h.post("/v1/chat/completions", chatBody("", 2, ""))
	if got := h.up.body(t)["model"]; got != "ghost" {
		t.Fatalf("model fill from allowlist only: %v", got)
	}
}

func TestMaxTokensClampAndPassthrough(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.MaxOutputTokens = 100 })
	cases := []struct {
		extra string
		field string
		want  string
	}{
		{`"max_tokens":500`, "max_tokens", "100"},
		{`"max_tokens":50`, "max_tokens", "50"},
		{`"max_tokens":-1`, "max_tokens", "100"},
		{`"max_completion_tokens":500`, "max_completion_tokens", "100"},
		{``, "max_tokens", "100"},
	}
	for _, c := range cases {
		if r := h.post("/v1/chat/completions", chatBody("m1", 2, c.extra)); r.status != 200 {
			t.Fatalf("%s: %d %s", c.extra, r.status, r.body)
		}
		if got := h.up.body(t)[c.field]; fmt.Sprint(got) != c.want {
			t.Fatalf("%s: upstream saw %s=%v, want %s", c.extra, c.field, got, c.want)
		}
	}
	// No clamp configured: absent stays absent, values stay as sent.
	h.setKey(func(k *keys.Key) { k.Limits.MaxOutputTokens = 0 })
	h.post("/v1/chat/completions", chatBody("m1", 2, `"max_tokens":9999`))
	if got := h.up.body(t)["max_tokens"]; fmt.Sprint(got) != "9999" {
		t.Fatalf("unclamped: %v", got)
	}
	h.post("/v1/chat/completions", chatBody("m1", 2, ""))
	if _, ok := h.up.body(t)["max_tokens"]; ok {
		t.Fatal("no clamp: max_tokens must not be invented")
	}
	// Unknown fields and number literals pass through untouched.
	h.post("/v1/chat/completions", chatBody("m1", 2, `"foo":{"bar":[1,2.50,"x",null]},"seed":12345678901234567890,"temperature":0.7`))
	body := h.up.body(t)
	wantFoo := map[string]any{"bar": []any{json.Number("1"), json.Number("2.50"), "x", nil}}
	if !reflect.DeepEqual(body["foo"], wantFoo) || body["seed"] != json.Number("12345678901234567890") || body["temperature"] != json.Number("0.7") {
		t.Fatalf("passthrough: %v", body)
	}
}

// Context handling (002 promise 4, shrink-to-fit per 005 fix 10c): the prompt alone must fit;
// prompt + max_tokens overshooting shrinks max_tokens to what remains, floor 16, never a 422.
func TestContextTooLongBoundary(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 100 })
	h.setKey(func(k *keys.Key) { k.Limits.MaxOutputTokens = 40 })
	post := func(words int, extra string) resp {
		return h.post("/v1/chat/completions", chatBody("m1", words, extra))
	}
	maxTok := func() string { return fmt.Sprint(h.up.body(t)["max_tokens"]) }
	// 60 + 40 = 100: exactly fits, untouched.
	if r := post(60, ""); r.status != 200 || maxTok() != "40" {
		t.Fatalf("60 + 40 must fit untouched: %d %s max_tokens=%s", r.status, r.body, maxTok())
	}
	// 61 + 40 > 100: shrink to the 39 that remain.
	if r := post(61, ""); r.status != 200 || maxTok() != "39" {
		t.Fatalf("61 + 40 must shrink to 39: %d %s max_tokens=%s", r.status, r.body, maxTok())
	}
	// 100: the prompt fills the context; the floor still lets the engine answer.
	if r := post(100, ""); r.status != 200 || maxTok() != "16" {
		t.Fatalf("a prompt that fills the context gets the floor: %d %s max_tokens=%s", r.status, r.body, maxTok())
	}
	// 101: the prompt alone does not fit.
	r := post(101, "")
	h.expectErr(r, CodeContextTooLong)
	if !strings.Contains(r.message, "101 tokens") || !strings.Contains(r.message, "context is 100") {
		t.Fatalf("message must carry the numbers: %s", r.message)
	}
	// Key's max_context lower than the upstream's wins.
	h.setKey(func(k *keys.Key) { k.Limits.MaxContext = 80 })
	if r := post(80, ""); r.status != 200 || maxTok() != "16" {
		t.Fatalf("80 must fit under the key's 80: %d %s max_tokens=%s", r.status, r.body, maxTok())
	}
	h.expectErr(post(81, ""), CodeContextTooLong)
	// Key's max_context higher than the upstream's: upstream wins.
	h.setKey(func(k *keys.Key) { k.Limits.MaxContext = 200 })
	h.expectErr(post(101, ""), CodeContextTooLong)
	// The request's own smaller cap already fits: untouched.
	if r := post(95, `"max_tokens":2`); r.status != 200 || maxTok() != "2" {
		t.Fatalf("95 + 2 must fit untouched: %d %s max_tokens=%s", r.status, r.body, maxTok())
	}
	// max_completion_tokens is rewritten in place; no max_tokens is invented beside it.
	if r := post(95, `"max_completion_tokens":30`); r.status != 200 {
		t.Fatalf("95 + 30 must shrink, not fail: %d %s", r.status, r.body)
	}
	if b := h.up.body(t); fmt.Sprint(b["max_completion_tokens"]) != "16" || b["max_tokens"] != nil {
		t.Fatalf("max_completion_tokens shrink: %v", b)
	}
	// Unknown context everywhere: no check.
	h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 0 })
	h.setKey(func(k *keys.Key) { k.Limits.MaxContext = 0 })
	if r := post(500, ""); r.status != 200 {
		t.Fatalf("no context known: %d %s", r.status, r.body)
	}
	// Tokenizer failure degrades to an estimate, never a rejection of the whole request.
	h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 10 })
	h.up.mu.Lock()
	h.up.countErr = errors.New("tokenize down")
	h.up.mu.Unlock()
	if r := post(2, ""); r.status != 200 { // "w w" = 3 chars → 1 token
		t.Fatalf("estimate path: %d %s", r.status, r.body)
	}
}

// ---- limits ----

func TestRPM(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.RPM = 2 })
	for i := 0; i < 2; i++ {
		if r := h.post("/v1/chat/completions", chatBody("m1", 1, "")); r.status != 200 {
			t.Fatalf("request %d: %d %s", i, r.status, r.body)
		}
	}
	r := h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h.expectErr(r, CodeRateLimited)
	if ra := r.header.Get("Retry-After"); ra != "60" {
		t.Fatalf("Retry-After for a full window just opened: %q", ra)
	}
	// Rejections do not count: /me still says 2 used.
	var m struct {
		Usage struct {
			RPMUsed int `json:"rpm_used"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(h.get("/me").body, &m)
	if m.Usage.RPMUsed != 2 {
		t.Fatalf("rpm_used: %d", m.Usage.RPMUsed)
	}
	// /me and /v1/models are not rate limited.
	if r := h.get("/v1/models"); r.status != 200 {
		t.Fatalf("models under rpm: %d", r.status)
	}
}

func TestTPMAndDailyAfterRealUsage(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.TPM = 10 })
	// upstream reports 4+4 = 8 tokens per call; pre-check prompt is 2.
	for i := 0; i < 2; i++ {
		if r := h.post("/v1/chat/completions", chatBody("m1", 2, "")); r.status != 200 {
			t.Fatalf("request %d: %d %s", i, r.status, r.body)
		}
		h.rec.waitFor(t, i+1)
	}
	r := h.post("/v1/chat/completions", chatBody("m1", 2, ""))
	h.expectErr(r, CodeRateLimited)
	if !strings.Contains(r.message, "16 used") {
		t.Fatalf("message: %s", r.message)
	}
	c := h.gw.Counters("k_alice1")
	if c.TPMUsed != 16 || c.TodayTokens != 16 || c.RPMUsed != 2 || c.InFlight != 0 {
		t.Fatalf("counters: %+v", c)
	}

	h2 := newHarness(t, Config{}, nil)
	h2.setKey(func(k *keys.Key) { k.Limits.DailyTokens = 10 })
	for i := 0; i < 2; i++ {
		if r := h2.post("/v1/chat/completions", chatBody("m1", 2, "")); r.status != 200 {
			t.Fatalf("daily request %d: %d %s", i, r.status, r.body)
		}
		h2.rec.waitFor(t, i+1)
	}
	r = h2.post("/v1/chat/completions", chatBody("m1", 2, ""))
	h2.expectErr(r, CodeBudgetExhausted)
	var ra int
	fmt.Sscanf(r.header.Get("Retry-After"), "%d", &ra)
	if ra < 1 || ra > 86400 {
		t.Fatalf("Retry-After to UTC midnight: %d", ra)
	}
	ev := h2.rec.last(t)
	if ev.Status != 429 || ev.Code != "budget_exhausted" || ev.PromptTokens != 2 {
		t.Fatalf("rejected event: %+v", ev)
	}
}

func TestPerKeyConcurrency(t *testing.T) {
	h := newHarness(t, Config{Slots: 4}, nil)
	h.up.set("sse", sseEvents(20, true)...)
	h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 1 })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, err := h.streamReq(ctx, chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if _, err := readEvent(bufio.NewReader(res.Body)); err != nil {
		t.Fatal(err)
	}
	r := h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h.expectErr(r, CodeConcurrencyLimited)
	if r.header.Get("Retry-After") != "1" {
		t.Fatalf("Retry-After: %q", r.header.Get("Retry-After"))
	}
	if c := h.gw.Counters("k_alice1"); c.InFlight != 1 {
		t.Fatalf("in flight: %+v", c)
	}
	if inF, wait := h.gw.Queue(); inF != 1 || wait != 0 {
		t.Fatalf("queue: %d %d", inF, wait)
	}
	cancel()
	h.rec.waitFor(t, 2)
	deadline := time.Now().Add(2 * time.Second)
	for h.gw.Counters("k_alice1").InFlight != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c := h.gw.Counters("k_alice1"); c.InFlight != 0 {
		t.Fatalf("slot not released: %+v", c)
	}
}

func TestGlobalQueueTimeout(t *testing.T) {
	h := newHarness(t, Config{Slots: 1, QueueTimeout: 150 * time.Millisecond}, nil)
	h.up.set("sse", sseEvents(20, true)...)
	h.store.set("second", &keys.Key{ID: "k_bob", Name: "bob", Status: keys.Active})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, err := h.streamReq(ctx, chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	<-h.up.started
	start := time.Now()
	r := h.do(http.MethodPost, "/v1/chat/completions", "Bearer second", chatBody("m1", 1, ""))
	h.expectErr(r, CodeQueueTimeout)
	if d := time.Since(start); d < 150*time.Millisecond || d > 2*time.Second {
		t.Fatalf("queue wait %s", d)
	}
	evs := h.rec.waitFor(t, 1)
	if evs[0].KeyID != "k_bob" || evs[0].QueuedMS < 100 {
		t.Fatalf("queued_ms recorded: %+v", evs[0])
	}
	cancel()
}

// Ticket 005 fix 10d: SetSlots widens the global queue for new arrivals (bob would otherwise get
// 503 queue_timeout behind alice, as TestGlobalQueueTimeout shows), while alice, holding a slot
// on the old size, finishes and releases cleanly.
func TestSetSlotsResizesTheGlobalQueue(t *testing.T) {
	h := newHarness(t, Config{Slots: 1, QueueTimeout: 150 * time.Millisecond}, nil)
	h.up.set("sse", sseEvents(20, true)...)
	h.store.set("second", &keys.Key{ID: "k_bob", Name: "bob", Status: keys.Active})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, err := h.streamReq(ctx, chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	<-h.up.started
	if in, w := h.gw.Queue(); in != 1 || w != 0 {
		t.Fatalf("Queue() = %d, %d; want alice in flight", in, w)
	}
	h.gw.SetSlots(2)
	r := h.do(http.MethodPost, "/v1/chat/completions", "Bearer second", chatBody("m1", 1, `"stream":true`))
	if r.status != 200 {
		t.Fatalf("with 2 slots bob must run beside alice: %d %s", r.status, r.body)
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for {
		in, w := h.gw.Queue()
		if in == 0 && w == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Queue() = %d, %d after both finished; want 0, 0", in, w)
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.gw.SetSlots(0)
	if h.gw.cfg.Slots != 1 || cap(h.gw.sem) != 1 {
		t.Fatalf("SetSlots(0) must mean 1, got %d/%d", h.gw.cfg.Slots, cap(h.gw.sem))
	}
}

func TestUpstreamFailures(t *testing.T) {
	h := newHarness(t, Config{RequestTimeout: 200 * time.Millisecond}, nil)
	h.up.set("500")
	r := h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h.expectErr(r, CodeUpstreamError)
	if !strings.Contains(r.message, "HTTP 500") || !strings.Contains(r.message, "engine exploded") {
		t.Fatalf("message: %s", r.message)
	}
	h.up.set("garbage")
	h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeUpstreamError)
	h.up.set("hang")
	start := time.Now()
	r = h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h.expectErr(r, CodeUpstreamError)
	if d := time.Since(start); d < 200*time.Millisecond || d > 3*time.Second {
		t.Fatalf("timeout took %s", d)
	}
	select {
	case <-h.up.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed-out upstream request was not cancelled")
	}
	if c := h.gw.Counters("k_alice1"); c.TodayTokens != 0 || c.InFlight != 0 {
		t.Fatalf("failures must not charge tokens or leak slots: %+v", c)
	}
	// Reported unhealthy: refused before admission, no RPM consumed.
	h.up.set("json")
	h.up.setInfo(func(i *upstream.Info) { i.Healthy = false })
	rpmBefore := h.gw.Counters("k_alice1").RPMUsed
	r = h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h.expectErr(r, CodeUpstreamDown)
	if r.header.Get("Retry-After") != "10" || h.gw.Counters("k_alice1").RPMUsed != rpmBefore {
		t.Fatalf("unhealthy: %v rpm %d→%d", r.header, rpmBefore, h.gw.Counters("k_alice1").RPMUsed)
	}
	// Unreachable engine.
	d := newDeadUpstream(t)
	h2 := newHarness(t, Config{}, d)
	r = h2.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h2.expectErr(r, CodeUpstreamDown)
	if strings.Contains(r.message, "127.0.0.1") {
		t.Fatalf("engine address leaked to the friend: %s", r.message)
	}
	h2.expectErr(h2.get("/v1/models"), CodeUpstreamDown)
}

// ---- streaming ----

func TestStreamFlushBeforeUpstreamFinishes(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.set("sse", sseEvents(5, true)...)
	start := time.Now()
	res, err := h.streamReq(context.Background(), chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	br := bufio.NewReader(res.Body)
	first, err := readEvent(br)
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Since(start)
	if h.up.finished.Load() {
		t.Fatal("first event arrived only after the upstream finished: no immediate flush")
	}
	if !strings.HasPrefix(first, `data: {"id":"c"`) {
		t.Fatalf("first event: %q", first)
	}
	rest, _ := io.ReadAll(br)
	total := time.Since(start)
	if !h.up.finished.Load() || !strings.HasSuffix(strings.TrimSpace(string(rest)), "data: [DONE]") {
		t.Fatalf("stream end: finished=%v tail=%q", h.up.finished.Load(), rest)
	}
	if firstAt > total/2 {
		t.Fatalf("first event at %s of %s total: not streaming", firstAt, total)
	}
	if first+string(rest) != h.up.written() {
		t.Fatal("stream bytes differ from what the upstream wrote")
	}
	ev := h.rec.last(t)
	if !ev.Stream || ev.Status != 200 || ev.PromptTokens != 7 || ev.CompletionTokens != 5 || ev.TTFTMS > total.Milliseconds() || ev.TotalMS < 200 {
		t.Fatalf("stream event: %+v", ev)
	}
	// include_usage was injected.
	so, _ := h.up.body(t)["stream_options"].(map[string]any)
	if so["include_usage"] != true {
		t.Fatalf("include_usage not injected: %v", h.up.body(t))
	}
	if ev.Prompt != "" || ev.Completion != "" {
		t.Fatal("prompt content recorded without LogPrompts")
	}
}

func TestIncludeUsageInjectionKeepsOtherOptions(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.set("sse", sseEvents(1, true)...)
	res, err := h.streamReq(context.Background(), chatBody("m1", 1, `"stream":true,"stream_options":{"foo":1}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	so, _ := h.up.body(t)["stream_options"].(map[string]any)
	if so["include_usage"] != true || so["foo"] != json.Number("1") {
		t.Fatalf("stream_options: %v", so)
	}
	// Non-stream: nothing injected.
	h.up.set("json")
	h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	if _, ok := h.up.body(t)["stream_options"]; ok {
		t.Fatal("stream_options injected into a non-stream request")
	}
}

func TestReasoningContentPassthroughByteForByte(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	events := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Let me think… <b> \"quoted\" \\ tab\there"}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"日本語 🐰 and \n newline"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"answer","reasoning_content":null}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":3}}`,
	}
	h.up.set("sse", events...)
	res, err := h.streamReq(context.Background(), chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(got) != h.up.written() {
		t.Fatalf("bytes differ:\n got %q\nwant %q", got, h.up.written())
	}
	for _, e := range events {
		if !strings.Contains(string(got), "data: "+e+"\n\n") {
			t.Fatalf("event not verbatim: %s", e)
		}
	}
}

func TestClientDisconnectCancelsUpstream(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.set("sse", sseEvents(200, true)...) // 10 s if left alone
	ctx, cancel := context.WithCancel(context.Background())
	res, err := h.streamReq(ctx, chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(res.Body)
	for i := 0; i < 3; i++ {
		if _, err := readEvent(br); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	res.Body.Close()
	select {
	case <-h.up.cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream never observed cancellation after the client left")
	}
	if h.up.finished.Load() {
		t.Fatal("upstream ran to completion")
	}
	ev := h.rec.last(t)
	if ev.Status != 200 || ev.Code != "client_closed" || ev.CompletionTokens < 3 || ev.CompletionTokens > 10 || ev.PromptTokens != 1 {
		t.Fatalf("aborted stream is charged by chunks seen: %+v", ev)
	}
	if c := h.gw.Counters("k_alice1"); c.InFlight != 0 || c.TodayTokens != ev.PromptTokens+ev.CompletionTokens {
		t.Fatalf("counters after abort: %+v", c)
	}
}

func TestStreamWithoutUsageFallsBackToChunkCount(t *testing.T) {
	h := newHarness(t, Config{LogPrompts: true}, nil)
	h.up.set("sse", sseEvents(4, false)...)
	res, err := h.streamReq(context.Background(), chatBody("m1", 3, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	ev := h.rec.last(t)
	if ev.PromptTokens != 3 || ev.CompletionTokens != 4 || ev.Code != "" {
		t.Fatalf("fallback: %+v", ev)
	}
	if ev.Prompt != "w w w\n" || ev.Completion != "t0t1t2t3" {
		t.Fatalf("LogPrompts content: %q %q", ev.Prompt, ev.Completion)
	}
}

func TestStreamUpstreamDiesMidway(t *testing.T) {
	h := newHarness(t, Config{RequestTimeout: 300 * time.Millisecond}, nil)
	h.up.set("sse", sseEvents(50, true)...) // 2.5 s; the timeout cuts it
	res, err := h.streamReq(context.Background(), chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(got), `"code":"upstream_error"`) {
		t.Fatalf("a cut stream must end with an SSE error event: %q", got)
	}
	ev := h.rec.last(t)
	if ev.Status != 200 || ev.Code != "upstream_error" || ev.CompletionTokens < 2 {
		t.Fatalf("event: %+v", ev)
	}
}

// ---- non-stream, embeddings, usage ----

func TestNonStreamPassthroughAndUsage(t *testing.T) {
	h := newHarness(t, Config{LogPrompts: true}, nil)
	r := h.post("/v1/chat/completions", chatBody("m1", 2, ""))
	if r.status != 200 || string(r.body) != h.up.events[0] || r.header.Get("Content-Type") != "application/json" {
		t.Fatalf("passthrough: %d %s", r.status, r.body)
	}
	ev := h.rec.last(t)
	if ev.PromptTokens != 4 || ev.CompletionTokens != 4 || ev.Stream || ev.Model != "m1" || ev.Endpoint != "/v1/chat/completions" || ev.Completion != "hi there" || ev.Prompt != "w w\n" {
		t.Fatalf("event: %+v", ev)
	}
	if ev.TTFTMS > ev.TotalMS || ev.TotalMS < 0 {
		t.Fatalf("timings: %+v", ev)
	}
	// Usage absent in the body: pre-check prompt count, 0 completion.
	h.up.set("json", `{"choices":[{"message":{"content":"x"}}]}`)
	h.post("/v1/chat/completions", chatBody("m1", 5, ""))
	ev = h.rec.waitFor(t, 2)[1]
	if ev.PromptTokens != 5 || ev.CompletionTokens != 0 {
		t.Fatalf("fallback: %+v", ev)
	}
}

func TestEmbeddings(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.set("json", `{"object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"usage":{"prompt_tokens":9,"total_tokens":9}}`)
	r := h.post("/v1/embeddings", `{"input":["a b","c"]}`)
	if r.status != 200 || string(r.body) != h.up.events[0] {
		t.Fatalf("embeddings: %d %s", r.status, r.body)
	}
	if h.up.lastPath != "/v1/embeddings" || h.up.body(t)["model"] != "m1" {
		t.Fatalf("upstream saw %s %v", h.up.lastPath, h.up.body(t))
	}
	ev := h.rec.last(t)
	if ev.Endpoint != "/v1/embeddings" || ev.PromptTokens != 9 || ev.CompletionTokens != 0 {
		t.Fatalf("event: %+v", ev)
	}
	h.setKey(func(k *keys.Key) { k.Limits.TPM = 5 })
	h.expectErr(h.post("/v1/embeddings", `{"input":"x"}`), CodeRateLimited)
}

func TestSnapshotAllCounters(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.store.set("second", &keys.Key{ID: "k_bob", Name: "bob", Status: keys.Active})
	h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h.do(http.MethodGet, "/me", "Bearer second", "")
	h.rec.waitFor(t, 2)
	all := h.gw.AllCounters()
	if len(all) != 2 || all["k_alice1"].TodayTokens != 8 || all["k_bob"].LastSeen.IsZero() || all["k_bob"].TodayTokens != 0 {
		t.Fatalf("all counters: %+v", all)
	}
	if c := h.gw.Counters("nobody"); c != (usage.KeyCounters{}) {
		t.Fatalf("unknown key: %+v", c)
	}
}

// ---- servers ----

func TestServeAndShutdown(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.gw.Serve(l) }()
	waitHealthy(t, "http://"+l.Addr().String())
	if err := h.gw.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve after Shutdown: %v", err)
	}
	l2, _ := net.Listen("tcp", "127.0.0.1:0")
	if err := h.gw.Serve(l2); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve on a shut-down gateway: %v", err)
	}
}

func waitHealthy(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(base + "/healthz")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never became healthy", base)
}

func TestServeDevCORS(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	const addr = "localhost:29102"
	done := make(chan error, 1)
	go func() { done <- h.gw.ServeDev(addr) }()
	base := "http://127.0.0.1:29102"
	waitHealthy(t, base)

	req, _ := http.NewRequest(http.MethodOptions, base+"/v1/chat/completions", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != "*" ||
		res.Header.Get("Access-Control-Allow-Headers") != "authorization, content-type" ||
		!strings.Contains(res.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Fatalf("preflight: %d %v", res.StatusCode, res.Header)
	}
	req, _ = http.NewRequest(http.MethodGet, base+"/me", nil)
	req.Header.Set("Authorization", "Bearer "+testSecret)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Access-Control-Allow-Origin") != "*" || res.Header.Get("Access-Control-Expose-Headers") != "Retry-After" {
		t.Fatalf("dev /me: %d %v", res.StatusCode, res.Header)
	}
	if err := h.gw.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("ServeDev after Shutdown: %v", err)
	}
	// The tunnel handler itself has no CORS.
	if r := h.get("/me"); r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS leaked into the tunnel handler")
	}
}

func TestServeDevRefusesNonLoopback(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	for _, addr := range []string{"0.0.0.0:29103", ":29103", "192.168.1.10:29103", "[::]:29103", "example.com:80", "nonsense", "127.0.0.1"} {
		err := h.gw.ServeDev(addr)
		if err == nil {
			t.Fatalf("%q accepted", addr)
		}
		if c, derr := net.DialTimeout("tcp", "127.0.0.1:29103", 100*time.Millisecond); derr == nil {
			c.Close()
			t.Fatalf("%q: something is listening after refusal", addr)
		}
	}
	// ::1 is loopback and accepted (skipped where IPv6 loopback is unavailable).
	if l, err := net.Listen("tcp", "[::1]:0"); err == nil {
		l.Close()
		done := make(chan error, 1)
		go func() { done <- h.gw.ServeDev("[::1]:29104") }()
		waitHealthy(t, "http://[::1]:29104")
		_ = h.gw.Shutdown(context.Background())
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

// ---- limiter unit tests with a fake clock ----

func TestLimiterWindowsWithFakeClock(t *testing.T) {
	l := newLimiter()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	lim := keys.Limits{RPM: 2, TPM: 100, DailyTokens: 150}

	must := func(e *gwError) {
		t.Helper()
		if e != nil {
			t.Fatalf("unexpected: %v", e)
		}
	}
	must(l.admit("k", lim, 10))
	l.release("k", 60)
	now = now.Add(10 * time.Second)
	must(l.admit("k", lim, 10))
	l.release("k", 30)
	// RPM full: Retry-After = when the first admission leaves the window (50 s).
	e := l.admit("k", lim, 10)
	if e == nil || e.Code != CodeRateLimited || e.RetryAfter != 50 {
		t.Fatalf("rpm: %+v", e)
	}
	now = now.Add(50 * time.Second) // t=60: first admission expires; its 60 tokens too (charged at t=0)
	c := l.counters("k")
	if c.RPMUsed != 1 || c.TPMUsed != 30 || c.TodayTokens != 90 {
		t.Fatalf("after expiry: %+v", c)
	}
	// TPM: 30 used + 80 requested > 100; retry when the 30-token charge (t=10) expires at t=70 → 10 s.
	e = l.admit("k", lim, 80)
	if e == nil || e.RetryAfter != 10 || !strings.Contains(e.Message, "token limit") {
		t.Fatalf("tpm: %+v", e)
	}
	// A request bigger than the whole TPM can never fit: Retry-After is the full window.
	if e = l.admit("k", lim, 101); e == nil || e.RetryAfter != 60 {
		t.Fatalf("oversize: %+v", e)
	}
	// Daily: 90 used today. Jump to one second before UTC midnight (same day, window empty).
	now = time.Date(2026, 9, 2, 23, 59, 59, 0, time.UTC)
	must(l.admit("k", lim, 1))
	l.release("k", 59) // 149 today
	e = l.admit("k", lim, 2)
	if e == nil || e.Code != CodeBudgetExhausted || e.RetryAfter != 1 {
		t.Fatalf("daily: %+v", e)
	}
	// Midnight rolls the day.
	now = time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	if c := l.counters("k"); c.TodayTokens != 0 {
		t.Fatalf("day did not roll: %+v", c)
	}
	must(l.admit("k", keys.Limits{DailyTokens: 150}, 100))
	// Concurrency and release bookkeeping.
	l2 := newLimiter()
	must(l2.admit("k", keys.Limits{MaxConcurrent: 2}, 0))
	must(l2.admit("k", keys.Limits{MaxConcurrent: 2}, 0))
	if e := l2.admit("k", keys.Limits{MaxConcurrent: 2}, 0); e == nil || e.Code != CodeConcurrencyLimited || e.RetryAfter != 1 {
		t.Fatalf("concurrency: %+v", e)
	}
	l2.release("k", 0)
	must(l2.admit("k", keys.Limits{MaxConcurrent: 2}, 0))
	// Zero limits mean unlimited.
	for i := 0; i < 50; i++ {
		must(l2.admit("z", keys.Limits{}, 1000))
	}
}
