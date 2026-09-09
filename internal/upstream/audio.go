package upstream

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"time"
)

// AudioEngine keeps the explicit engine's transport, bearer and address behind the seam.
type AudioEngine interface {
	Info() Info
	Refresh(context.Context) error
	AudioDo(context.Context, string, string, []byte) (*http.Response, error)
}
type audioClient struct {
	c    *client
	mu   sync.RWMutex
	info Info
}

func OpenAudio(ctx context.Context, rawURL, key string) (AudioEngine, error) {
	if rawURL == "" {
		return nil, nil
	}
	base, err := normalize(rawURL)
	if err != nil {
		return nil, err
	}
	a := &audioClient{c: newClient(base, key), info: Info{URL: base.String(), Slots: 1}}
	_ = a.Refresh(ctx)
	return a, nil
}
func (a *audioClient) Info() Info {
	a.mu.RLock()
	defer a.mu.RUnlock()
	i := a.info
	i.Models = append([]string(nil), i.Models...)
	return i
}

// Explicit endpoint configuration asserts the route. A successful /v1/models or /health probe
// asserts reachability only; it does not infer ASR/TTS from model names or generate any content.
func (a *audioClient) Refresh(ctx context.Context) error {
	var models modelsResponse
	err := a.c.getJSON(ctx, "/v1/models", &models)
	if err != nil {
		err = a.c.getJSON(ctx, "/health", nil)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	a.info.ProbedAt = now
	if a.info.Health.Since.IsZero() || a.info.Health.OK != (err == nil) {
		a.info.Health.Since = now
	}
	a.info.Health.OK = err == nil
	a.info.Models = nil
	if err != nil {
		a.info.Health.Err = err.Error()
		return err
	}
	a.info.Health.Err = ""
	for _, m := range models.Data {
		a.info.Models = append(a.info.Models, m.ID)
	}
	return nil
}
func (a *audioClient) AudioDo(ctx context.Context, path, contentType string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.c.base.String()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return a.c.hc.Do(req)
}
