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

func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		// A body is coming (declared, or -1 = chunked): bound how long it may take from this moment,
		// valid key or not (promise 4). This also bounds net/http's post-handler discard of a body that
		// a rejected request never read. readBody clears it once the body is in hand.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(g.readTimeout))
	}
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "11")
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	q := g.newRequest(w, r)
	defer q.finish() // the one exit: releases whatever the request took, records the event
	q.serve()
}

func bearer(h string) (string, bool) {
	parts := strings.Fields(h)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
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
