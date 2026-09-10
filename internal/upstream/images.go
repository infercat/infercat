package upstream

import (
	"context"
	"net/http"
)

// ImageEngine shares the explicit engine probe/transport; it needs no tokenizer.
type ImageEngine interface {
	Info() Info
	Refresh(context.Context) error
	ImageDo(context.Context, []byte) (*http.Response, error)
}
type imageClient struct{ AudioEngine }

func OpenImages(ctx context.Context, url, key string) (ImageEngine, error) {
	a, e := OpenAudio(ctx, url, key)
	if e != nil || a == nil {
		return nil, e
	}
	return &imageClient{a}, nil
}
func (i *imageClient) ImageDo(ctx context.Context, body []byte) (*http.Response, error) {
	return i.AudioDo(ctx, "/v1/images/generations", "application/json", body)
}
