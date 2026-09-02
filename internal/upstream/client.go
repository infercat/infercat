package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// probeTimeout caps every detection, sniff, refresh, and tokenize call. Probes must never be the
// reason a request hangs.
const probeTimeout = 3 * time.Second

// Slotted is implemented by upstreams whose parallel-slot count the host can override
// (`serve --slots N`). Engines that report their own slot count ignore the override.
type Slotted interface {
	SetSlots(n int)
}

// client is the single Upstream implementation; the Kind selects which probes it runs.
type client struct {
	base   *url.URL
	apiKey string
	hc     *http.Client
	rt     http.RoundTripper

	mu       sync.RWMutex
	info     Info
	setSlots int // host override from --slots; 0 = engine's own answer
}

var _ Upstream = (*client)(nil)
var _ Slotted = (*client)(nil)

// bearer adds the upstream API key to every proxied request.
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

func newClient(base *url.URL, kind Kind, apiKey string) *client {
	rt := http.DefaultTransport.(*http.Transport).Clone()
	rt.ResponseHeaderTimeout = 0 // streaming responses hold headers open; the gateway times out
	var tr http.RoundTripper = rt
	if apiKey != "" {
		tr = bearer{rt: tr, key: apiKey}
	}
	return &client{
		base:   base,
		apiKey: apiKey,
		hc:     &http.Client{Transport: tr},
		rt:     tr,
		info:   Info{Kind: kind, URL: base.String(), Slots: defaultSlots(kind)},
	}
}

func defaultSlots(k Kind) int {
	if k == VLLM {
		return 2 // docs/ARCHITECTURE.md: vLLM does not report slots; --slots overrides
	}
	return 1
}

func (c *client) BaseURL() *url.URL            { u := *c.base; return &u }
func (c *client) Transport() http.RoundTripper { return c.rt }

func (c *client) Info() Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	i := c.info
	i.Models = append([]string(nil), c.info.Models...)
	return i
}

func (c *client) SetSlots(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setSlots = n
	if n > 0 {
		c.info.Slots = n
	}
}

// Open dials an explicit upstream URL and sniffs which engine answers there. It returns a usable
// Upstream even when the engine is down: `serve` warns and keeps polling Refresh.
func Open(ctx context.Context, rawURL, apiKey string) (Upstream, error) {
	base, err := normalize(rawURL)
	if err != nil {
		return nil, err
	}
	probe := newClient(base, Generic, apiKey)
	kind, _ := sniff(ctx, probe)
	c := newClient(base, kind, apiKey)
	_ = c.Refresh(ctx)
	return c, nil
}

// candidates are probed in the order docs/ARCHITECTURE.md fixes.
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
var ErrNoUpstream = errors.New("no local inference server found on 127.0.0.1 ports 8080 (llama.cpp), 11434 (Ollama), 1234 (LM Studio), 8000 (vLLM); pass --upstream URL")

// Detect probes the known local engines in the contract's order and returns the first that
// answers. The kind is sniffed from the server, not assumed from the port.
func Detect(ctx context.Context) (Upstream, error) {
	for _, cand := range candidates {
		base, err := normalize(cand.url)
		if err != nil {
			continue
		}
		probe := newClient(base, cand.kind, "")
		kind, ok := sniff(ctx, probe)
		if !ok {
			continue
		}
		c := newClient(base, kind, "")
		if err := c.Refresh(ctx); err != nil {
			continue
		}
		return c, nil
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

// getJSON does a 3-second GET and decodes JSON into v. A non-2xx is an error.
func (c *client) getJSON(ctx context.Context, path string, v any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, v)
}

func (c *client) doJSON(ctx context.Context, method, path string, body any, v any) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
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
