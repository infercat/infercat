package upstream

import (
	"context"
	"net/http"
	"time"
)

// ImageEngine shares the explicit engine probe/transport; it needs no tokenizer.
type ImageEngine interface {
	Info() Info
	Refresh(context.Context) error
	ImageDo(context.Context, []byte) (*http.Response, error)
}

// ImageGenerationTimeout bounds the complete non-streaming image response.
const ImageGenerationTimeout = 15 * time.Minute

type imageClient struct{ *audioClient }

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
	return &imageClient{a}, nil
}
func (i *imageClient) ImageDo(ctx context.Context, body []byte) (*http.Response, error) {
	return i.AudioDo(ctx, "/v1/images/generations", "application/json", body)
}
