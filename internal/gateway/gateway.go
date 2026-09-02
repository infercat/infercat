// Package gateway is the http.Handler that sits between the tunnel listener and the upstream engine:
// bearer auth against the key store, per-key limits, a global queue sized to the engine's slots,
// request clamps, a streaming reverse proxy, OpenAI-shaped errors, and one usage event per request.
// It exposes exactly one thing to the tunnel (Protection 1) and never logs or stores a secret
// (Protection 2).
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/upstream"
	"github.com/2185Lab/bunny-network/internal/usage"
)

// Config is everything the gateway needs beyond its collaborators. Zero values take the defaults
// in docs/ARCHITECTURE.md: Slots 1, QueueTimeout 30 s, RequestTimeout 300 s, MaxBody 4 MiB.
type Config struct {
	Slots          int           // global concurrency around the upstream call (= engine slots)
	QueueTimeout   time.Duration // bounded wait for a global slot, then 503 queue_timeout
	RequestTimeout time.Duration // upstream call timeout (covers the whole stream)
	MaxBody        int64         // request body cap, bytes; over it → 413 body_too_large
	LogPrompts     bool          // put prompt/completion text into usage events (Protection 3: opt-in)
	HostName       string        // shown in /me
	RelayRegion    func() string // shown in /me; nil → ""
}

const (
	retryAfterUpstreamDown = 10 // seconds; matches the CLI's 10 s upstream health poll
	retryAfterQueueTimeout = 5  // seconds; the friend already waited QueueTimeout
	defaultQueueTimeout    = 30 * time.Second
	defaultRequestTimeout  = 300 * time.Second
	defaultMaxBody         = 4 << 20
	defaultReadTimeout     = 30 * time.Second // whole request body, from handler entry (006 promise 4)
	defaultWriteTimeout    = 60 * time.Second // any single write or flush to the friend (006 promise 3)
	maxEndpointLen         = 64               // usage.Event.Endpoint is the request path: bounded (006 promise 7)
)

// Gateway serves the API on any number of listeners (the tunnel, and loopback in dev mode) and
// implements usage.Snapshot for the admin status API.
type Gateway struct {
	cfg   Config
	up    upstream.Upstream
	store keys.Store
	rec   usage.Recorder
	logf  func(string, ...any)
	lim   *limiter

	semMu      sync.Mutex    // guards sem, which SetSlots swaps
	sem        chan struct{} // global slots; cap(sem) = slots
	inFlight   atomic.Int32  // holders of a global slot (on any generation of sem)
	waiting    atomic.Int32  // goroutines blocked on sem
	maxWaiting atomic.Int32  // max(2, 2×slots) may wait at once (006 promise 10); follows SetSlots
	bodies     atomic.Int32  // request bodies held in memory (per-key slots bound it; tests read it)

	readTimeout  time.Duration // unexported: tests shorten them, hosts get the defaults
	writeTimeout time.Duration

	mu      sync.Mutex
	servers []*http.Server
	closed  bool
}

// The CLI (cmd/bunny-network, ticket 003) wires the gateway through exactly this interface.
var _ interface {
	usage.Snapshot
	Serve(l net.Listener) error
	ServeDev(addr string) error
	Shutdown(ctx context.Context) error
	SetSlots(n int)
} = (*Gateway)(nil)

// New builds a gateway. logf may be nil. Nothing is listening until Serve or ServeDev is called.
func New(cfg Config, up upstream.Upstream, store keys.Store, rec usage.Recorder, logf func(string, ...any)) *Gateway {
	if cfg.Slots <= 0 {
		cfg.Slots = 1
	}
	if cfg.QueueTimeout <= 0 {
		cfg.QueueTimeout = defaultQueueTimeout
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = defaultMaxBody
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	g := &Gateway{
		cfg:   cfg,
		up:    up,
		store: store,
		rec:   rec,
		logf:  logf,
		lim:   newLimiter(),
		sem:   make(chan struct{}, cfg.Slots),

		readTimeout:  defaultReadTimeout,
		writeTimeout: defaultWriteTimeout,
	}
	g.maxWaiting.Store(waitCap(cfg.Slots))
	return g
}

// waitCap is how many requests may wait for a global slot at once: max(2, 2×slots).
func waitCap(slots int) int32 { return int32(max(2, 2*slots)) }

// Handler is the routed API without CORS. Serve wraps it in an http.Server; use it directly only in tests.
func (g *Gateway) Handler() http.Handler { return http.HandlerFunc(g.serveHTTP) }

// Serve blocks serving l (the tunnel listener) until l fails or Shutdown is called. It returns nil
// after Shutdown, the listener's error otherwise, and http.ErrServerClosed if called after Shutdown.
func (g *Gateway) Serve(l net.Listener) error { return g.serve(l, g.Handler()) }

// ServeDev serves the API on a loopback TCP address with permissive CORS (origin *, headers
// authorization and content-type, preflight answered) so the web app can be developed against real
// fetch. Any non-loopback address is refused before anything listens (Protection 1). Blocks like Serve.
func (g *Gateway) ServeDev(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("dev listen %q: %w", addr, err)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("dev listen %q refused: only loopback addresses (127.0.0.1 or ::1) are allowed", addr)
	}
	l, err := net.Listen("tcp", net.JoinHostPort(ip.String(), port))
	if err != nil {
		return err
	}
	g.logf("gateway: dev listener on http://%s (permissive CORS, loopback only)", l.Addr())
	return g.serve(l, cors(g.Handler()))
}

func (g *Gateway) serve(l net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          log.New(logWriter{g.logf}, "gateway: http: ", 0),
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		_ = l.Close()
		return http.ErrServerClosed
	}
	g.servers = append(g.servers, srv)
	g.mu.Unlock()
	err := srv.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops accepting, drains in-flight requests until ctx expires, then force-closes what is
// left (so a caller's "drain up to 10 s" is one call). Returns the first drain error, if any.
func (g *Gateway) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	g.closed = true
	servers := g.servers
	g.servers = nil
	g.mu.Unlock()
	var first error
	for _, s := range servers {
		if err := s.Shutdown(ctx); err != nil {
			if first == nil {
				first = err
			}
			_ = s.Close()
		}
	}
	return first
}

// Counters implements usage.Snapshot.
func (g *Gateway) Counters(keyID string) usage.KeyCounters { return g.lim.counters(keyID) }

// AllCounters implements usage.Snapshot.
func (g *Gateway) AllCounters() map[string]usage.KeyCounters { return g.lim.allCounters() }

// Queue implements usage.Snapshot: holders of a global slot, and goroutines waiting for one.
func (g *Gateway) Queue() (inFlight, waiting int) {
	return int(g.inFlight.Load()), int(g.waiting.Load())
}

// SetSlots resizes the global queue to the engine's slot count (ticket 005 fix 10d: an engine
// that is down at New reports its real count only after a later Refresh). New arrivals use the
// new size at once; requests already holding a slot finish and release on the old one.
func (g *Gateway) SetSlots(n int) {
	if n <= 0 {
		n = 1
	}
	g.semMu.Lock()
	defer g.semMu.Unlock()
	if n == cap(g.sem) {
		return
	}
	g.cfg.Slots = n
	g.sem = make(chan struct{}, n)
	g.maxWaiting.Store(waitCap(n))
}

// acquire takes a global slot, waiting at most QueueTimeout. The returned func releases it. At most
// maxWaiting requests wait at once (each holds a body, a goroutine, and a per-key slot); one more is
// refused on the spot with the same 503 queue_timeout, so a burst degrades to fast 503s with
// Retry-After instead of a growing set of parked bodies (Protection 4).
func (g *Gateway) acquire(ctx context.Context) (func(), *gwError) {
	g.semMu.Lock()
	sem := g.sem
	g.semMu.Unlock()
	release := func() { g.inFlight.Add(-1); <-sem }
	select {
	case sem <- struct{}{}:
		g.inFlight.Add(1)
		return release, nil
	default:
	}
	if n := g.waiting.Add(1); n > g.maxWaiting.Load() {
		g.waiting.Add(-1)
		return nil, errf(CodeQueueTimeout, retryAfterQueueTimeout, "the host's engine is busy; %d requests already waiting", n-1)
	}
	defer g.waiting.Add(-1)
	t := time.NewTimer(g.cfg.QueueTimeout)
	defer t.Stop()
	select {
	case sem <- struct{}{}:
		g.inFlight.Add(1)
		return release, nil
	case <-t.C:
		return nil, errf(CodeQueueTimeout, retryAfterQueueTimeout, "the host's engine is busy; waited %s for a free slot", g.cfg.QueueTimeout)
	case <-ctx.Done():
		return nil, errf(CodeClientClosed, 0, "client went away while queued")
	}
}

// call is one request's state: the writer, the key, and the usage event being assembled.
type call struct {
	g           *Gateway
	w           http.ResponseWriter
	r           *http.Request
	start       time.Time
	key         *keys.Key
	ev          usage.Event
	wroteHeader bool
	ttftSet     bool
	noEvent     bool // unauthenticated: nothing to record (promise 7b)
	stalled     bool // the body read deadline fired: r.Context is cancelled by net/http, the friend is still there
}

func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		// A body is coming (declared, or -1 = chunked): bound how long it may take from this moment,
		// valid key or not. This also bounds net/http's post-handler discard of a body that a rejected
		// request never read. readBody clears it once the body is in hand.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(g.readTimeout))
	}
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "11")
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	c := &call{g: g, w: w, r: r, start: time.Now()}
	c.ev.TS = c.start
	c.ev.Endpoint = r.URL.Path
	defer c.finish()

	key, gerr := g.authenticate(r)
	if key != nil { // active, paused, or revoked: the event names the key and last_seen moves (promise 7a)
		c.key = key
		c.ev.KeyID = key.ID
		g.lim.touch(key.ID)
	}
	if gerr != nil {
		c.noEvent = gerr.Code == CodeInvalidKey
		c.fail(gerr)
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/me":
		c.me()
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		c.models()
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		c.chat()
	case r.Method == http.MethodPost && r.URL.Path == "/v1/embeddings":
		c.embeddings()
	default:
		c.fail(errf(CodeNotFound, 0, "no route for %s %s", r.Method, r.URL.Path))
	}
}

// authenticate resolves the bearer secret through the store on every request (no caching: hot
// reload is the store's job). The secret is never logged, never echoed, never put in an event. A
// paused or revoked key is returned alongside its error so the rejection is recorded against it.
func (g *Gateway) authenticate(r *http.Request) (*keys.Key, *gwError) {
	secret, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		return nil, errf(CodeInvalidKey, 0, "missing or malformed Authorization header; expected: Bearer <invite secret>")
	}
	k, found, err := g.store.Lookup(r.Context(), secret)
	if err != nil {
		g.logf("gateway: key store lookup failed: %v", err)
		return nil, errf(CodeUpstreamDown, 1, "the host's key store is unavailable; try again")
	}
	if !found {
		return nil, errf(CodeInvalidKey, 0, "unknown key; check the invite")
	}
	switch k.Status {
	case keys.Active:
		return k, nil
	case keys.Revoked:
		return k, errf(CodeKeyRevoked, 0, "this key has been revoked by the host")
	default: // Paused, or anything unexpected: fail closed as paused
		return k, errf(CodeKeyPaused, 0, "this key is paused by the host")
	}
}

func bearer(h string) (string, bool) {
	parts := strings.Fields(h)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

// finish records the usage event. It runs after every request, including rejected ones — except
// 401s: an unauthenticated request has no key to act on, and its path is attacker-chosen noise the
// host cannot use (promise 7b). Endpoint is capped so a keyed friend's 404s cannot inflate the log.
func (c *call) finish() {
	if c.noEvent {
		return
	}
	c.ev.TotalMS = time.Since(c.start).Milliseconds()
	if len(c.ev.Endpoint) > maxEndpointLen {
		c.ev.Endpoint = c.ev.Endpoint[:maxEndpointLen]
	}
	if c.g.rec != nil {
		c.g.rec.Record(context.WithoutCancel(c.r.Context()), c.ev)
	}
}

// fail writes e as the response: a full error response if nothing was written yet, an SSE error
// event if a stream is under way, or nothing if the friend has already gone (recorded as 499).
func (c *call) fail(e *gwError) {
	if c.wroteHeader {
		c.ev.Code = string(e.Code)
		if c.r.Context().Err() == nil {
			writeStreamError(c.w, e)
		}
		return
	}
	if e.Code == CodeClientClosed || (c.r.Context().Err() != nil && !c.stalled) {
		c.ev.Status, c.ev.Code = 499, string(CodeClientClosed)
		return
	}
	c.ev.Status, c.ev.Code = e.Status(), string(e.Code)
	c.armWrite()
	writeError(c.w, e)
}

func (c *call) writeHeader(status int) {
	c.wroteHeader = true
	c.ev.Status = status
	c.armWrite()
	c.w.WriteHeader(status)
}

// armWrite gives the next write or flush to the friend writeTimeout to complete (promise 3). Called
// before every response and re-armed per stream line, so a friend who stops reading fails the write
// and frees the goroutine, the per-key slot, and the global slot; a friend who keeps reading is never
// cut. net/http clears the deadline when the handler returns.
func (c *call) armWrite() {
	_ = http.NewResponseController(c.w).SetWriteDeadline(time.Now().Add(c.g.writeTimeout))
}

func (c *call) markTTFT() {
	if !c.ttftSet {
		c.ttftSet = true
		c.ev.TTFTMS = time.Since(c.start).Milliseconds()
	}
}

// cors wraps h for dev mode: any origin, the two headers the web app sends, preflight answered
// without auth, and Retry-After exposed so the app can show a countdown.
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Access-Control-Allow-Origin", "*")
		hd.Set("Access-Control-Allow-Headers", "authorization, content-type")
		hd.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		hd.Set("Access-Control-Expose-Headers", "Retry-After")
		if r.Method == http.MethodOptions {
			hd.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

type logWriter struct{ f func(string, ...any) }

func (l logWriter) Write(p []byte) (int, error) {
	l.f("%s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
