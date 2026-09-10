package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakes of the four engines, shaped from what the real servers answered on 2026-09-02
// (llama.cpp b9553 at 127.0.0.1:18080 and vLLM 0.25.0 on a workstation, port 8010).

func llamaCPP(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/props", jsonOK(`{"default_generation_settings":{"n_ctx":4096},"total_slots":2,
		"model_alias":"gemma-4-E2B-it-Q4_K_M.gguf","model_path":"/models/gemma.gguf"}`))
	mux.HandleFunc("/v1/models", jsonOK(`{"object":"list","data":[
		{"id":"gemma-4-E2B-it-Q4_K_M.gguf","object":"model","owned_by":"llamacpp"}]}`))
	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content    string `json:"content"`
			AddSpecial bool   `json:"add_special"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Content == "" {
			// llama.cpp's shape is {"content": ...}; anything else must not produce a count.
			http.Error(w, "bad tokenize body", http.StatusBadRequest)
			return
		}
		toks := make([]int, len(strings.Fields(body.Content)))
		if body.AddSpecial {
			toks = append(toks, 2) // BOS
		}
		b, _ := json.Marshal(map[string]any{"tokens": toks})
		w.Write(b)
	})
	// The chat template as b9553 renders it (Gemma's shape): a turn marker, the role, the content,
	// a closing marker per message — three whitespace-separated fields of template — and the
	// generation prompt, two more.
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) == 0 {
			http.Error(w, "bad apply-template body", http.StatusBadRequest)
			return
		}
		var sb strings.Builder
		for _, m := range body.Messages {
			fmt.Fprintf(&sb, "<|turn> %s %s <turn|>\n", m.Role, m.Content)
		}
		sb.WriteString("<|turn> model\n")
		b, _ := json.Marshal(map[string]string{"prompt": sb.String()})
		w.Write(b)
	})
	return serve(t, mux)
}

func vllm(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/version", jsonOK(`{"version":"0.25.0"}`))
	mux.HandleFunc("/v1/models", jsonOK(`{"object":"list","data":[
		{"id":"entropy-v2-gemma4-12b-w4a16-group128","object":"model","owned_by":"vllm","max_model_len":8192}]}`))
	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string                           `json:"model"`
			Prompt   string                           `json:"prompt"`
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model == "" || (body.Prompt == "" && len(body.Messages) == 0) {
			// vLLM's shape is {"model": ..., "prompt": ...} or {"model": ..., "messages": [...]}.
			http.Error(w, "bad tokenize body", http.StatusBadRequest)
			return
		}
		n := len(strings.Fields(body.Prompt))
		for _, m := range body.Messages { // the template: three tokens a message, three for BOS and the generation prompt
			n += len(strings.Fields(m.Content)) + 3
		}
		if len(body.Messages) > 0 {
			n += 3
		}
		b, _ := json.Marshal(map[string]any{"count": n, "max_model_len": 8192})
		w.Write(b)
	})
	return serve(t, mux)
}

func ollama(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", jsonOK(`{"models":[{"name":"llama3.2:3b","model":"llama3.2:3b"}]}`))
	mux.HandleFunc("/v1/models", jsonOK(`{"object":"list","data":[{"id":"llama3.2:3b","owned_by":"library"}]}`))
	return serve(t, mux)
}

func lmStudio(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", jsonOK(`{"object":"list","data":[
		{"id":"qwen3-8b","object":"model","owned_by":"organization_owner","publisher":"qwen",
		 "quantization":"Q4_K_M","loaded_context_length":32768}]}`))
	return serve(t, mux)
}

func generic(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", jsonOK(`{"object":"list","data":[{"id":"some-model","owned_by":"someone"}]}`))
	return serve(t, mux)
}

func serve(t *testing.T, h http.Handler) *httptest.Server {
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func jsonOK(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}
}

func TestOpenSniffsEveryKind(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		srv   *httptest.Server
		want  Kind
		info  Info
		exact bool
	}{
		{"llama.cpp", llamaCPP(t), LlamaCPP, Info{ModelContext: 4096, Slots: 2, Models: []string{"gemma-4-E2B-it-Q4_K_M.gguf"}}, true},
		{"vllm", vllm(t), VLLM, Info{ModelContext: 8192, Slots: 2, Models: []string{"entropy-v2-gemma4-12b-w4a16-group128"}}, true},
		{"ollama", ollama(t), Ollama, Info{ModelContext: 0, Slots: 1, Models: []string{"llama3.2:3b"}}, false},
		{"lmstudio", lmStudio(t), LMStudio, Info{ModelContext: 32768, Slots: 1, Models: []string{"qwen3-8b"}}, false},
		{"generic", generic(t), Generic, Info{ModelContext: 0, Slots: 1, Models: []string{"some-model"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up, err := Open(ctx, tc.srv.URL, "")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			got := up.Info()
			if got.Kind != tc.want {
				t.Errorf("kind = %q, want %q", got.Kind, tc.want)
			}
			if !got.Health.OK || got.Health.Err != "" || got.Health.Since.IsZero() || got.ProbedAt.IsZero() {
				t.Errorf("health after a successful Refresh = %+v", got.Health)
			}
			if got.ModelContext != tc.info.ModelContext {
				t.Errorf("context = %d, want %d", got.ModelContext, tc.info.ModelContext)
			}
			if got.Slots != tc.info.Slots {
				t.Errorf("slots = %d, want %d", got.Slots, tc.info.Slots)
			}
			if len(got.Models) != 1 || got.Models[0] != tc.info.Models[0] {
				t.Errorf("models = %v, want %v", got.Models, tc.info.Models)
			}

			n, exact, err := up.CountTokens(ctx, up.Info().Models[0], "one two three four five", nil)
			if err != nil {
				t.Fatalf("CountTokens: %v", err)
			}
			if exact != tc.exact {
				t.Errorf("exact = %v, want %v", exact, tc.exact)
			}
			if tc.exact && n != 5 {
				t.Errorf("exact count = %d, want 5 (the engine's own tokenizer)", n)
			}
			if !tc.exact && n != EstimateTokens("one two three four five") {
				t.Errorf("estimate = %d, want ceil(chars/4)", n)
			}
		})
	}
}

// Detection walks the contract's fixed order and takes the first engine that answers.
func TestDetectOrder(t *testing.T) {
	ctx := context.Background()
	llama, vl := llamaCPP(t), vllm(t)
	dead := "http://127.0.0.1:1" // nothing listens on port 1

	restore := candidates
	t.Cleanup(func() { candidates = restore })

	candidates = []struct {
		url  string
		kind Kind
	}{{dead, LlamaCPP}, {llama.URL, LlamaCPP}, {vl.URL, VLLM}}
	up, err := Detect(ctx, "")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if up.Info().Kind != LlamaCPP {
		t.Errorf("kind = %q, want the earlier candidate llama.cpp", up.Info().Kind)
	}

	// With llama.cpp absent the next candidate wins.
	candidates = []struct {
		url  string
		kind Kind
	}{{dead, LlamaCPP}, {vl.URL, VLLM}}
	up, err = Detect(ctx, "")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if up.Info().Kind != VLLM {
		t.Errorf("kind = %q, want vllm", up.Info().Kind)
	}

	candidates = []struct {
		url  string
		kind Kind
	}{{dead, LlamaCPP}}
	if _, err := Detect(ctx, ""); err != ErrNoUpstream {
		t.Errorf("Detect with nothing listening = %v, want ErrNoUpstream", err)
	}
}

// 005 fix 10n: auto-detection sends --upstream-key on its probes, so a vLLM behind a bearer token
// (401 to an unauthenticated probe) is found instead of being reported as no engine at all.
func TestDetectPassesTheAPIKeyToProbes(t *testing.T) {
	const key = "sk-guarded"
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+key {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/version", guard(jsonOK(`{"version":"0.25.0"}`)))
	mux.HandleFunc("/v1/models", guard(jsonOK(`{"object":"list","data":[{"id":"m","owned_by":"vllm","max_model_len":8192}]}`)))
	srv := serve(t, mux)

	restore := candidates
	t.Cleanup(func() { candidates = restore })
	candidates = []struct {
		url  string
		kind Kind
	}{{srv.URL, VLLM}}

	// Without the key the guarded engine looks absent.
	if _, err := Detect(context.Background(), ""); err != ErrNoUpstream {
		t.Fatalf("Detect without the key = %v; want ErrNoUpstream (401 on every probe)", err)
	}
	// With the key it is found, healthy, and correctly typed.
	up, err := Detect(context.Background(), key)
	if err != nil {
		t.Fatalf("Detect with the key: %v", err)
	}
	if i := up.Info(); i.Kind != VLLM || !i.Health.OK || i.ModelContext != 8192 {
		t.Fatalf("detected %+v; want vllm, healthy, context 8192", i)
	}
}

// The default candidate list is part of the contract, not an implementation detail.
func TestDefaultDetectionOrderMatchesTheContract(t *testing.T) {
	want := []struct {
		url  string
		kind Kind
	}{
		{"http://127.0.0.1:8080", LlamaCPP},
		{"http://127.0.0.1:11434", Ollama},
		{"http://127.0.0.1:1234", LMStudio},
		{"http://127.0.0.1:8000", VLLM},
	}
	if len(candidates) != len(want) {
		t.Fatalf("candidates = %v", candidates)
	}
	for i := range want {
		if candidates[i] != want[i] {
			t.Errorf("candidate %d = %v, want %v", i, candidates[i], want[i])
		}
	}
}

// E3 (DESIGN §3.5): an engine that is down at Open is Unknown, not a guess; the first probe that
// finds it answering identifies it with its real kind, slots and context — and the gateway's queue
// cap follows on its next acquire with no call from serve (gateway: TestQueueFollowsEngineSlots;
// cmd: TestServeRoutesTunnelLogAndFollowsSlots).
func TestE3EngineDownAtOpenIsIdentifiedByTheNextProbe(t *testing.T) {
	var up atomic.Bool
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/props":
			io.WriteString(w, `{"total_slots":4,"default_generation_settings":{"n_ctx":8192}}`)
		case "/v1/models":
			io.WriteString(w, `{"data":[{"id":"m"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	u, err := Open(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	before := u.Info()
	if before.Health.OK || before.Kind != Unknown || before.Health.Err == "" {
		t.Fatalf("down engine at Open = %+v; want Unknown, not OK, with the probe's error", before)
	}
	if err := u.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh against a down engine must fail")
	}
	if i := u.Info(); i.Kind != Unknown || !i.Health.Since.Equal(before.Health.Since) {
		t.Fatalf("a second failed probe must keep Unknown and Since: %+v", i)
	}
	up.Store(true)
	if err := u.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if i := u.Info(); !i.Health.OK || i.Health.Err != "" || i.Kind != LlamaCPP || i.Slots != 4 || i.ModelContext != 8192 || len(i.Models) != 1 || !i.Health.Since.After(before.Health.Since) {
		t.Fatalf("after the engine came up = %+v; want llama.cpp, OK since now, 4 slots, context 8192, 1 model", i)
	}
}

// E1 (DESIGN §3.5): Unknown ⇒ one slot, no context, no models — even with a --slots override,
// which shows once an engine is identified.
func TestE1UnknownEngineHasNothingButOneSlot(t *testing.T) {
	up, err := Open(context.Background(), "http://127.0.0.1:1", "")
	if err != nil {
		t.Fatalf("Open should not fail on an engine that is merely down: %v", err)
	}
	up.(Slotted).SetSlots(7)
	if i := up.Info(); i.Kind != Unknown || i.Health.OK || i.Slots != 1 || i.ModelContext != 0 || len(i.Models) != 0 {
		t.Fatalf("Unknown engine = %+v; want one slot, no context, no models", i)
	}
	// It still counts tokens, by estimate, so the gateway's pre-check works while it waits.
	if n, exact, err := up.CountTokens(context.Background(), "test-model", "abcd", nil); err != nil || exact || n != 1 {
		t.Errorf("CountTokens = %d %v %v", n, exact, err)
	}
}

// E2 (DESIGN §3.5): when a healthy engine goes away, Refresh reports the failure and flips Health
// (OK false, Since moved, Err set) but Kind, models, slots and context stay, so /me keeps telling
// the truth about what it saw.
func TestE2RefreshKeepsTheLastGoodInfoWhenTheEngineGoesAway(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/props", jsonOK(`{"total_slots":2,"default_generation_settings":{"n_ctx":4096}}`))
	mux.HandleFunc("/v1/models", jsonOK(`{"data":[{"id":"gemma"}]}`))
	srv := httptest.NewServer(mux)

	up, err := Open(ctx, srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	before := up.Info()
	if !before.Health.OK || before.ModelContext != 4096 {
		t.Fatalf("info before = %+v", before)
	}
	srv.Close()

	if err := up.Refresh(ctx); err == nil {
		t.Error("Refresh against a dead engine returned no error")
	}
	got := up.Info()
	if got.Health.OK || got.Health.Err == "" || !got.Health.Since.After(before.Health.Since) {
		t.Errorf("health after the engine went away = %+v; want not OK, since now, with the error", got.Health)
	}
	if got.Kind != LlamaCPP || got.Slots != 2 || got.ModelContext != 4096 || len(got.Models) != 1 || got.Models[0] != "gemma" {
		t.Errorf("info = %+v, want the last known kind, slots, models and context kept", got)
	}
}

func TestSlotsOverride(t *testing.T) {
	ctx := context.Background()
	up, err := Open(ctx, llamaCPP(t).URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if up.Info().Slots != 2 {
		t.Fatalf("slots = %d, want the engine's 2", up.Info().Slots)
	}
	up.(Slotted).SetSlots(7)
	if up.Info().Slots != 7 {
		t.Errorf("slots = %d, want the host override 7", up.Info().Slots)
	}
	if err := up.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if up.Info().Slots != 7 {
		t.Errorf("slots = %d after Refresh, want the override to survive", up.Info().Slots)
	}
}

func TestDoAddsTheUpstreamKey(t *testing.T) {
	var seen string
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		jsonOK(`{"object":"list","data":[]}`)(w, r)
	}))
	up, err := Open(context.Background(), srv.URL, "sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := up.Do(context.Background(), http.MethodGet, "/v1/models", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if seen != "Bearer sk-secret" {
		t.Errorf("Authorization = %q, want the upstream key", seen)
	}
}

// E5 (DESIGN §3.5): Do never follows a redirect (the bearer would otherwise be replayed to
// wherever Location points) and carries nothing of the friend's — the request is built from
// method, path and body alone. A GET is a list, bounded by the probe deadline until its body is closed.
func TestE5DoNeverFollowsRedirectsNorLeaksHeaders(t *testing.T) {
	var landed atomic.Int32
	var mu sync.Mutex
	var auths []string
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		switch r.URL.Path {
		case "/landed":
			landed.Add(1)
			io.WriteString(w, `{}`)
		case "/v1/chat/completions":
			http.Redirect(w, r, "/landed", http.StatusFound)
		default:
			jsonOK(`{"object":"list","data":[{"id":"m"}]}`)(w, r)
		}
	}))
	up, err := Open(context.Background(), srv.URL, "sk-host")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := up.Do(context.Background(), http.MethodPost, "/v1/chat/completions", []byte(`{"x":1}`), true)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || landed.Load() != 0 {
		t.Fatalf("Do returned %d and the redirect target was hit %d time(s); want the 302 itself and 0", resp.StatusCode, landed.Load())
	}
	mu.Lock()
	seen := append([]string(nil), auths...)
	mu.Unlock()
	for _, a := range seen {
		if a != "Bearer sk-host" {
			t.Fatalf("the engine saw Authorization %q; only the host's key may reach it", a)
		}
	}
	resp, err = up.Do(context.Background(), http.MethodGet, "/v1/models", nil, false)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET through Do: %v %v", err, resp)
	}
	if _, ok := resp.Body.(cancelOnClose); !ok {
		t.Fatal("a GET's body must end the probe deadline on Close")
	}
	resp.Body.Close()
}

func TestNormalizeURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://127.0.0.1:8000", "http://127.0.0.1:8000"},
		{"http://127.0.0.1:8000/", "http://127.0.0.1:8000"},
		{"http://127.0.0.1:8000/v1", "http://127.0.0.1:8000"},
		{"http://127.0.0.1:8000/v1/", "http://127.0.0.1:8000"},
		{"127.0.0.1:8000", "http://127.0.0.1:8000"},
		{"https://host.example/v1?x=1", "https://host.example"},
	} {
		u, err := normalize(tc.in)
		if err != nil {
			t.Errorf("normalize(%q): %v", tc.in, err)
			continue
		}
		if u.String() != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.in, u, tc.want)
		}
	}
	for _, bad := range []string{"", "   ", "http://"} {
		if _, err := normalize(bad); err == nil {
			t.Errorf("normalize(%q) accepted", bad)
		}
	}
}

// A tokenizer that rejects the request must degrade to the estimate, never to an error: the
// gateway's context pre-check must not turn a tokenizer hiccup into a 5xx.
func TestTokenizeFailureFallsBackToTheEstimate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/props", jsonOK(`{"total_slots":1,"default_generation_settings":{"n_ctx":2048}}`))
	mux.HandleFunc("/v1/models", jsonOK(`{"data":[{"id":"m"}]}`))
	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	up, err := Open(context.Background(), serve(t, mux).URL, "")
	if err != nil {
		t.Fatal(err)
	}
	n, exact, err := up.CountTokens(context.Background(), "test-model", "12345678", nil)
	if err != nil || exact || n != 2 {
		t.Errorf("CountTokens = %d exact=%v err=%v, want 2 by estimate", n, exact, err)
	}
	// A chat degrades to the estimate plus the template allowance (036), never an error.
	msgs := []byte(`[{"role":"user","content":"12345678"}]`)
	if n, exact, err := up.CountTokens(context.Background(), "test-model", "12345678\n", msgs); err != nil || exact || n != 3+TemplateAllowance(msgs) {
		t.Errorf("CountTokens(chat) = %d exact=%v err=%v, want %d by estimate + allowance", n, exact, err, 3+TemplateAllowance(msgs))
	}
}

// 036: a chat is counted under the engine's chat template — what its chat endpoint enforces —
// which is more than its bare text: exactly, by the template, on llama.cpp (/apply-template, then
// /tokenize with the special tokens and BOS) and on vLLM (/tokenize with the messages); by the
// allowance on an engine that estimates. Without messages (embeddings) the count is the text's.
func TestCountTokensUnderTheChatTemplate(t *testing.T) {
	ctx := context.Background()
	msgs := []byte(`[{"role":"system","content":"one two"},{"role":"user","content":"three four five"}]`)
	text := "one two\nthree four five\n"
	if TemplateAllowance(nil) != 0 || TemplateAllowance([]byte(`{"not":"an array"}`)) != 0 || TemplateAllowance(msgs) != 24 {
		t.Fatalf("TemplateAllowance: nil %d, object %d, two messages %d", TemplateAllowance(nil), TemplateAllowance([]byte(`{"not":"an array"}`)), TemplateAllowance(msgs))
	}
	for _, tc := range []struct {
		name  string
		srv   *httptest.Server
		exact bool
	}{
		{"llama.cpp", llamaCPP(t), true},
		{"vllm", vllm(t), true},
		{"ollama", ollama(t), false},
		{"lmstudio", lmStudio(t), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up, err := Open(ctx, tc.srv.URL, "")
			if err != nil {
				t.Fatal(err)
			}
			raw, exact, err := up.CountTokens(ctx, up.Info().Models[0], text, nil)
			if err != nil || exact != tc.exact {
				t.Fatalf("text count: %d exact=%v err=%v", raw, exact, err)
			}
			chat, exact, err := up.CountTokens(ctx, up.Info().Models[0], text, msgs)
			if err != nil || exact != tc.exact {
				t.Fatalf("chat count: %d exact=%v err=%v", chat, exact, err)
			}
			want := raw + 3*2 + 3 // the fakes' template: three tokens a message, BOS and the generation prompt
			if !tc.exact {
				want = raw + TemplateAllowance(msgs)
			}
			if chat != want || chat <= raw {
				t.Fatalf("chat count = %d, want %d (text %d)", chat, want, raw)
			}
			t.Logf("%s: text %d tokens, under the chat template %d (exact %v)", tc.name, raw, chat, exact)
		})
	}
}

func TestEstimateTokens(t *testing.T) {
	for in, want := range map[string]int{"": 0, "a": 1, "abcd": 1, "abcde": 2, "12345678": 2} {
		if got := EstimateTokens(in); got != want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", in, got, want)
		}
	}
}

// The vLLM slot default (docs/ARCHITECTURE.md: 2 unless --slots) is applied at identification,
// not guessed while Unknown.
func TestVLLMSlotsDefaultToTwoOnceIdentified(t *testing.T) {
	up, err := Open(context.Background(), vllm(t).URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if i := up.Info(); i.Kind != VLLM || i.Slots != 2 {
		t.Fatalf("vllm = %+v; want 2 slots", i)
	}
	up.(Slotted).SetSlots(5)
	if err := up.Refresh(context.Background()); err != nil || up.Info().Slots != 5 {
		t.Fatalf("override 5 must survive a refresh: %v %d", err, up.Info().Slots)
	}
}

func TestLlamaSwapModelMetadataAndSlots(t *testing.T) {
	// v255's exact response fields; the escaped id also pins the /props query boundary.
	const first = "model /a&b"
	var stage, firstProbes, secondProbes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := stage.Load()
		switch r.URL.Path {
		case "/v1/models":
			if n == 5 {
				http.Error(w, "unavailable", 503)
				return
			}
			a, b := "unloaded", "unloaded"
			if n == 1 || n == 4 {
				a = "loaded"
			}
			if n == 2 {
				b = "loaded"
			}
			json.NewEncoder(w).Encode(map[string]any{"data": []any{
				map[string]any{"id": first, "owned_by": "llama-swap", "context_length": 4096, "status": map[string]string{"value": a}},
				map[string]any{"id": "second", "owned_by": "llama-swap", "context_length": 8192, "status": map[string]string{"value": b}},
			}})
		case "/props":
			switch r.URL.Query().Get("model") {
			case first:
				firstProbes.Add(1)
				if n != 1 && n != 4 {
					t.Error("probed unloaded first model")
				}
				if n == 4 {
					http.Error(w, "loading changed", 503)
					return
				}
				io.WriteString(w, `{"total_slots":4,"modalities":{"vision":false}}`)
			case "second":
				secondProbes.Add(1)
				if n != 2 {
					t.Error("probed unloaded second model")
				}
				io.WriteString(w, `{"total_slots":2}`)
			default:
				t.Errorf("unqualified or incorrectly escaped props query: %s", r.URL)
				http.Error(w, "missing model", 400)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	up, err := Open(context.Background(), server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if info := up.Info(); info.Kind != LlamaSwap || !info.Health.OK || info.ModelContext != 4096 || info.Slots != 1 || info.ModelDetails[first] != (ModelInfo{Context: 4096, State: "unloaded", Slots: 1}) {
		t.Fatalf("initial metadata: %+v", info)
	}
	if firstProbes.Load() != 0 || secondProbes.Load() != 0 {
		t.Fatal("discovery probed unloaded models")
	}
	for _, tc := range []struct{ stage, global, first, second int }{{1, 1, 4, 1}, {2, 2, 4, 2}, {3, 2, 4, 2}, {4, 2, 4, 2}} {
		stage.Store(int32(tc.stage))
		if err := up.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		info := up.Info()
		if info.Slots != tc.global || info.ModelDetails[first].Slots != tc.first || info.ModelDetails["second"].Slots != tc.second || info.ModelDetails["second"].Context != 8192 {
			t.Fatalf("stage %d: %+v", tc.stage, info)
		}
	}
	if firstProbes.Load() != 2 || secondProbes.Load() != 1 {
		t.Fatalf("unexpected probes: %d/%d", firstProbes.Load(), secondProbes.Load())
	}
	copy := up.Info()
	copy.ModelDetails[first] = ModelInfo{}
	if up.Info().ModelDetails[first].Slots != 4 {
		t.Fatal("Info exposes mutable metadata")
	}
	up.(Slotted).SetSlots(7)
	stage.Store(3)
	if err := up.Refresh(context.Background()); err != nil || up.Info().Slots != 7 || up.Info().ModelDetails[first].Slots != 4 {
		t.Fatalf("slot override: %+v %v", up.Info(), err)
	}
	stage.Store(5)
	if err := up.Refresh(context.Background()); err == nil || up.Info().Health.OK || up.Info().ModelDetails[first].State != "unloaded" || up.Info().Slots != 7 {
		t.Fatalf("failed refresh did not retain metadata: %+v %v", up.Info(), err)
	}
}

func TestSwapFieldsWithoutSignatureStayGeneric(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", jsonOK(`{"data":[{"id":"m","owned_by":"other","context_length":8192,"status":{"value":"loaded"}}]}`))
	server := httptest.NewServer(mux)
	defer server.Close()
	up, err := Open(context.Background(), server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if info := up.Info(); info.Kind != Generic || info.ModelContext != 0 || info.Slots != 1 || info.ModelDetails != nil {
		t.Fatalf("non-signature changed generic behavior: %+v", info)
	}
}
