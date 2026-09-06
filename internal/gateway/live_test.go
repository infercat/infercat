package gateway

// Opt-in check against a real llama.cpp server, e.g.
//   INFERCAT_LIVE_UPSTREAM=http://127.0.0.1:18080 go test -run Live -v ./internal/gateway/
// It sends requests only. The minimal Engine here is not ticket 003's adapter; it exists so the
// gateway can be exercised end-to-end with the seam in the test's hands.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/2185Lab/infercat/internal/keys"
	"github.com/2185Lab/infercat/internal/upstream"
)

type liveLlama struct {
	base *url.URL
	info upstream.Info
}

func (l *liveLlama) Info() upstream.Info { return l.info }

func (l *liveLlama) Do(ctx context.Context, method, path string, body []byte, stream bool) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, l.base.String()+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return noRedirectClient(0).Do(req)
}

func (l *liveLlama) Refresh(ctx context.Context) error {
	var props struct {
		TotalSlots int `json:"total_slots"`
		Defaults   struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if err := getJSON(ctx, l.base.String()+"/props", &props); err != nil {
		return err
	}
	var models struct {
		Data []struct{ ID string } `json:"data"`
	}
	if err := getJSON(ctx, l.base.String()+"/v1/models", &models); err != nil {
		return err
	}
	l.info = upstream.Info{Kind: upstream.LlamaCPP, URL: l.base.String(), Health: upstream.Health{OK: true}, ModelContext: props.Defaults.NCtx, Slots: props.TotalSlots}
	for _, m := range models.Data {
		l.info.Models = append(l.info.Models, m.ID)
	}
	return nil
}

func (l *liveLlama) CountTokens(ctx context.Context, text string, _ []byte) (int, bool, error) {
	body, _ := json.Marshal(map[string]string{"content": text})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.base.String()+"/tokenize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer res.Body.Close()
	var out struct{ Tokens []int }
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return 0, false, err
	}
	return len(out.Tokens), true, nil
}

func getJSON(ctx context.Context, u string, v any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(v)
}

func TestLiveLlamaCPP(t *testing.T) {
	target := os.Getenv("INFERCAT_LIVE_UPSTREAM")
	if target == "" {
		t.Skip("set INFERCAT_LIVE_UPSTREAM=http://127.0.0.1:18080 to run")
	}
	base, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	up := &liveLlama{base: base}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := up.Refresh(ctx); err != nil {
		t.Fatalf("upstream not reachable: %v", err)
	}
	t.Logf("upstream: %+v", up.info)

	h := newHarness(t, Config{HostName: "live-laptop", RelayRegion: func() string { return "loopback" }}, up)
	h.setKey(func(k *keys.Key) { k.Limits = keys.DefaultLimits() })

	r := h.get("/me")
	t.Logf("GET /me → %d %s", r.status, r.body)
	r = h.get("/v1/models")
	t.Logf("GET /v1/models → %d %s", r.status, r.body)

	// Streaming chat: time to first content delta, reasoning_content seen, tokens/s from the usage chunk.
	body := `{"messages":[{"role":"user","content":"In one short sentence, why is the sky blue?"}],"stream":true,"max_tokens":120}`
	start := time.Now()
	res, err := h.streamReq(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	br := bufio.NewReader(res.Body)
	var ttft time.Duration
	var content, reasoning strings.Builder
	chunks, sawUsage := 0, ""
	for {
		ev, err := readEvent(br)
		if strings.HasPrefix(ev, "data: ") && !strings.HasPrefix(ev, "data: [DONE]") {
			var ch struct {
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"delta"`
				} `json:"choices"`
				Usage *usageT
			}
			_ = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(ev, "data: "))), &ch)
			for _, c := range ch.Choices {
				if c.Delta.Content != "" || c.Delta.ReasoningContent != "" {
					if ttft == 0 {
						ttft = time.Since(start)
					}
					chunks++
					content.WriteString(c.Delta.Content)
					reasoning.WriteString(c.Delta.ReasoningContent)
				}
			}
			if ch.Usage != nil {
				sawUsage = fmt.Sprintf("%+v", *ch.Usage)
			}
		}
		if err != nil {
			break
		}
	}
	total := time.Since(start)
	t.Logf("stream: status %d, ttft %s, total %s, %d delta chunks, usage chunk %q", res.StatusCode, ttft, total, chunks, sawUsage)
	t.Logf("reasoning_content (%d bytes): %.120q", reasoning.Len(), reasoning.String())
	t.Logf("content: %q", content.String())
	ev := h.rec.waitFor(t, 3)[2]
	t.Logf("usage event: %+v", ev)
	if ev.CompletionTokens > 0 && total > ttft {
		t.Logf("tokens/s (completion tokens / (total - ttft)): %.1f", float64(ev.CompletionTokens)/(total-ttft).Seconds())
	}
	if res.StatusCode != 200 || chunks == 0 || sawUsage == "" || ev.PromptTokens == 0 {
		t.Fatalf("live stream failed")
	}

	// Non-stream.
	r = h.post("/v1/chat/completions", `{"messages":[{"role":"user","content":"Say hi."}],"max_tokens":20}`)
	t.Logf("non-stream: %d %.200s", r.status, r.body)
	ev = h.rec.waitFor(t, 4)[3]
	t.Logf("usage event: %+v", ev)
	if r.status != 200 || ev.CompletionTokens == 0 {
		t.Fatalf("live non-stream failed")
	}

	// 006 promise 1 against the real engine: llama.cpp lets n_predict win over max_tokens (proven by the
	// review); through the gateway the alias is stripped and the key's cap holds.
	h.setKey(func(k *keys.Key) { k.Limits = keys.DefaultLimits(); k.Limits.MaxOutputTokens = 8 })
	r = h.post("/v1/chat/completions", `{"messages":[{"role":"user","content":"Count from one to one hundred, slowly."}],"max_tokens":8,"n_predict":40,"ignore_eos":true}`)
	ev = h.rec.waitFor(t, 5)[4]
	t.Logf("max_tokens 8 + n_predict 40 + ignore_eos through the gateway: %d, completion_tokens %d", r.status, ev.CompletionTokens)
	if r.status != 200 || ev.CompletionTokens > 8 {
		t.Fatalf("alias bypass: completion_tokens %d", ev.CompletionTokens)
	}

	// Limits against real counts: context too long, then rpm.
	h.setKey(func(k *keys.Key) { k.Limits.MaxContext = 64; k.Limits.MaxOutputTokens = 32 })
	long := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 20)
	r = h.post("/v1/chat/completions", `{"messages":[{"role":"user","content":"`+long+`"}]}`)
	t.Logf("long prompt with max_context 64: %d %s", r.status, r.body)
	h.expectErr(r, CodeContextTooLong)
	h.setKey(func(k *keys.Key) { k.Limits = keys.DefaultLimits(); k.Limits.RPM = 1 })
	h.post("/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":5}`)
	r = h.post("/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":5}`)
	t.Logf("second request with rpm 1: %d Retry-After=%s %s", r.status, r.header.Get("Retry-After"), r.body)
	h.expectErr(r, CodeRateLimited)
	_ = io.Discard
}
