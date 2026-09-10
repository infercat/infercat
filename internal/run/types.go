// Package run owns durable, per-key work independently of its submitting HTTP request.
package run

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/infercat/infercat/internal/usage"
	"time"
)

const (
	MaxInput    = 1 << 20
	MaxOutput   = 1 << 20
	MaxStored   = 64 << 20
	MaxRuns     = 100
	MaxLiveKey  = 16
	MaxLiveHost = 64
	Retention   = 7 * 24 * time.Hour
	MaxAge      = 24 * time.Hour
)

var ErrNeedsAttention = errors.New("run store needs operator reclamation")

var (
	ErrAgentUnavailable = errors.New("agent runtime unavailable")
	ErrStopping         = errors.New("runtime stopping")
	ErrQuarantined      = errors.New("runtime quarantined")
	ErrQueueLimit       = errors.New("image queue limit reached")
	ErrNotFound         = errors.New("run not found")
	ErrLimit            = errors.New("run limit reached")
	ErrInvalid          = errors.New("invalid run request")
	ErrConflict         = errors.New("run state changed")
)

type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Waiting   State = "waiting"
	Done      State = "done"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

func terminal(s State) bool { return s == Done || s == Failed || s == Cancelled }

type Attempt struct {
	Purpose             string          `json:"purpose,omitempty"`
	ID                  string          `json:"id"`
	Dispatched          bool            `json:"dispatched"`
	Settled             bool            `json:"settled"`
	AccountingUncertain bool            `json:"accounting_uncertain"`
	Usage               usage.Event     `json:"usage"`
	Output              json.RawMessage `json:"output,omitempty"`
}
type Run struct {
	ClientRequestID string          `json:"client_request_id,omitempty"`
	Started         *time.Time      `json:"started,omitempty"`
	Batch           *Batch          `json:"batch,omitempty"`
	ID              string          `json:"id"`
	KeyID           string          `json:"key_id"`
	Kind            string          `json:"kind"`
	Priority        string          `json:"priority"`
	State           State           `json:"state"`
	Created         time.Time       `json:"created"`
	Updated         time.Time       `json:"updated"`
	Expires         time.Time       `json:"expires"`
	CancelRequested bool            `json:"cancel_requested"`
	Reason          string          `json:"reason,omitempty"`
	Input           json.RawMessage `json:"input"`
	Output          json.RawMessage `json:"output,omitempty"`
	Attempts        []Attempt       `json:"attempts"`
}
type Summary struct {
	Created         time.Time `json:"created"`
	ClientRequestID string    `json:"client_request_id,omitempty"`
	ID              string    `json:"id"`
	KeyID           string    `json:"key_id"`
	Kind            string    `json:"kind"`
	Priority        string    `json:"priority"`
	State           State     `json:"state"`
	Updated         time.Time `json:"updated"`
	Expires         time.Time `json:"expires"`
}

func summary(r Run) Summary {
	return Summary{ID: r.ID, KeyID: r.KeyID, Kind: r.Kind, Priority: r.Priority, State: r.State, Updated: r.Updated, Expires: r.Expires, ClientRequestID: r.ClientRequestID, Created: r.Created}
}

type Event struct {
	Type      string     `json:"type,omitempty"`
	Step      *StepEvent `json:"step,omitempty"`
	Cursor    string     `json:"cursor"`
	RunID     string     `json:"run_id,omitempty"`
	State     State      `json:"state,omitempty"`
	AttemptID string     `json:"attempt_id,omitempty"`
	Time      time.Time  `json:"time"`
	Reset     bool       `json:"reset,omitempty"`
	Runs      []Summary  `json:"runs,omitempty"`
}
type Step struct {
	Purpose      string
	RequireAgent bool
	// Observe borrows read-only callback-scoped bytes; copy before retention.
	// Delivery must be bounded and must not re-enter Work. Errors settle through
	// the request owner; no callback occurs after ExecuteStep returns.
	Observe func([]byte) error
	RunID   string
	Route   string
	Input   json.RawMessage
}
type StepResult struct {
	Output              json.RawMessage
	Usage               usage.Event
	Dispatched, Settled bool
}

// Executor returns only after the gateway's sole finish/settlement path has released capacity.
type Executor func(context.Context, string, Step, func() error) (StepResult, error)
type Decision struct {
	Step   *Step
	Wait   string
	Output json.RawMessage
}

// Kind is host-registered code, not a client-supplied program. Production registers image when configured.
type Kind func(context.Context, Run) (Decision, error)

// Failure carries only an adapter-authored cause, never a backend error body.
type Failure string

func (f Failure) Error() string { return string(f) }
func failureReason(err error) string {
	var f Failure
	if errors.As(err, &f) {
		switch f {
		case "runtime_lost", "retention_refused", "step_invalid", "key_revoked", "approval_invalid", "workspace_unavailable":
			return string(f)
		}
	}
	return "kind failed"
}
