// Package upstream abstracts the OpenAI-compatible inference server the gateway proxies to. The
// engine is a state (DESIGN §3.2) that every consumer reads at the moment it decides (§3.3), behind
// a seam so small the engine's address cannot leak into the gateway (§3.4).
package upstream

import (
	"context"
	"net/http"
	"time"
)

type Kind string

const (
	Unknown  Kind = "unknown" // no engine has answered a signature probe yet
	LlamaCPP Kind = "llama.cpp"
	VLLM     Kind = "vllm"
	Ollama   Kind = "ollama"
	LMStudio Kind = "lmstudio"
	Generic  Kind = "openai-compatible" // answered /v1/models without any engine's signature
)

// Health is whether the engine answered its last probe, since when, and why not.
type Health struct {
	OK    bool      `json:"ok"`
	Since time.Time `json:"since"` // when OK last changed
	Err   string    `json:"err"`   // last probe error while !OK; "" while OK
}

// Info is the engine state as last probed. Unknown until an engine answered a signature probe;
// while Unknown there is one slot, no context and no models (E1). A failed refresh keeps every
// field and only flips Health (E2), so /me keeps telling the truth about what it knew.
type Info struct {
	URL          string    `json:"url"`
	Kind         Kind      `json:"kind"`
	Health       Health    `json:"health"`
	ModelContext int       `json:"model_context"` // last known; 0 while Unknown or unreported
	Slots        int       `json:"slots"`         // last known, override applied; 1 while Unknown
	Models       []string  `json:"models"`        // last known
	ProbedAt     time.Time `json:"probed_at"`
}

// Engine is what the gateway is allowed to know about the engine (§3.4): its state, its
// tokenizer, and one way to send it a request. The engine's address, transport, bearer, redirect
// policy and deadlines stay behind this seam — Protection 1 in structural form. All methods are
// safe for concurrent use.
type Engine interface {
	// Info returns the state as of the last probe.
	Info() Info
	// CountTokens returns an exact count where the engine offers /tokenize, else an estimate
	// (ceil(len/4)) with exact=false. Text is the concatenation of message contents.
	CountTokens(ctx context.Context, text string) (n int, exact bool, err error)
	// Do sends one request to the engine: bearer added, base URL private, redirects never
	// followed, a generation bounded by the first-byte deadline and a GET by the probe deadline
	// (DESIGN §1.6). A non-2xx comes back as a response, not an error. The caller owns resp.Body.
	Do(ctx context.Context, method, path string, body []byte, stream bool) (*http.Response, error)
}

// Upstream is the host's view: the Engine plus the probe the serve loop drives.
type Upstream interface {
	Engine
	// Refresh probes the engine once and moves the state (§3.2). The error is the probe's.
	Refresh(ctx context.Context) error
}
