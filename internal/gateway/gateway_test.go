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
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
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

	// 7 of the 11 requests record an event: the four 401s record nothing (006 promise 7b); paused and
	// revoked rejections name the key (7a). TestAuditTruth covers the details.
	evs := h.rec.waitFor(t, 7)
	for _, ev := range evs {
		if ev.Status == 401 {
			t.Fatalf("a 401 recorded a usage event: %+v", ev)
		}
		if ev.Status == 403 && ev.KeyID != "k_alice1" {
			t.Fatalf("a paused/revoked rejection must name the key: %+v", ev)
		}
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
		"host":     {"name", "upstream", "models", "vision", "audio", "relay", "log_prompts"},
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
	h := newHarness(t, Config{}, nil)
	h.gw.maxBody = 100
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
	h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 30 })
	h.up.mu.Lock()
	h.up.countErr = errors.New("tokenize down")
	h.up.mu.Unlock()
	if r := post(2, ""); r.status != 200 { // "w w" = 3 chars → 1 token, + the template allowance of 20 for one message (036)
		t.Fatalf("estimate path: %d %s", r.status, r.body)
	}
}

// 036: the pre-check counts what the engine will count — the prompt under the chat template, not
// its bare text. The fake's template costs 4 a message and 16 a chat (the estimate's allowance);
// a system prompt and 20 turns is 21 messages, 100 tokens of template. Context 300: 200 words of
// text fill it exactly (admitted, the floor), 201 do not (422 with the templated number, and the
// engine is never asked), and the shrink-to-fit leaves room for the template too.
func TestContextPrecheckCountsTheChatTemplate(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 300 })
	h.up.mu.Lock()
	h.up.tplPerMsg, h.up.tplBase = 4, 16
	h.up.mu.Unlock()
	h.setKey(func(k *keys.Key) { k.Limits.MaxOutputTokens = 40 })
	body := func(words int) string { // a one-word system prompt and 20 turns sharing the rest
		var sb strings.Builder
		sb.WriteString(`{"model":"m1","messages":[{"role":"system","content":"w"}`)
		left := words - 1
		for i := 0; i < 20; i++ {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			n := left / (20 - i)
			left -= n
			fmt.Fprintf(&sb, `,{"role":%q,"content":%q}`, role, strings.TrimSpace(strings.Repeat("w ", n)))
		}
		sb.WriteString("]}")
		return sb.String()
	}
	post := func(words int) resp { return h.post("/v1/chat/completions", body(words)) }
	maxTok := func() string { return fmt.Sprint(h.up.body(t)["max_tokens"]) }
	// 200 + 100 = 300: the templated prompt fills the context; the floor still lets the engine answer.
	if r := post(200); r.status != 200 || maxTok() != "16" {
		t.Fatalf("200 words + 100 of template must fill the context: %d %s max_tokens=%s", r.status, r.body, maxTok())
	}
	asked := h.up.requests.Load()
	// 201 + 100 = 301: over — while the bare text, 201, is far under. The message carries the templated number.
	r := post(201)
	h.expectErr(r, CodeContextTooLong)
	if !strings.Contains(r.message, "301 tokens") || !strings.Contains(r.message, "context is 300") {
		t.Fatalf("the 422 must carry the templated count: %s", r.message)
	}
	if h.up.requests.Load() != asked {
		t.Fatal("the engine was asked for a prompt the pre-check should have refused")
	}
	// 170 + 100 = 270, + 40 > 300: shrink to the 30 that remain under the templated count (the bare text would leave 90).
	if r := post(170); r.status != 200 || maxTok() != "30" {
		t.Fatalf("170 words + 100 of template + 40 must shrink to 30: %d %s max_tokens=%s", r.status, r.body, maxTok())
	}
	// The count was given the messages, so the engine's template is what was counted; embeddings are not a chat.
	if h.up.lastMsgs == nil {
		t.Fatal("the chat count was not given the messages")
	}
	if r := h.post("/v1/embeddings", `{"model":"m1","input":"w w w"}`); r.status != 200 || h.up.lastMsgs != nil {
		t.Fatalf("embeddings: %d, count given messages %v", r.status, h.up.lastMsgs != nil)
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
	// /me is not rate limited; /v1/models is (006 promise 5).
	if r := h.get("/me"); r.status != 200 {
		t.Fatalf("/me under rpm: %d", r.status)
	}
	h.expectErr(h.get("/v1/models"), CodeRateLimited)
}

// The reservation is the worst case, prompt + max_tokens, shrunk to what the window has left
// (DESIGN §1.4, 010): a key with TPM 26 and a 2-token prompt gets max_tokens 24, then 16 (the
// floor) once 8 are charged, then a 429 with the numbers once 16 are charged. (002's 10-token key
// is refused outright now: it cannot hold prompt + 16, which is the honest answer.)
func TestTPMAndDailyAfterRealUsage(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.setKey(func(k *keys.Key) { k.Limits.TPM = 26 })
	// upstream reports 4+4 = 8 tokens per call; pre-check prompt is 2.
	for i, wantCap := range []string{"24", "16"} {
		if r := h.post("/v1/chat/completions", chatBody("m1", 2, "")); r.status != 200 {
			t.Fatalf("request %d: %d %s", i, r.status, r.body)
		}
		if got := fmt.Sprint(h.up.body(t)["max_tokens"]); got != wantCap {
			t.Fatalf("request %d: max_tokens shrunk to %s, want %s", i, got, wantCap)
		}
		h.rec.waitFor(t, i+1)
	}
	r := h.post("/v1/chat/completions", chatBody("m1", 2, ""))
	h.expectErr(r, CodeRateLimited)
	if !strings.Contains(r.message, "16 used") || !strings.Contains(r.message, "at least 18") {
		t.Fatalf("message: %s", r.message)
	}
	c := h.gw.Counters("k_alice1")
	if c.TPMUsed != 16 || c.TodayTokens != 16 || c.RPMUsed != 2 || c.InFlight != 0 {
		t.Fatalf("counters: %+v", c)
	}
	h.setKey(func(k *keys.Key) { k.Limits.TPM = 10 })
	h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 2, "")), CodeRateLimited)

	h2 := newHarness(t, Config{}, nil)
	h2.setKey(func(k *keys.Key) { k.Limits.DailyTokens = 26 })
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

// Both friend personas watched their usage meter snap to zero when the host restarted ("I'd used
// 5.8k tokens a minute earlier"), and the daily budget went with it. A gateway started over the
// same usage.jsonl now begins the day where the last one left it (DESIGN §4 item 5). The sliding
// minute deliberately does not come back: rpm/tpm are questions about right now.
func TestRestartKeepsTodaysCountersAndLastSeen(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, Config{DataDir: dir}, nil)
	for i := 0; i < 2; i++ {
		if r := h.post("/v1/chat/completions", chatBody("m1", 2, "")); r.status != 200 {
			t.Fatalf("request %d: %d %s", i, r.status, r.body)
		}
		h.rec.waitFor(t, i+1)
	}
	before, beforeMe := h.gw.Counters("k_alice1"), meUsage(t, h.get("/me").body)
	if beforeMe.TodayTokens == 0 {
		t.Fatal("nothing was charged before the restart; the test proves nothing")
	}

	// The host stops: the server goes away and usage.jsonl is flushed and closed.
	h.srv.Close()
	if err := h.file.Close(); err != nil {
		t.Fatal(err)
	}
	h.gw = New(Config{DataDir: dir, HostName: "max-laptop"}, h.up, h.store, h.rec, globalLogs.logf)
	h.srv = httptest.NewServer(h.gw.Handler())
	defer h.srv.Close()

	// Read the seeded state before any request touches it.
	seeded := h.gw.Counters("k_alice1")
	if seeded.TodayTokens != before.TodayTokens {
		t.Fatalf("today_tokens after restart: %d, want the %d charged before", seeded.TodayTokens, before.TodayTokens)
	}
	if d := seeded.LastSeen.Sub(before.LastSeen); seeded.LastSeen.IsZero() || d > time.Minute || d < -time.Minute {
		t.Fatalf("last_seen after restart: %v, want ~%v", seeded.LastSeen, before.LastSeen)
	}
	if seeded.RPMUsed != 0 || seeded.InFlight != 0 {
		t.Fatalf("the sliding minute must start empty: %+v", seeded)
	}
	if got := meUsage(t, h.get("/me").body); got.TodayTokens != beforeMe.TodayTokens {
		t.Fatalf("/me today_tokens after restart: %d, want %d", got.TodayTokens, beforeMe.TodayTokens)
	}

	// A key with no history today, and a data dir with no usage.jsonl at all, both seed nothing.
	if c := h.gw.Counters("k_nobody"); c.TodayTokens != 0 || !c.LastSeen.IsZero() {
		t.Fatalf("a key absent from history: %+v", c)
	}
	if c := New(Config{DataDir: t.TempDir()}, h.up, h.store, h.rec, globalLogs.logf).Counters("k_alice1"); c.TodayTokens != 0 {
		t.Fatalf("empty data dir seeded %d tokens", c.TodayTokens)
	}
}

// meUsage is the usage block of /me, the numbers the friend's meter shows.
func meUsage(t *testing.T, body []byte) struct {
	RPMUsed     int `json:"rpm_used"`
	TPMUsed     int `json:"tpm_used"`
	TodayTokens int `json:"today_tokens"`
} {
	t.Helper()
	var m struct {
		Usage struct {
			RPMUsed     int `json:"rpm_used"`
			TPMUsed     int `json:"tpm_used"`
			TodayTokens int `json:"today_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("/me: %v\n%s", err, body)
	}
	return m.Usage
}

func TestPerKeyConcurrency(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.slots(4)
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
	var body errorBody
	if err := json.Unmarshal(r.body, &body); err != nil || body.Error.Limit != 1 || body.Error.InFlight != 1 {
		t.Fatalf("concurrency snapshot: %s (%v)", r.body, err)
	}
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
	h := newHarness(t, Config{}, nil)
	h.gw.queueTimeout = 150 * time.Millisecond
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

// The queue follows the engine's slot count on the next acquire (DESIGN §1.5; was 005 fix 10d's
// SetSlots push): an engine that reports 2 slots after alice took the only one lets bob run beside
// her at once, and an engine that reports 0 means 1.
func TestQueueFollowsEngineSlots(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.queueTimeout = 150 * time.Millisecond
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
	h.slots(2)
	r := h.do(http.MethodPost, "/v1/chat/completions", "Bearer second", chatBody("m1", 1, `"stream":true`))
	if r.status != 200 {
		t.Fatalf("with 2 slots bob must run beside alice: %d %s", r.status, r.body)
	}
	cancel()
	waitUntil(t, 2*time.Second, "both to finish", func() bool { in, w := h.gw.Queue(); return in == 0 && w == 0 })
	h.slots(0)
	h.up.set("sse", sseEvents(20, true)...)
	res2, err := h.streamReq(context.Background(), chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	waitUntil(t, 2*time.Second, "alice to hold the slot", func() bool { in, _ := h.gw.Queue(); return in == 1 })
	h.expectErr(h.do(http.MethodPost, "/v1/chat/completions", "Bearer second", chatBody("m1", 1, "")), CodeQueueTimeout)
}

func TestUpstreamFailures(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.firstByte(200 * time.Millisecond)
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
	if d := time.Since(start); d < 200*time.Millisecond || d > 3*time.Second || !strings.Contains(r.message, "did not answer in time") {
		t.Fatalf("first-byte timeout took %s: %s", d, r.message)
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
	h.up.setInfo(func(i *upstream.Info) { i.Health.OK = false })
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

func TestReasoningAliasesCountWithoutRewriting(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.up.set("sse",
		`{"choices":[{"delta":{"reasoning":"new name"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"preferred","reasoning":"ignored"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"","reasoning":"ignored too"}}]}`,
		`{"choices":[{"delta":{"content":"answer"}}]}`,
	)
	res, err := h.streamReq(context.Background(), chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != h.up.written() {
		t.Fatal("the side-channel rewrote stream bytes")
	}
	if ev := h.rec.last(t); ev.CompletionTokens != 3 {
		t.Fatalf("estimated %d tokens, want 3: %+v", ev.CompletionTokens, ev)
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

// An engine that goes quiet mid-stream is cut by the idle deadline (DESIGN §1.6), with an SSE
// error event so the friend sees why; the two events that arrived are charged.
func TestStreamUpstreamDiesMidway(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.idleTimeout = 300 * time.Millisecond
	h.up.set("sse", sseEvents(50, true)...)
	h.up.mu.Lock()
	h.up.stallAfter = 2
	h.up.mu.Unlock()
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
	if ev.Status != 200 || ev.Code != "upstream_error" || ev.CompletionTokens != 2 {
		t.Fatalf("event: %+v", ev)
	}
	select {
	case <-h.up.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the idle engine was not cancelled")
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

// admitAll is 002's one-shot admission (concurrency, RPM, TPM, daily) composed from 006/010's split:
// admit, then reserve the prompt (no output: the embeddings shape), settling the admission
// un-counted when the tokens do not fit.
func (l *limiter) admitAll(id string, lim keys.Limits, prompt int) (*admission, *gwError) {
	a, e := l.admit(id, lim)
	if e != nil {
		return nil, e
	}
	if _, e := l.reserve(a, lim, prompt, 0); e != nil {
		l.settle(a, false, 0)
		return nil, e
	}
	return a, nil
}

func TestLimiterWindowsWithFakeClock(t *testing.T) {
	l := newLimiter()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	lim := keys.Limits{RPM: 2, TPM: 100, DailyTokens: 150}

	must := func(a *admission, e *gwError) *admission {
		t.Helper()
		if e != nil {
			t.Fatalf("unexpected: %v", e)
		}
		return a
	}
	l.settle(must(l.admitAll("k", lim, 10)), true, 60)
	now = now.Add(10 * time.Second)
	l.settle(must(l.admitAll("k", lim, 10)), true, 30)
	// RPM full: Retry-After = when the first admission leaves the window (50 s).
	_, e := l.admitAll("k", lim, 10)
	if e == nil || e.Code != CodeRateLimited || e.RetryAfter != 50 {
		t.Fatalf("rpm: %+v", e)
	}
	now = now.Add(50 * time.Second) // t=60: first admission expires; its 60 tokens too (charged at t=0)
	c := l.counters("k")
	if c.RPMUsed != 1 || c.TPMUsed != 30 || c.TodayTokens != 90 {
		t.Fatalf("after expiry: %+v", c)
	}
	// TPM: 30 used + 80 requested > 100; retry when the 30-token charge (t=10) expires at t=70 → 10 s.
	_, e = l.admitAll("k", lim, 80)
	if e == nil || e.RetryAfter != 10 || !strings.Contains(e.Message, "token limit") {
		t.Fatalf("tpm: %+v", e)
	}
	// A request bigger than the whole TPM can never fit: Retry-After is the full window.
	if _, e = l.admitAll("k", lim, 101); e == nil || e.RetryAfter != 60 {
		t.Fatalf("oversize: %+v", e)
	}
	// Daily: 90 used today. Jump to one second before UTC midnight (same day, window empty).
	now = time.Date(2026, 9, 2, 23, 59, 59, 0, time.UTC)
	l.settle(must(l.admitAll("k", lim, 1)), true, 59) // 149 today
	_, e = l.admitAll("k", lim, 2)
	if e == nil || e.Code != CodeBudgetExhausted || e.RetryAfter != 1 {
		t.Fatalf("daily: %+v", e)
	}
	// Midnight rolls the day.
	now = time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	if c := l.counters("k"); c.TodayTokens != 0 {
		t.Fatalf("day did not roll: %+v", c)
	}
	must(l.admitAll("k", keys.Limits{DailyTokens: 150}, 100))
	// Concurrency and settle bookkeeping.
	l2 := newLimiter()
	a1 := must(l2.admitAll("k", keys.Limits{MaxConcurrent: 2}, 0))
	must(l2.admitAll("k", keys.Limits{MaxConcurrent: 2}, 0))
	if _, e := l2.admitAll("k", keys.Limits{MaxConcurrent: 2}, 0); e == nil || e.Code != CodeConcurrencyLimited || e.RetryAfter != 1 || e.Limit != 2 || e.InFlight != 2 {
		t.Fatalf("concurrency: %+v", e)
	}
	l2.settle(a1, true, 0)
	must(l2.admitAll("k", keys.Limits{MaxConcurrent: 2}, 0))
	// Zero limits mean unlimited.
	for i := 0; i < 50; i++ {
		must(l2.admitAll("z", keys.Limits{}, 1000))
	}
	// A settle that does not count removes exactly its own admission (by seq), not the newest.
	l3 := newLimiter()
	first := must(l3.admitAll("k", keys.Limits{MaxConcurrent: 3, RPM: 3}, 0))
	must(l3.admitAll("k", keys.Limits{MaxConcurrent: 3, RPM: 3}, 0))
	l3.settle(first, false, 0)
	if c := l3.counters("k"); c.RPMUsed != 1 || c.InFlight != 1 {
		t.Fatalf("un-counting the first admission: %+v", c)
	}
	if st := l3.state("k"); len(st.log) != 1 || st.log[0].seq != 2 {
		t.Fatalf("the surviving entry must be the second admission: %+v", st.log)
	}
}

func TestHostModelPinIntersectsEverySurface(t *testing.T) {
	for _, tc := range []struct {
		name      string
		key, want []string
	}{
		{"unrestricted key", nil, []string{"m2"}},
		{"overlap", []string{"m1", "m2"}, []string{"m2"}},
		{"disjoint", []string{"m1"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, Config{ModelsPinned: []string{"m2", "offline"}}, nil)
			h.setKey(func(k *keys.Key) { k.Limits.Models = tc.key })
			for _, route := range []string{"/v1/models", "/me"} {
				r := h.get(route)
				if r.status != 200 {
					t.Fatalf("%s: %d %s", route, r.status, r.body)
				}
				var body map[string]any
				if err := json.Unmarshal(r.body, &body); err != nil {
					t.Fatal(err)
				}
				var got []string
				if route == "/me" {
					for _, id := range body["host"].(map[string]any)["models"].([]any) {
						got = append(got, id.(string))
					}
				} else {
					for _, row := range body["data"].([]any) {
						got = append(got, row.(map[string]any)["id"].(string))
					}
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("%s: %v, want %v", route, got, tc.want)
				}
			}
			for _, route := range []string{"/v1/chat/completions", "/v1/embeddings"} {
				h.expectErr(h.post(route, chatBody("m1", 2, "")), CodeModelNotAllowed)
			}
			if len(tc.want) == 0 {
				h.expectErr(h.post("/v1/chat/completions", chatBody("", 2, "")), CodeModelNotAllowed)
				h.expectErr(h.post("/v1/chat/completions", chatBody("m2", 2, "")), CodeModelNotAllowed)
			} else {
				if got := h.post("/v1/chat/completions", chatBody("", 2, "")); got.status != 200 {
					t.Fatalf("default: %d %s", got.status, got.body)
				}
				if got := h.up.body(t)["model"]; got != "m2" {
					t.Fatalf("default escaped pin: %v", got)
				}
			}
		})
	}
	h := newHarness(t, Config{ModelsPinned: []string{"offline"}}, nil)
	if got := h.post("/v1/chat/completions", chatBody("", 2, "")); got.status != 200 {
		t.Fatalf("unreported pin: %d %s", got.status, got.body)
	}
	if got := h.up.body(t)["model"]; got != "offline" {
		t.Fatalf("unreported pin default: %v", got)
	}
}

// Tool-only work remains chargeable when the engine dies before reporting usage.
func TestToolArgumentsCountOnCut(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.idleTimeout = 100 * time.Millisecond
	h.up.set("sse",
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"id\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":99,"completion_tokens":99}}`,
	)
	h.up.mu.Lock()
	h.up.stallAfter = 3
	h.up.mu.Unlock()
	res, err := h.streamReq(context.Background(), chatBody("m1", 4, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.HasPrefix(string(got), h.up.written()) {
		t.Fatal("chat stream was rewritten")
	}
	ev := h.rec.last(t)
	if ev.Code != "upstream_error" || ev.PromptTokens != 4 || ev.CompletionTokens != 2 {
		t.Fatalf("cut before usage: %+v", ev)
	}
	if m := ev.Meters[0]; m.Charged != 6 {
		t.Fatalf("cut charge: %+v", m)
	}
}
