package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/infercat/infercat/internal/keys"
	runstate "github.com/infercat/infercat/internal/run"
	"github.com/infercat/infercat/internal/usage"
)

const searchOutputLimit = 16 << 10

// Search owns the operator credential in memory; only its path belongs in config.
type Search struct {
	key, endpoint string
	client        *http.Client
}

// AgentKey supplies the same startup-loaded credential to the confined child pipe.
func (s *Search) AgentKey() string {
	if s == nil {
		return ""
	}
	return s.key
}

func OpenSearch(path string) (*Search, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("search key file unavailable")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	key := strings.TrimSpace(string(b))
	if err != nil || len(b) > 4096 || key == "" || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("search key file invalid")
	}
	return &Search{key: key, endpoint: "https://api.exa.ai/search", client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (s *Search) query(ctx context.Context, query string, count int) (string, bool) {
	body, _ := json.Marshal(map[string]any{"query": query, "numResults": count, "contents": map[string]any{"highlights": map[string]int{"maxCharacters": 1600}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return "search unavailable", false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.key)
	res, err := s.client.Do(req)
	if err != nil {
		return "search unavailable", false
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 128<<10+1))
	var reply struct {
		Results []struct {
			Title, URL string
			Highlights []string
		} `json:"results"`
	}
	if err != nil || res.StatusCode != 200 || len(raw) > 128<<10 || json.Unmarshal(raw, &reply) != nil || reply.Results == nil {
		return "search unavailable", false
	}
	var out strings.Builder
	for i, r := range reply.Results {
		if i >= count {
			break
		}
		fmt.Fprintf(&out, "%d. %s\n%s\n%s\n\n", i+1, r.Title, r.URL, strings.Join(r.Highlights, " "))
		if out.Len() >= searchOutputLimit {
			break
		}
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return "no results", true
	}
	if len(text) > searchOutputLimit {
		text = text[:searchOutputLimit]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text, true
}

// Reserve under the existing meter lock; only this attempt settles its reservation.
func (g *Gateway) search(ctx context.Context, key *keys.Key, query string, count int) (string, error) {
	st := g.lim.state(key.ID)
	st.mu.Lock()
	start := g.lim.now()
	st.prune(start)
	meter := st.meter("search")
	limit := key.Limits.Budgets().Amount("search", "day")
	if limit > 0 && meter.today+meter.reserved >= float64(limit) {
		st.mu.Unlock()
		return "", errf(CodeBudgetExhausted, secondsUntil(start.UTC().Truncate(24*time.Hour).Add(24*time.Hour), start), "search budget exhausted: %d searches per UTC day", limit)
	}
	meter.reserved++
	st.mu.Unlock()
	attempted, valid := false, false
	defer func() {
		st.mu.Lock()
		at := g.lim.now()
		st.prune(at)
		meter.reserved--
		charge, measured := 0.0, 0.0
		if attempted {
			charge = 1
			meter.today++
		}
		if valid {
			measured = 1
		}
		st.mu.Unlock()
		if attempted {
			status, code := 200, ""
			if !valid {
				status, code = 502, string(CodeUpstreamError)
			}
			g.rec.Record(context.WithoutCancel(ctx), usage.Event{TS: start, SettledAt: at, KeyID: key.ID, Endpoint: "web_search", Kind: "search", Status: status, Code: code, TotalMS: at.Sub(start).Milliseconds(), Meters: []usage.Meter{{Class: "search", Unit: "requests", Measured: measured, Charged: charge}}})
		}
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	attempted = true
	text, ok := g.cfg.Search.query(ctx, query, count)
	valid = ok
	return text, nil
}

func (g *Gateway) searchTool(ctx context.Context, w *runstate.Work, raw string, allowed bool) (text string, dispatched bool, err error) {
	release, err := w.Store.Admit(w.Run.KeyID, w.Run.ID, 32<<10)
	if err != nil {
		return "", false, err
	}
	defer release()
	id := "search_" + rand.Text()
	step := runstate.StepEvent{ID: id, Type: "step", At: time.Now().UTC(), Kind: "search", Name: "Search web", Tool: "web_search", Status: "running"}
	if err = w.Store.Retain(w.Run.KeyID, w.Run.ID, nil, nil, nil, step); err != nil {
		return "", false, err
	}
	args := struct {
		Query string          `json:"query"`
		Count json.RawMessage `json:"count"`
	}{}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	valid := d.Decode(&args) == nil && d.Decode(new(any)) == io.EOF
	count := 3
	if args.Count != nil {
		valid = valid && string(args.Count) != "null" && json.Unmarshal(args.Count, &count) == nil
	}
	text = "search needs a query (at most 4096 bytes) and count from 1 to 5"
	if !allowed {
		text = "search limit reached for this turn"
	} else if valid && strings.TrimSpace(args.Query) != "" && len(args.Query) <= 4096 && count >= 1 && count <= 5 {
		key, e := g.chatKey(ctx, w.Run.KeyID)
		err = e
		if err == nil && g.cfg.Search != nil {
			text, err = g.search(ctx, key, args.Query, count)
			dispatched = err == nil
		} else {
			text = "search unavailable"
		}
	}
	step.Status = "done"
	step.Result = "Search results"
	if err != nil {
		text = err.Error()
	}
	if !dispatched {
		step.Status = "failed"
		step.Result = text
	} else if text == "no results" || text == "search unavailable" {
		step.Result = text
	}
	step.OutputID = id + "_result"
	outputs := map[string]runstate.Captured{step.OutputID: {Name: "Search results", MIME: "text/plain; charset=utf-8", Data: []byte(text)}}
	if e := w.Store.Retain(w.Run.KeyID, w.Run.ID, nil, nil, outputs, step); e != nil {
		return "", dispatched, e
	}
	return text, dispatched, err
}
