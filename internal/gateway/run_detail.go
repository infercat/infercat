package gateway

import (
	"encoding/json"
	"errors"
	runstate "github.com/infercat/infercat/internal/run"
	"io"
	"net/http"
)

func runView(s *runstate.Store, key, id string) (any, error) { return s.Detail(key, id) }
func (q *request) runDetail(parts []string) {
	m := q.g.runs
	if len(parts) == 3 && parts[1] == "outputs" && q.r.Method == http.MethodGet {
		o, err := m.Store.CapturedOutput(q.key.ID, parts[0], parts[2])
		if err != nil {
			q.fail(runError(err))
			return
		}
		q.w.Header().Set("Content-Type", o.MIME)
		q.w.Header().Set("X-Content-Type-Options", "nosniff")
		q.outcome = outcomeServed
		q.writeHeader(http.StatusOK)
		_, _ = q.w.Write(o.Data)
		return
	}
	if len(parts) == 2 && parts[1] == "approval" && q.r.Method == http.MethodPost {
		var input struct {
			ID    string `json:"id"`
			Allow *bool  `json:"allow"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(q.w, q.r.Body, 1024))
		dec.DisallowUnknownFields()
		if dec.Decode(&input) != nil || input.Allow == nil || dec.Decode(new(any)) != io.EOF {
			q.fail(runError(runstate.ErrInvalid))
			return
		}
		var ended *runstate.CommittedTerminal
		if _, err := m.Answer(q.key.ID, parts[0], input.ID, *input.Allow); err != nil && !errors.As(err, &ended) {
			q.fail(runError(err))
			return
		}
		v, err := runView(m.Store, q.key.ID, parts[0])
		if err != nil {
			q.fail(runError(err))
			return
		}
		q.w.Header().Set("Content-Type", "application/json")
		q.outcome = outcomeServed
		q.writeHeader(http.StatusOK)
		_ = json.NewEncoder(q.w).Encode(v)
		return
	}
	q.fail(runError(runstate.ErrNotFound))
}
