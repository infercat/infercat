package run

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const MaxImage = 8 << 20
const ImageBudget = 256 << 20

type Batch struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	Count int    `json:"count"`
}
type ImageInput struct {
	ClientRequestID string `json:"client_request_id,omitempty"`
	Prompt          string `json:"prompt"`
	Conversation    string `json:"conversation,omitempty"`
}
type ImageOutput struct {
	URL       string    `json:"url"`
	MIME      string    `json:"mime"`
	Width     int       `json:"w"`
	Height    int       `json:"h"`
	Bytes     int       `json:"bytes"`
	ExpiresAt time.Time `json:"expiresAt"`
	Gone      bool      `json:"gone,omitempty"`
}

func ValidateImage(input json.RawMessage) error {
	var in ImageInput
	if json.Unmarshal(input, &in) != nil || strings.TrimSpace(in.Prompt) == "" || len(input) > MaxInput || len(in.Conversation) > 128 || len(in.ClientRequestID) > 128 || strings.Contains(in.Prompt, "<sd_cpp_extra_args>") {
		return ErrInvalid
	}
	return nil
}
func ImageKind(_ context.Context, r Run) (Decision, error) {
	if err := ValidateImage(r.Input); err != nil {
		return Decision{}, err
	}
	if len(r.Attempts) > 0 {
		return Decision{Output: r.Attempts[len(r.Attempts)-1].Output}, nil
	}
	return Decision{Step: &Step{Route: "/v1/images/generations", Input: r.Input, RunID: r.ID}}, nil
}
func imageOutput(r Run) (ImageOutput, bool) {
	var out ImageOutput
	if r.Kind != "image" || json.Unmarshal(r.Output, &out) != nil {
		return ImageOutput{}, false
	}
	return out, out.URL != ""
}
func (s *Store) artifactPath(key, rid string) string {
	return filepath.Join(s.root, key, "images", rid)
}

// Artifact bytes have their own budget. State and attempt outputs contain metadata only.
func (s *Store) PutImage(key, rid string, raw []byte, mime string, w, h int) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaxImage || (mime != "image/png" && mime != "image/jpeg") {
		return nil, ErrLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return nil, err
	}
	if err = s.sweepImages(key, v); err != nil {
		return nil, err
	}
	v = s.data[key]
	old, ok := v.Runs[rid]
	if !ok || old.Kind != "image" || terminal(old.State) {
		return nil, ErrNotFound
	}
	if err = privateDir(filepath.Dir(s.artifactPath(key, rid))); err != nil {
		return nil, err
	}
	budget := s.imageBudget
	if budget == 0 {
		budget = ImageBudget
	}
	next := clone(v)
	type held struct {
		id     string
		output ImageOutput
	}
	var heldImages []held
	total := len(raw)
	for id, r := range next.Runs {
		if o, ok := imageOutput(r); ok && !o.Gone {
			heldImages = append(heldImages, held{id, o})
			total += o.Bytes
		}
	}
	sort.Slice(heldImages, func(i, j int) bool {
		if heldImages[i].output.ExpiresAt.Equal(heldImages[j].output.ExpiresAt) {
			return heldImages[i].id < heldImages[j].id
		}
		return heldImages[i].output.ExpiresAt.Before(heldImages[j].output.ExpiresAt)
	})
	var evict []string
	for _, a := range heldImages {
		if total <= budget && a.output.ExpiresAt.After(s.now()) {
			continue
		}
		r := next.Runs[a.id]
		a.output.Gone = true
		r.Output, _ = json.Marshal(a.output)
		next.Runs[a.id] = r
		total -= a.output.Bytes
		evict = append(evict, a.id)
	}
	path := s.artifactPath(key, rid)
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return nil, ErrConflict
		}
		return nil, err
	}
	if err = atomicWrite(path, raw); err != nil {
		return nil, err
	}
	out := ImageOutput{URL: "/v1/images/outputs/" + rid, MIME: mime, Width: w, Height: h, Bytes: len(raw), ExpiresAt: s.now().UTC().Add(Retention)}
	result, _ := json.Marshal(out)
	old.Output = result
	next.Runs[rid] = old
	if err = s.commit(key, next, &Event{RunID: rid, State: old.State, Time: s.now().UTC()}); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	for _, id := range evict {
		s.unlinkImage(s.artifactPath(key, id))
	}
	return result, nil
}
func (s *Store) ReadImage(key, rid string) ([]byte, ImageOutput, error) {
	o, err := func() (ImageOutput, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		v, err := s.load(key)
		if err != nil {
			return ImageOutput{}, err
		}
		r, ok := v.Runs[rid]
		o, has := imageOutput(r)
		if !ok || !has || o.Gone || !o.ExpiresAt.After(s.now()) {
			return o, ErrNotFound
		}
		return o, nil
	}()
	if err != nil {
		return nil, o, err
	}
	path := s.artifactPath(key, rid)
	if info, e := os.Lstat(filepath.Dir(path)); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, o, ErrNotFound
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxImage {
		return nil, o, ErrNotFound
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, o, ErrNotFound
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxImage+1))
	if len(raw) > MaxImage {
		return nil, o, ErrLimit
	}
	return raw, o, err
}
func (s *Store) DiscardImage(key, rid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return err
	}
	r, ok := v.Runs[rid]
	o, has := imageOutput(r)
	if !ok || !has {
		return ErrNotFound
	}
	if o.Gone {
		return nil
	}
	o.Gone = true
	r.Output, _ = json.Marshal(o)
	next := clone(v)
	next.Runs[rid] = r
	if err = s.commit(key, next, &Event{RunID: rid, State: r.State, Time: s.now().UTC()}); err != nil {
		return err
	}
	s.unlinkImage(s.artifactPath(key, rid))
	return nil
}

// Called under the store lock after expiry. Removes orphaned files after an interrupted commit too.
func (s *Store) sweepImages(key string, v *snapshot) error {
	dir := filepath.Join(s.root, key, "images")
	for path := range s.imageCleanup {
		if filepath.Dir(path) == dir {
			s.unlinkImage(path)
		}
	}
	if info, e := os.Lstat(dir); errors.Is(e, os.ErrNotExist) {
		for path := range s.imageCleanup {
			if filepath.Dir(path) == dir {
				delete(s.imageCleanup, path)
			}
		}
		return nil
	} else if e != nil {
		return e
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalid
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if v == nil || len(v.Runs) == 0 {
		info, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if s.emptyImageStamp == nil {
			s.emptyImageStamp = map[string]time.Time{}
		}
		if len(entries) > 0 && !s.emptyImageStamp[key].Equal(info.ModTime()) {
			s.log("image scan skipped for empty snapshot: %s", key)
			s.emptyImageStamp[key] = info.ModTime()
		}
		return nil
	}
	delete(s.emptyImageStamp, key)
	for path := range s.imageCleanup {
		if filepath.Dir(path) == dir {
			delete(s.imageCleanup, path)
		}
	}
	next := clone(v)
	changed := false
	for id, r := range next.Runs {
		if o, ok := imageOutput(r); ok && !o.Gone && !o.ExpiresAt.After(s.now()) {
			o.Gone = true
			r.Output, _ = json.Marshal(o)
			next.Runs[id] = r
			changed = true
		}
	}
	if changed {
		if err = s.commit(key, next, nil); err != nil {
			return err
		}
	}
	for _, e := range entries {
		r, exists := next.Runs[e.Name()]
		o, ok := imageOutput(r)
		if !exists || !ok || o.Gone {
			s.unlinkImage(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

func (s *Store) unlinkImage(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		if s.imageCleanup == nil {
			s.imageCleanup = map[string]bool{}
		}
		s.imageCleanup[path] = true
		if s.Log != nil {
			s.Log("image cleanup pending for %s: %v", path, err)
		} else {
			log.Printf("image cleanup pending for %s: %v", path, err)
		}
	} else {
		delete(s.imageCleanup, path)
	}
}
func (s *Store) ImageCleanupPending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.imageCleanup)
}
