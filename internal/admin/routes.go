package admin

import (
	"net/http"
	"slices"
	"strings"
)

// Mux records the operator routes at the same call that registers them. The CLI contract
// inventory reads this list; routing and authentication remain net/http's and the server's.
type Mux struct {
	*http.ServeMux
	patterns []string
}

func NewMux() *Mux { return &Mux{ServeMux: http.NewServeMux()} }
func (m *Mux) HandleFunc(pattern string, f func(http.ResponseWriter, *http.Request)) {
	m.Handle(pattern, http.HandlerFunc(f))
}
func (m *Mux) Handle(pattern string, h http.Handler) {
	m.ServeMux.Handle(pattern, h)
	if pattern == "/" {
		m.patterns = append(m.patterns, Routes(h)...)
	} else {
		m.patterns = append(m.patterns, pattern)
	}
}
func Routes(h http.Handler) []string {
	if v, ok := h.(interface{ Routes() []string }); ok {
		return v.Routes()
	}
	return nil
}
func (m *Mux) Routes() []string {
	var out []string
	for _, p := range m.patterns {
		if !strings.Contains(p, " ") {
			// A methodless legacy handler may serve several verbs; explicit registrations
			// carry those verbs. /status's historical methodless match denotes its GET API.
			if slices.Contains(m.patterns, "GET "+p) || slices.Contains(m.patterns, "DELETE "+p) {
				continue
			}
			p = "GET " + p
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func withRoutes(next, h http.Handler, patterns ...string) http.Handler {
	m := NewMux()
	for _, p := range patterns {
		m.Handle(p, h)
	}
	m.Handle("/", next)
	return m
}
