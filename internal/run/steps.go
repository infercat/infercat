package run

import (
	"strings"
	"time"
)

// StepEvent is a complete replacement, with immutable identity and start time.
type StepEvent struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Name     string    `json:"name,omitempty"`
	Tool     string    `json:"tool,omitempty"`
	Result   string    `json:"result,omitempty"`
	Status   string    `json:"status"`
	Text     string    `json:"text,omitempty"`
	OutputID string    `json:"output_id,omitempty"`
}

func (s StepEvent) Valid() bool {
	return safeID.MatchString(s.ID) && s.Type == "step" && !s.At.IsZero() &&
		strings.Contains("|think|search|run|read|write|wait|other|", "|"+s.Kind+"|") &&
		strings.Contains("|running|waiting|done|failed|cancelled|", "|"+s.Status+"|") &&
		!strings.ContainsAny(s.Result, "\r\n") && len(s.Text) <= MaxOutput
}
