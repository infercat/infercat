package admin

import (
	"encoding/json"
	"net/http"

	runstate "github.com/infercat/infercat/internal/run"
)

// WithRuns adds metadata listing behind the existing local/remote admin authentication.
func WithRuns(next http.Handler, list func(string) ([]runstate.Summary, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/runs" {
			next.ServeHTTP(w, r)
			return
		}
		rows, err := list(r.URL.Query().Get("key_id"))
		if err != nil {
			http.Error(w, "run store unavailable", http.StatusServiceUnavailable)
			return
		}
		more := len(rows) > runstate.MaxRuns
		if more {
			rows = rows[:runstate.MaxRuns]
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			Runs      []runstate.Summary `json:"runs"`
			Truncated bool               `json:"truncated"`
		}{rows, more})
	})
}
