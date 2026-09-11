package run

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

type StoredRun struct {
	Summary
	Expires *time.Time   `json:"expires,omitempty"`
	Bytes   int          `json:"bytes"` // Run JSON plus its retained payload, excluding shared event/envelope bytes.
	Image   *StoredImage `json:"image,omitempty"`
}
type StoredImage struct {
	Bytes   int       `json:"bytes"`
	Expires time.Time `json:"expires"`
}
type StoredKind struct {
	Stored int `json:"stored"`
	Live   int `json:"live"`
}
type StoredData struct {
	Cursor          string                `json:"cursor"`
	Reserved        int                   `json:"reserved"`
	ObservedOrphans int                   `json:"observed_orphans"`
	RetryCleanup    int                   `json:"retry_cleanup"`
	KeyID           string                `json:"key_id"`
	Kinds           map[string]StoredKind `json:"kinds"`
	Total           int                   `json:"total"`
	Terminal        int                   `json:"terminal"`
	Bytes           int                   `json:"bytes"`
	Budget          int                   `json:"budget"`
	Images          int                   `json:"images"`
	ImageBytes      int                   `json:"image_bytes"`
	ImageBudget     int                   `json:"image_budget"`
	ClearImages     int                   `json:"clear_images"`
	First           *time.Time            `json:"expires_first,omitempty"`
	Last            *time.Time            `json:"expires_last,omitempty"`
	PendingCleanup  *int                  `json:"pending_cleanup,omitempty"`
	Runs            []StoredRun           `json:"runs"`
	Truncated       bool                  `json:"truncated"`
}

func (s *Store) Stored(key string) (StoredData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := s.accessed[key]
	defer func() { s.accessed[key] = last }()
	return s.stored(key)
}
func (s *Store) stored(key string) (StoredData, error) {
	v, err := s.load(key)
	if err != nil {
		return StoredData{}, err
	}
	out := StoredData{KeyID: key, Cursor: fmt.Sprintf("%s:%d", v.Epoch, v.Seq), Kinds: map[string]StoredKind{}, Total: len(v.Runs), Bytes: v.encodedBytes, Budget: s.maxStored - MaxLiveKey*terminalBound, Reserved: s.reservedBytes(key), ImageBudget: ImageBudget, Runs: []StoredRun{}}
	expiry := func(at time.Time) {
		if out.First == nil || at.Before(*out.First) {
			t := at
			out.First = &t
		}
		if out.Last == nil || at.After(*out.Last) {
			t := at
			out.Last = &t
		}
	}
	for id, r := range v.Runs {
		raw, _ := json.Marshal(r)
		row := StoredRun{Summary: summary(r), Bytes: len(raw)}
		if held, ok := v.Retained[id]; ok {
			raw, _ = json.Marshal(held)
			row.Bytes += len(raw)
		}
		kind := out.Kinds[r.Kind]
		kind.Stored++
		if !terminal(r.State) {
			kind.Live++
		} else {
			out.Terminal++
		}
		out.Kinds[r.Kind] = kind
		if terminal(r.State) {
			at := r.Expires
			row.Expires = &at
			expiry(r.Expires)
		}
		if image, ok := imageOutput(r); ok && !image.Gone {
			if terminal(r.State) {
				out.ClearImages++
			}
			if image.ExpiresAt.After(s.now()) {
				row.Image = &StoredImage{image.Bytes, image.ExpiresAt}
				out.Images++
				out.ImageBytes += image.Bytes
				if terminal(r.State) {
					expiry(image.ExpiresAt)
				}
			}
		}
		out.Runs = append(out.Runs, row)
	}
	dir := filepath.Join(s.root, key, "images")
	pending := 0
	for path := range s.imageCleanup {
		if filepath.Dir(path) == dir {
			pending++
			r, ok := v.Runs[filepath.Base(path)]
			if !ok || terminal(r.State) {
				out.RetryCleanup++
			}
		}
	}
	for path := range s.imageOrphans {
		if filepath.Dir(path) == dir && !s.imageCleanup[path] {
			out.ObservedOrphans++
		}
	}
	out.PendingCleanup = &pending
	sort.Slice(out.Runs, func(i, j int) bool {
		a, b := out.Runs[i], out.Runs[j]
		if a.Bytes != b.Bytes {
			return a.Bytes > b.Bytes
		}
		ac, bc := v.Runs[a.ID].Created, v.Runs[b.ID].Created
		if !ac.Equal(bc) {
			return ac.After(bc)
		}
		return a.ID < b.ID
	})
	out.Truncated = len(out.Runs) > 20
	if out.Truncated {
		out.Runs = out.Runs[:20]
	}
	return out, nil
}

type ClearExpectation struct {
	Cursor   string `json:"cursor"`
	Terminal int    `json:"terminal"`
	Images   int    `json:"images"`
	Cleanup  int    `json:"cleanup"`
}

// Manager lock excludes outstanding workers, including terminal records awaiting settlement.
type ClearResult struct {
	Cleared int    `json:"cleared"`
	Retried int    `json:"retried_cleanup"`
	Skipped int    `json:"skipped"`
	Images  int    `json:"cleared_images"`
	Warning string `json:"warning,omitempty"`
}

func (m *Manager) ClearTerminal(key string, expected ClearExpectation) (result ClearResult, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.Store
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.stored(key)
	if err != nil {
		return result, err
	}
	if expected.Cursor != current.Cursor || expected.Terminal != current.Terminal || expected.Images != current.ClearImages || expected.Cleanup != current.RetryCleanup {
		return result, ErrConflict
	}
	v := s.data[key]
	next := clone(v)
	intents := map[string]bool{}
	for _, rid := range v.Cleanup {
		intents[rid] = true
	}
	for rid, r := range v.Runs {
		if terminal(r.State) && m.active[rid] != nil {
			result.Skipped++
			continue
		}
		if terminal(r.State) {
			result.Cleared++
			if image, ok := imageOutput(r); ok && !image.Gone {
				result.Images++
			}
			delete(next.Runs, rid)
			intents[rid] = true
		}
	}
	dir := filepath.Join(s.root, key, "images")
	var directRetries []string
	for path := range s.imageCleanup {
		rid := filepath.Base(path)
		r, ok := v.Runs[rid]
		if filepath.Dir(path) == dir && (!ok || (terminal(r.State) && m.active[rid] == nil)) {
			result.Retried++
			if !safeID.MatchString(rid) {
				directRetries = append(directRetries, path)
				continue
			}
			intents[rid] = true
		}
	}
	next.Cleanup = nil
	for rid := range intents {
		next.Cleanup = append(next.Cleanup, rid)
	}
	sort.Strings(next.Cleanup)
	if result.Cleared > 0 || len(next.Cleanup) > 0 {
		if result.Cleared > 0 {
			next.Epoch = id("e_")
			next.Seq = 0
			next.Events = nil
		}
		if err = s.commit(key, next, nil); err != nil {
			return result, err
		}
		s.restoreCleanup(key, next)
		remaining := []Summary{}
		for _, r := range next.Runs {
			remaining = append(remaining, summary(r))
		}
		if result.Cleared > 0 {
			s.publish(key, Event{Cursor: next.Epoch + ":0", Time: s.now().UTC(), Reset: true, Runs: remaining})
		}
		if err = s.drainCleanup(key); err != nil {
			result.Warning = "Stored data cleared; cleanup step failed: " + err.Error()
			return result, nil
		}
	}
	for _, path := range directRetries {
		s.unlinkImage(path)
	}
	if len(next.Runs) == 0 {
		if err = s.sweepImages(key, s.data[key]); err != nil {
			if result.Cleared > 0 || len(next.Cleanup) > 0 {
				result.Warning = "Stored data cleared; cleanup step failed: " + err.Error()
				return result, nil
			}
			return result, err
		}
	}
	return result, nil
}
