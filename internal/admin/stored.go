package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	runstate "github.com/infercat/infercat/internal/run"
)

var storedKey = regexp.MustCompile(`^k_[A-Za-z0-9_-]{1,62}$`)

// WithStored shares the enclosing admin authentication; no all-key scan is exposed.
func WithStored(next http.Handler, read func(string) (runstate.StoredData, error), clear func(string, runstate.ClearExpectation) (runstate.ClearResult, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stored" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			w.Header().Set("Allow", "GET, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		query := r.URL.Query()
		key := query.Get("key_id")
		if len(query) != 1 || len(query["key_id"]) != 1 || !storedKey.MatchString(key) {
			http.Error(w, "one key_id is required", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodDelete {
			var expected runstate.ClearExpectation
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&expected); err != nil || expected.Cursor == "" {
				http.Error(w, "clear confirmation is required", 400)
				return
			}
			result, err := clear(key, expected)
			if err != nil {
				status := http.StatusServiceUnavailable
				if errors.Is(err, runstate.ErrConflict) {
					status = http.StatusConflict
				}
				http.Error(w, "stored data changed or could not be cleared; refresh before confirming", status)
				return
			}
			value, readErr := read(key)
			var stored *runstate.StoredData
			if readErr == nil {
				stored = &value
			} else if result.Warning == "" {
				result.Warning = "Stored data cleared; refreshed list unavailable: " + readErr.Error()
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				runstate.ClearResult
				Stored *runstate.StoredData `json:"stored,omitempty"`
			}{result, stored})
			return
		}
		value, err := read(key)
		if err != nil {
			http.Error(w, "run store unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	})
}
