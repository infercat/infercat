package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The two deadlines the engine owns (docs/archive/DESIGN.md §1.6). Probes, tokenize and lists must never be the
// reason a request hangs; a generation's first byte bounds an engine that accepted a request but
// does not start (its later bytes are the gateway's idle deadline).
const (
	ProbeTimeout     = 3 * time.Second
	FirstByteTimeout = 120 * time.Second
)

// Slotted is implemented by upstreams whose parallel-slot count the host can override
// (`serve --slots N`). The override takes precedence over reported or estimated slots.
type Slotted interface {
	SetSlots(n int)
}

// client is the single Upstream implementation; the Kind selects which probes it runs.
type client struct {
	base *url.URL
	hc   *http.Client // the one client: bearer, no redirects, first-byte deadline

	mu       sync.RWMutex
	info     Info
	setSlots int // host override from --slots; shows once the engine is identified
}

var _ Upstream = (*client)(nil)
var _ Slotted = (*client)(nil)

// bearer adds the upstream API key to every request the engine is sent.
type bearer struct {
	rt  http.RoundTripper
	key string
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Authorization") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+b.key)
	}
	return b.rt.RoundTrip(r)
}

// newClient is the Unknown state: nothing has answered yet, one slot, not OK since now.
func newClient(base *url.URL, apiKey string) *client {
	return newClientWithHeaderTimeout(base, apiKey, FirstByteTimeout)
}

func newClientWithHeaderTimeout(base *url.URL, apiKey string, timeout time.Duration) *client {
	rt := http.DefaultTransport.(*http.Transport).Clone()
	rt.ResponseHeaderTimeout = timeout
	var tr http.RoundTripper = rt
	if apiKey != "" {
		tr = bearer{rt: tr, key: apiKey}
	}
	return &client{
		base: base,
		hc: &http.Client{
			Transport: tr,
			// Never follow a redirect: the bearer rides on every request, and a 3xx would replay
			// it to wherever Location points. The 3xx surfaces as a non-2xx status instead (E5).
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		info: Info{URL: base.String(), Kind: Unknown, Slots: 1, Health: Health{Since: time.Now(), Err: "not probed yet"}},
	}
}

func (c *client) Info() Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	i := c.info
	i.ModelDetails = maps.Clone(c.info.ModelDetails)
	i.Models = append([]string(nil), c.info.Models...)
	if c.info.Vision != nil {
		i.Vision = make(map[string]*bool, len(c.info.Vision))
		for id, vision := range c.info.Vision {
			i.Vision[id] = nil
			if vision != nil {
				v := *vision
				i.Vision[id] = &v
			}
		}
	}
	return i
}

// SetSlots records the host's --slots override. It shows in Info once an engine is identified:
// an Unknown engine has one slot (E1).
func (c *client) SetSlots(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setSlots = n
	if n > 0 && c.info.Kind != Unknown {
		c.info.Slots = n
	}
}

// Do implements Engine.Do. A GET is a list, never a generation, so the probe deadline bounds it
// (cancelled when the caller closes the body); a POST's first byte is bounded by the transport.
func (c *client) Do(ctx context.Context, method, path string, body []byte, stream bool) (*http.Response, error) {
	cancel := context.CancelFunc(func() {})
	if method == http.MethodGet {
		ctx, cancel = context.WithTimeout(ctx, ProbeTimeout)
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, rdr)
	if err != nil {
		cancel()
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose ends a GET's probe deadline when the caller is done with the body.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelOnClose) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}

// Open dials an explicit upstream URL and probes it once. It returns a usable Upstream even when
// the engine is down (Unknown, not answering): `serve` warns and keeps probing.
func Open(ctx context.Context, rawURL, apiKey string) (Upstream, error) {
	base, err := normalize(rawURL)
	if err != nil {
		return nil, err
	}
	c := newClient(base, apiKey)
	_ = c.Refresh(ctx)
	return c, nil
}

// candidates are probed in the order docs/ARCHITECTURE.md fixes; the kind is what usually lives at
// that port and is documentation only — what answers is sniffed from the server, never assumed.
var candidates = []struct {
	url  string
	kind Kind
}{
	{"http://127.0.0.1:8080", LlamaCPP},
	{"http://127.0.0.1:11434", Ollama},
	{"http://127.0.0.1:1234", LMStudio},
	{"http://127.0.0.1:8000", VLLM},
}

// ErrNoUpstream is returned when nothing answers on any known port.
var ErrNoUpstream = errors.New("no local inference server found on 127.0.0.1 ports 8080 (llama.cpp/llama-swap), 11434 (Ollama), 1234 (LM Studio), 8000 (vLLM); pass --upstream URL")

// Detect probes the known local engines in the contract's order and returns the first that
// reaches OK. apiKey, when set, rides on every probe so an engine behind a bearer token is found
// instead of being reported as absent (005 fix 10n).
func Detect(ctx context.Context, apiKey string) (Upstream, error) {
	for _, cand := range candidates {
		base, err := normalize(cand.url)
		if err != nil {
			continue
		}
		c := newClient(base, apiKey)
		if c.Refresh(ctx) == nil {
			return c, nil
		}
	}
	return nil, ErrNoUpstream
}

// normalize turns a user-supplied URL into a scheme://host base with any trailing "/" or "/v1"
// removed, so both `http://host:8000` and `http://host:8000/v1` work.
func normalize(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty upstream URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("upstream URL %q: %w", raw, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("upstream URL %q has no host", raw)
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1"), "/")
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

// getJSON does a GET under the probe deadline and decodes JSON into v. A non-2xx is an error.
func (c *client) getJSON(ctx context.Context, path string, v any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, v)
}

func (c *client) doJSON(ctx context.Context, method, path string, body any, v any) error {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)); resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if v == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}
