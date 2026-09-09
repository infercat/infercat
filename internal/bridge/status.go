package bridge

import (
	"sync"
	"time"

	"github.com/infercat/infercat/internal/usage"
)

// Status contains public endpoint facts, never the registration credential.
type Status struct {
	Enabled       bool      `json:"enabled"`
	URL           string    `json:"url"`
	Connected     bool      `json:"connected"`
	Since         time.Time `json:"since"` // When the current enabled/connection state began.
	LastError     string    `json:"last_error"`
	RequestsToday int       `json:"requests_today"`
}

// Separate from the reload lock: stopping a client joins it while it reports disconnection.
type connectionState struct {
	mu sync.Mutex
	s  Status
}

func (s *connectionState) configure(c Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.s = Status{}
	if c != (Config{}) {
		s.s = Status{Enabled: !c.Disabled, URL: c.URL(), Since: time.Now().UTC()}
	}
}

func (s *connectionState) connection(connected bool, message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s.Connected != connected {
		s.s.Since = time.Now().UTC()
	}
	s.s.Connected, s.s.LastError = connected, message
}

func (m *Manager) Status(dir string) *Status {
	m.state.mu.Lock()
	s := m.state.s
	m.state.mu.Unlock()
	if s.URL == "" {
		return nil
	}
	// Read the durable log, so restart and off/on never reset today's count. Like usage,
	// this counts completed requests by their start date (UTC), including gateway refusals.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	rep, err := usage.AggregateFile(dir, usage.Filter{Since: today, Until: today.Add(24 * time.Hour)})
	if err != nil {
		s.LastError = "bridge usage unavailable: could not read usage.jsonl"
	} else if via := rep.ByVia["bridge"]; via != nil {
		s.RequestsToday = via.Requests
	}
	return &s
}
