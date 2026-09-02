package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakes of the four engines, shaped from what the real servers answered on 2026-09-02
// (llama.cpp b9553 at 127.0.0.1:18080 and vLLM 0.25.0 at max-ws.lab:8010).

func llamaCPP(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/props", jsonOK(`{"default_generation_settings":{"n_ctx":4096},"total_slots":2,
		"model_alias":"gemma-4-E2B-it-Q4_K_M.gguf","model_path":"/models/gemma.gguf"}`))
	mux.HandleFunc("/v1/models", jsonOK(`{"object":"list","data":[
		{"id":"gemma-4-E2B-it-Q4_K_M.gguf","object":"model","owned_by":"llamacpp"}]}`))
	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Content == "" {
			// llama.cpp's shape is {"content": ...}; anything else must not produce a count.
			http.Error(w, "bad tokenize body", http.StatusBadRequest)
			return
		}
		toks := make([]int, len(strings.Fields(body.Content)))
		b, _ := json.Marshal(map[string]any{"tokens": toks})
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
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model == "" || body.Prompt == "" {
			// vLLM's shape is {"model": ..., "prompt": ...}.
			http.Error(w, "bad tokenize body", http.StatusBadRequest)
			return
		}
		b, _ := json.Marshal(map[string]any{"count": len(strings.Fields(body.Prompt)), "max_model_len": 8192})
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
			if !got.Healthy {
				t.Error("healthy = false after a successful Refresh")
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

			n, exact, err := up.CountTokens(ctx, "one two three four five")
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
	if i := up.Info(); i.Kind != VLLM || !i.Healthy || i.ModelContext != 8192 {
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

// 005 fix 10g: an engine that is down when serve starts is a guess (Generic, unhealthy); the
// first Refresh that finds it answering re-sniffs and adopts the real kind, slots, and context.
func TestRefreshReSniffsAnEngineThatWasDownAtOpen(t *testing.T) {
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
	if i := u.Info(); i.Healthy || i.Kind != Generic {
		t.Fatalf("down engine at Open = %+v; want Generic and unhealthy", i)
	}
	if err := u.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh against a down engine must fail")
	}
	up.Store(true)
	if err := u.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if i := u.Info(); !i.Healthy || i.Kind != LlamaCPP || i.Slots != 4 || i.ModelContext != 8192 || len(i.Models) != 1 {
		t.Fatalf("after the engine came up = %+v; want llama.cpp, 4 slots, context 8192, 1 model", i)
	}
}

func TestUnreachableUpstreamOpensUnhealthy(t *testing.T) {
	up, err := Open(context.Background(), "http://127.0.0.1:1", "")
	if err != nil {
		t.Fatalf("Open should not fail on an engine that is merely down: %v", err)
	}
	if up.Info().Healthy {
		t.Error("healthy = true for an upstream that never answered")
	}
	// It still counts tokens, by estimate, so the gateway's pre-check works while it waits.
	if n, exact, err := up.CountTokens(context.Background(), "abcd"); err != nil || exact || n != 1 {
		t.Errorf("CountTokens = %d %v %v", n, exact, err)
	}
}

// When a healthy engine goes away, Refresh reports the failure and flips Healthy, but the models
// and context it last knew stay in Info so /me keeps telling the truth about what it saw.
func TestRefreshKeepsTheLastGoodInfoWhenTheEngineGoesAway(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/props", jsonOK(`{"total_slots":2,"default_generation_settings":{"n_ctx":4096}}`))
	mux.HandleFunc("/v1/models", jsonOK(`{"data":[{"id":"gemma"}]}`))
	srv := httptest.NewServer(mux)

	up, err := Open(ctx, srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if !up.Info().Healthy || up.Info().ModelContext != 4096 {
		t.Fatalf("info before = %+v", up.Info())
	}
	srv.Close()

	if err := up.Refresh(ctx); err == nil {
		t.Error("Refresh against a dead engine returned no error")
	}
	got := up.Info()
	if got.Healthy {
		t.Error("healthy = true after the engine went away")
	}
	if got.ModelContext != 4096 || len(got.Models) != 1 || got.Models[0] != "gemma" {
		t.Errorf("info = %+v, want the last known models and context kept", got)
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

func TestTransportAddsTheUpstreamKey(t *testing.T) {
	var seen string
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		jsonOK(`{"object":"list","data":[]}`)(w, r)
	}))
	up, err := Open(context.Background(), srv.URL, "sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/v1/models", nil)
	resp, err := up.Transport().RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if seen != "Bearer sk-secret" {
		t.Errorf("Authorization = %q, want the upstream key", seen)
	}
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
	n, exact, err := up.CountTokens(context.Background(), "12345678")
	if err != nil || exact || n != 2 {
		t.Errorf("CountTokens = %d exact=%v err=%v, want 2 by estimate", n, exact, err)
	}
}

func TestEstimateTokens(t *testing.T) {
	for in, want := range map[string]int{"": 0, "a": 1, "abcd": 1, "abcde": 2, "12345678": 2} {
		if got := EstimateTokens(in); got != want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestBaseURLIsACopy(t *testing.T) {
	up, err := Open(context.Background(), "http://127.0.0.1:1", "")
	if err != nil {
		t.Fatal(err)
	}
	u := up.BaseURL()
	u.Path = "/mutated"
	if up.BaseURL().Path == "/mutated" {
		t.Error("BaseURL handed out the client's own URL")
	}
}
