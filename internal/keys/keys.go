// Package keys defines per-friend API keys and the Store the gateway reads them from.
// Types and the Store interface are the PM-owned seam (docs/ARCHITECTURE.md); FileStore is ticket 003.
package keys

import (
	"context"
	"time"
)

type Status string

const (
	Active  Status = "active"
	Paused  Status = "paused"
	Revoked Status = "revoked"
)

// Limits are per-key. Zero means "use the default" at creation time and "unlimited / upstream's"
// once stored (MaxContext 0 = upstream context; Models empty = all models).
type Limits struct {
	RPM             int      `json:"rpm"`
	TPM             int      `json:"tpm"`
	MaxConcurrent   int      `json:"max_concurrent"`
	MaxOutputTokens int      `json:"max_output_tokens"`
	MaxContext      int      `json:"max_context"`
	DailyTokens     int      `json:"daily_tokens"`
	Models          []string `json:"models,omitempty"`
}

// DefaultLimits are applied at `keys add` when a field is zero.
func DefaultLimits() Limits {
	return Limits{RPM: 20, TPM: 20000, MaxConcurrent: 1, MaxOutputTokens: 2048, MaxContext: 0, DailyTokens: 200000}
}

// Key is one friend. SecretHash is "sha256:<hex>" of the invite secret; the secret itself is never stored.
type Key struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	SecretHash string    `json:"secret_hash"`
	Status     Status    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	Limits     Limits    `json:"limits"`
}

// AllowsModel reports whether the key may use model id m.
func (k *Key) AllowsModel(m string) bool {
	if len(k.Limits.Models) == 0 {
		return true
	}
	for _, a := range k.Limits.Models {
		if a == m {
			return true
		}
	}
	return false
}

// Store is what the gateway needs. Implementations must be safe for concurrent use and must
// reflect external edits to keys.json without a restart (hot reload on mtime, checked at most once per second).
type Store interface {
	// Lookup resolves a presented secret. ok=false means no such key (401). A found key may still be
	// paused or revoked; the gateway decides the status code.
	Lookup(ctx context.Context, secret string) (k *Key, ok bool, err error)
	List(ctx context.Context) ([]*Key, error)
}

// Admin is the CLI-side write surface (ticket 003). Not used by the gateway.
type Admin interface {
	Store
	// Add creates a key and returns it with the plaintext secret (shown once).
	Add(ctx context.Context, name string, l Limits) (k *Key, secret string, err error)
	SetStatus(ctx context.Context, id string, s Status) error
	// Rotate replaces the secret, keeping id, limits, and history; returns the new plaintext secret.
	Rotate(ctx context.Context, id string) (secret string, err error)
	SetLimits(ctx context.Context, id string, l Limits) error
}

// HashSecret returns the canonical "sha256:<hex>" form used in keys.json.
// Implemented in hash.go (PM-owned) so 002 and 003 share one definition.
