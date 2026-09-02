package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Filter selects which events an aggregation counts.
type Filter struct {
	KeyID string    // empty = every key
	Since time.Time // zero = from the beginning
}

func (f Filter) match(e *Event) bool {
	if f.KeyID != "" && e.KeyID != f.KeyID {
		return false
	}
	if !f.Since.IsZero() && e.TS.Before(f.Since) {
		return false
	}
	return true
}

// Stats are the numbers `usage` prints, for one key or for the whole file.
type Stats struct {
	KeyID            string         `json:"key_id,omitempty"`
	Requests         int            `json:"requests"`
	Errors           int            `json:"errors"`
	ErrorsByCode     map[string]int `json:"errors_by_code,omitempty"`
	PromptTokens     int            `json:"prompt_tokens"`
	CompletionTokens int            `json:"completion_tokens"`
	TTFTMedianMS     int64          `json:"ttft_median_ms"`
	TTFTP95MS        int64          `json:"ttft_p95_ms"`
	TotalMedianMS    int64          `json:"total_median_ms"`
	TotalP95MS       int64          `json:"total_p95_ms"`
	FirstSeen        time.Time      `json:"first_seen,omitempty"`
	LastSeen         time.Time      `json:"last_seen,omitempty"`

	ttfts  []int64
	totals []int64
}

func (s *Stats) add(e *Event) {
	s.Requests++
	if e.Status >= 400 {
		s.Errors++
		code := e.Code
		if code == "" {
			code = "(none)"
		}
		if s.ErrorsByCode == nil {
			s.ErrorsByCode = map[string]int{}
		}
		s.ErrorsByCode[code]++
	}
	s.PromptTokens += e.PromptTokens
	s.CompletionTokens += e.CompletionTokens
	if e.TTFTMS > 0 {
		s.ttfts = append(s.ttfts, e.TTFTMS)
	}
	if e.TotalMS > 0 {
		s.totals = append(s.totals, e.TotalMS)
	}
	if s.FirstSeen.IsZero() || e.TS.Before(s.FirstSeen) {
		s.FirstSeen = e.TS
	}
	if e.TS.After(s.LastSeen) {
		s.LastSeen = e.TS
	}
}

func (s *Stats) finish() {
	s.TTFTMedianMS, s.TTFTP95MS = percentile(s.ttfts, 50), percentile(s.ttfts, 95)
	s.TotalMedianMS, s.TotalP95MS = percentile(s.totals, 50), percentile(s.totals, 95)
	s.ttfts, s.totals = nil, nil
}

// percentile is nearest-rank over a copy of v; 0 for an empty sample.
func percentile(v []int64, p int) int64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := (p*len(s) + 99) / 100 // ceil(p/100 * n)
	if rank < 1 {
		rank = 1
	}
	if rank > len(s) {
		rank = len(s)
	}
	return s[rank-1]
}

// Report is the whole-file answer: one Stats for everything plus one per key.
type Report struct {
	Total     Stats    `json:"total"`
	Keys      []*Stats `json:"keys"`
	Malformed int      `json:"malformed_lines"`
}

// Aggregate reads JSONL events and summarises the ones the filter admits. Lines that do not
// parse are counted and skipped: a half-written final line from a killed host must not make
// `usage` fail.
func Aggregate(r io.Reader, f Filter) (*Report, error) {
	rep := &Report{}
	byKey := map[string]*Stats{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			rep.Malformed++
			continue
		}
		if !f.match(&e) {
			continue
		}
		rep.Total.add(&e)
		s := byKey[e.KeyID]
		if s == nil {
			s = &Stats{KeyID: e.KeyID}
			byKey[e.KeyID] = s
		}
		s.add(&e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	rep.Total.finish()
	for _, s := range byKey {
		s.finish()
		rep.Keys = append(rep.Keys, s)
	}
	sort.Slice(rep.Keys, func(i, j int) bool { return rep.Keys[i].KeyID < rep.Keys[j].KeyID })
	return rep, nil
}

// AggregateFile is Aggregate over <dataDir>/usage.jsonl. A missing file is an empty report, not
// an error: `usage` before the first request is a legitimate thing to run.
func AggregateFile(dataDir string, f Filter) (*Report, error) {
	fh, err := os.Open(filepath.Join(dataDir, FileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Report{}, nil
		}
		return nil, err
	}
	defer fh.Close()
	return Aggregate(fh, f)
}

// LastSeen maps key id to the timestamp of its most recent event.
func (r *Report) LastSeen() map[string]time.Time {
	m := make(map[string]time.Time, len(r.Keys))
	for _, s := range r.Keys {
		m[s.KeyID] = s.LastSeen
	}
	return m
}
