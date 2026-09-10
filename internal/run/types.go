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
	ErrStopping    = errors.New("runtime stopping")
	ErrQuarantined = errors.New("runtime quarantined")
	ErrQueueLimit  = errors.New("image queue limit reached")
	ErrNotFound    = errors.New("run not found")
	ErrLimit       = errors.New("run limit reached")
	ErrInvalid     = errors.New("invalid run request")
	ErrConflict    = errors.New("run state changed")
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
	ID                  string          `json:"id"`
	Dispatched          bool            `json:"dispatched"`
	Settled             bool            `json:"settled"`
	AccountingUncertain bool            `json:"accounting_uncertain"`
	Usage               usage.Event     `json:"usage"`
	Output              json.RawMessage `json:"output,omitempty"`
}
type Run struct {
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
	ID       string    `json:"id"`
	KeyID    string    `json:"key_id"`
	Kind     string    `json:"kind"`
	Priority string    `json:"priority"`
	State    State     `json:"state"`
	Updated  time.Time `json:"updated"`
	Expires  time.Time `json:"expires"`
}

func summary(r Run) Summary {
	return Summary{r.ID, r.KeyID, r.Kind, r.Priority, r.State, r.Updated, r.Expires}
}

type Event struct {
	Cursor    string    `json:"cursor"`
	RunID     string    `json:"run_id,omitempty"`
	State     State     `json:"state,omitempty"`
	AttemptID string    `json:"attempt_id,omitempty"`
	Time      time.Time `json:"time"`
	Reset     bool      `json:"reset,omitempty"`
	Runs      []Summary `json:"runs,omitempty"`
}
type Step struct {
	RunID string
	Route string
	Input json.RawMessage
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
