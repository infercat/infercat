// Package usage defines the per-request usage event and the Recorder the gateway writes to.
// Types are the PM-owned seam (docs/ARCHITECTURE.md); the JSONL recorder and aggregates are ticket 003.
package usage

import (
	"context"
	"time"
)

// Event is one request. No prompt or completion content lives here unless the host opted in,
// in which case Prompt/Completion are set (Protection 3 in docs/PRINCIPLES.md).
type Event struct {
	TS               time.Time `json:"ts"`
	KeyID            string    `json:"key_id"`
	Endpoint         string    `json:"endpoint"` // "/v1/chat/completions", "/v1/embeddings", "/v1/models", "/me"
	Model            string    `json:"model,omitempty"`
	Status           int       `json:"status"`         // HTTP status returned to the friend
	Code             string    `json:"code,omitempty"` // gateway error code when Status >= 400
	Stream           bool      `json:"stream"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	QueuedMS         int64     `json:"queued_ms"`
	TTFTMS           int64     `json:"ttft_ms"` // time to first byte of the response body; 0 if none
	TotalMS          int64     `json:"total_ms"`
	Prompt           string    `json:"prompt,omitempty"`     // only with --log-prompts
	Completion       string    `json:"completion,omitempty"` // only with --log-prompts
}

// Recorder receives every completed request. Record must not block the response path for long;
// implementations buffer and append.
type Recorder interface {
	Record(ctx context.Context, e Event)
}

// Counters are the live per-key numbers the gateway keeps in memory for limits and for /me.
// Implemented by the gateway (002); exposed to admin status via the Snapshot interface.
type KeyCounters struct {
	InFlight    int       `json:"in_flight"`
	RPMUsed     int       `json:"rpm_used"`
	TPMUsed     int       `json:"tpm_used"`
	TodayTokens int       `json:"today_tokens"`
	LastSeen    time.Time `json:"last_seen"`
}

// Snapshot is what the admin API reads from the running gateway.
type Snapshot interface {
	Counters(keyID string) KeyCounters
	AllCounters() map[string]KeyCounters
	Queue() (inFlight, waiting int)
}
