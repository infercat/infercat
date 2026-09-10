package upstream

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

// ImageEngine shares the explicit engine probe/transport; it needs no tokenizer.
type ImageEngine interface {
	Info() Info
	Refresh(context.Context) error
	ImageDo(context.Context, []byte) (*http.Response, error)
	RecordSuccess(time.Duration)
}

// ImageGenerationTimeout bounds the complete non-streaming image response.
const ImageGenerationTimeout = 15 * time.Minute

type imageClient struct {
	*audioClient
	active         int
	started        time.Time
	longestSuccess time.Duration
	generation     uint64 // protected by audioClient.mu, including failure publication
}

func OpenImages(ctx context.Context, url, key string) (ImageEngine, error) {
	if url == "" {
		return nil, nil
	}
	base, e := normalize(url)
	if e != nil {
		return nil, e
	}
	c := newClientWithHeaderTimeout(base, key, ImageGenerationTimeout)
	c.hc.Timeout = ImageGenerationTimeout
	a := &audioClient{c: c, info: Info{URL: base.String(), Slots: 1}}
	_ = a.Refresh(ctx)
	return &imageClient{audioClient: a}, nil
}
func (i *imageClient) ImageDo(ctx context.Context, body []byte) (*http.Response, error) {
	i.mu.Lock()
	if i.active == 0 {
		i.started = time.Now()
	}
	i.active++
	i.generation++
	i.mu.Unlock()
	var once sync.Once
	done := func() { once.Do(func() { i.mu.Lock(); i.active--; i.generation++; i.mu.Unlock() }) }
	response, err := i.AudioDo(ctx, "/v1/images/generations", "application/json", body)
	if err != nil {
		done()
		return response, err
	}
	response.Body = &imageResponseBody{ReadCloser: response.Body, done: done}
	return response, nil
}

func (i *imageClient) Refresh(ctx context.Context) error {
	i.mu.RLock()
	generation, active, started := i.generation, i.active, i.started
	grace := min(10*time.Minute, max(3*time.Minute, 2*i.longestSuccess))
	i.mu.RUnlock()
	return i.refresh(ctx, func() bool {
		return (active > 0 && time.Since(started) >= grace) || (i.active > 0 && time.Since(i.started) >= grace) || (active == 0 && i.active == 0 && i.generation == generation)
	})
}

type imageResponseBody struct {
	io.ReadCloser
	done func()
}

func (b *imageResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.done()
	}
	return n, err
}
func (b *imageResponseBody) Close() error { err := b.ReadCloser.Close(); b.done(); return err }

// The gateway calls this only after decoding a valid, bounded image.
func (i *imageClient) RecordSuccess(elapsed time.Duration) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.longestSuccess = max(i.longestSuccess, min(max(0, elapsed), ImageGenerationTimeout))
}
