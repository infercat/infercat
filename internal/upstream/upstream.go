// Package upstream abstracts the OpenAI-compatible inference server the gateway proxies to.
// Interface is the PM-owned seam (docs/ARCHITECTURE.md); detection and implementations are ticket 003.
package upstream

import (
	"context"
	"net/http"
	"net/url"
)

type Kind string

const (
	LlamaCPP Kind = "llama.cpp"
	VLLM     Kind = "vllm"
	Ollama   Kind = "ollama"
	LMStudio Kind = "lmstudio"
	Generic  Kind = "openai-compatible"
)

// Info is what /me and the admin status report about the upstream.
type Info struct {
	Kind         Kind     `json:"kind"`
	URL          string   `json:"url"`
	Healthy      bool     `json:"healthy"`
	ModelContext int      `json:"model_context"` // tokens; 0 if unknown
	Slots        int      `json:"slots"`         // parallel requests the engine can serve
	Models       []string `json:"models"`
}

// Upstream is the gateway's view of the engine. All methods are safe for concurrent use.
type Upstream interface {
	// BaseURL is where /v1/* lives (e.g. http://127.0.0.1:8080). Never loopback-restricted here;
	// the gateway is the only thing that talks to it.
	BaseURL() *url.URL
	// Transport returns the http.RoundTripper to use (adds the upstream API key if configured).
	Transport() http.RoundTripper
	// Info returns the last known state; Refresh probes the engine and updates it.
	Info() Info
	Refresh(ctx context.Context) error
	// CountTokens returns an exact count where the engine offers /tokenize, else an estimate
	// (ceil(len/4)) with exact=false. Text is the concatenation of message contents.
	CountTokens(ctx context.Context, text string) (n int, exact bool, err error)
}
