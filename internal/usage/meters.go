package usage

import (
	"sort"
	"time"
)

// Meter separates what the engine reported from what settlement charged.
// Fractional seconds are preserved; token and character amounts are integral.
type Meter struct {
	Class    string  `json:"class"`
	Unit     string  `json:"unit"`
	Measured float64 `json:"measured"`
	Charged  float64 `json:"charged"`
}

// ResourceMeters maps pre-108 rows to their historical measured=charged behavior.
// Presence, not a nonzero charge, distinguishes new rows from legacy telemetry.
func (e Event) ResourceMeters() []Meter {
	if e.Meters != nil {
		return e.Meters
	}
	_, class := ModelEndpoint(e.Endpoint)
	var meters []Meter
	if e.PromptTokens != 0 || e.CompletionTokens != 0 || class == "tokens" {
		n := float64(e.PromptTokens) + float64(e.CompletionTokens)
		meters = append(meters, Meter{"tokens", "tokens", n, n})
	}
	if e.Seconds != 0 || e.Kind == "transcription" || class == "audio" {
		meters = append(meters, Meter{"audio", "seconds", e.Seconds, e.Seconds})
	}
	if e.Characters != 0 || e.Kind == "speech" || class == "speech" {
		n := float64(e.Characters)
		meters = append(meters, Meter{"speech", "characters", n, n})
	}
	return meters
}

func (s *Stats) addMeters(e *Event) {
	for _, m := range e.ResourceMeters() {
		found := false
		for i := range s.Meters {
			if s.Meters[i].Class == m.Class {
				s.Meters[i].Measured += m.Measured
				s.Meters[i].Charged += m.Charged
				found = true
				break
			}
		}
		if !found {
			s.Meters = append(s.Meters, m)
		}
	}
}

func (s *Stats) sortMeters() {
	sort.Slice(s.Meters, func(i, j int) bool { return s.Meters[i].Class < s.Meters[j].Class })
}

// AccountingTime keeps charges in their settlement window without moving request
// telemetry. Rows written before settled_at retain the historical TS convention.
func (e Event) AccountingTime() time.Time {
	if e.SettledAt.IsZero() {
		return e.TS
	}
	return e.SettledAt
}
