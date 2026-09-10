package bridge

import (
	"sync"
	"time"

	"github.com/infercat/infercat/internal/usage"
)

// Status contains public endpoint facts, never the registration credential.
type Status struct {
	Slots         int       `json:"slots"` // Negotiated for this connection; zero when disconnected.
	Enabled       bool      `json:"enabled"`
	URL           string    `json:"url"`
	Connected     bool      `json:"connected"`
	Since         time.Time `json:"since"` // When the current enabled/connection state began.
	LastError     string    `json:"last_error"`
	RequestsToday int       `json:"requests_today"`
}

// Separate from the reload lock: stopping a client joins it while it reports disconnection.
type connectionState struct {
	mu           sync.Mutex
	s            Status
	day, started time.Time
	requests     int
	seeded       bool
	seedError    string
}

func (s *connectionState) configure(c Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.s = Status{}
	if c != (Config{}) {
		s.s = Status{Enabled: !c.Disabled, URL: c.URL(), Since: time.Now().UTC()}
	}
}

func (s *connectionState) connection(connected bool, message string, slots ...int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s.Connected != connected {
		s.s.Since = time.Now().UTC()
	}
	if !connected {
		s.s.Slots = 0
	} else if len(slots) > 0 {
		s.s.Slots = slots[0]
	}
	s.s.Connected, s.s.LastError = connected, message
}

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now().UTC()
	}
	return time.Now().UTC()
}

func (s *connectionState) rollDay(now time.Time) {
	day := now.Truncate(24 * time.Hour)
	if s.day != day {
		s.day, s.requests = day, 0
	}
}

// Count runs after the recorder has assigned trusted provenance. Completion keeps the
// event's request-start day, so a request crossing midnight does not count in the new day.
func (m *Manager) Count(e usage.Event) {
	if e.Via != "bridge" {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	now := m.clock()
	if e.TS.IsZero() {
		e.TS = now
	}
	m.state.rollDay(now)
	if !e.TS.Before(m.state.day) && e.TS.Before(m.state.day.Add(24*time.Hour)) {
		m.state.requests++
	}
}

// Called on startup reload, then only retried after a failed seed. The cutoff stays at
// startup: recovery adds historical events without counting this process's live events twice.
// File I/O holds neither the status mutex nor the recorder path while friends are served.
func (m *Manager) seedCounter(dir string, logf func(string, ...any)) {
	m.state.mu.Lock()
	if m.state.seeded {
		m.state.mu.Unlock()
		return
	}
	now := m.clock()
	m.state.rollDay(now)
	if m.state.started.IsZero() {
		m.state.started = now
	}
	day, cutoff := m.state.day, m.state.started
	m.state.mu.Unlock()
	rep, err := usage.AggregateFile(dir, usage.Filter{Since: day, Until: cutoff})
	m.state.mu.Lock()
	m.state.rollDay(m.clock())
	if err != nil {
		m.state.seedError = "bridge usage counter incomplete: could not read usage.jsonl; reload to retry"
		m.state.mu.Unlock()
		logf("WARNING: bridge usage counter starts without history: %v; serving continues; reload to retry", err)
		return
	}
	if via := rep.ByVia["bridge"]; via != nil && m.state.day == day {
		m.state.requests += via.Requests
	}
	m.state.seeded, m.state.seedError = true, ""
	m.state.mu.Unlock()
}

func (m *Manager) Status() *Status {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.rollDay(m.clock())
	s := m.state.s
	if s.URL == "" {
		return nil
	}
	s.RequestsToday = m.state.requests
	if m.state.seedError != "" {
		if s.LastError != "" {
			s.LastError += "; "
		}
		s.LastError += m.state.seedError
	}
	return &s
}
