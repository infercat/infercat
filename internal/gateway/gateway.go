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
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

// Config is everything the gateway needs beyond its collaborators. The queue's capacity is not
// here: it is the engine's slot count, read live (DESIGN §1.5). Deadlines and the body cap are
// constants (§1.6), each bounding one party's failure.
type Config struct {
	RemoteConsole                http.Handler
	LiveHostName                 func() string
	Transcribe, Speech           upstream.AudioEngine
	TranscribeModel, SpeechModel string
	MaxTranscriptionSeconds      float64
	ModelsPinned                 []string      // host-wide model allowlist; empty means all models
	LogPrompts                   bool          // put prompt/completion text into usage events (Protection 3: opt-in)
	HostName                     string        // shown in /me
	RelayRegion                  func() string // shown in /me; nil → ""
	// DataDir is where usage.jsonl lives; New reads it once to seed today's per-key counters.
	// Empty means no history to seed from, and the counters start at zero as they always did.
	DataDir string
}

const (
	retryAfterUpstreamDown = 10 // seconds; matches the CLI's 10 s upstream health poll
	retryAfterQueueTimeout = 5  // seconds; the friend already waited the queue timeout

	// DESIGN §1.6: one owner each, no general "request timeout".
	defaultQueueTimeout = 30 * time.Second // an engine that is full: wait for a slot, then 503 queue_timeout
	defaultReadTimeout  = 30 * time.Second // a client that stalls its body: from handler entry to body in hand
	defaultWriteTimeout = 60 * time.Second // a client that stops reading: any single write or flush
	defaultIdleTimeout  = 60 * time.Second // an engine that stalls mid-stream: re-armed per read
	defaultMaxBody      = 4 << 20          // request body cap, bytes; over it → 413 body_too_large
	defaultQueuedEvery  = 5 * time.Second  // a streaming request waiting for a slot says `: queued` this often (018)
	maxEndpointLen      = 64               // usage.Event.Endpoint is the request path: bounded (006 promise 7)
	// The engine's first byte (an engine that accepted a request but does not start) is the
	// engine's own deadline, upstream.FirstByteTimeout, behind Engine.Do (DESIGN §3.4).
)

// Gateway serves the API on any number of listeners (the tunnel, and loopback in dev mode) and
// implements usage.Snapshot for the admin status API.
type Gateway struct {
	audioHistoryErr error
	cfg             Config
	up              upstream.Engine // the gateway's whole view of the engine (DESIGN §3.4)
	store           keys.Store
	rec             usage.Recorder
	logf            func(string, ...any)
	lim             *limiter
	queue           slotQueue    // global slots; capacity = up.Info().Slots, read at every decision
	bodies          atomic.Int32 // request bodies held in memory (per-key slots bound it; tests read it)

	// The deadlines, the keepalive and the body cap, unexported: tests shorten them, hosts get the constants.
	queueTimeout, readTimeout, writeTimeout, idleTimeout, queuedEvery time.Duration
	maxBody                                                           int64

	mu       sync.Mutex
	sessions map[netip.Addr]sessionSeen
	servers  []*http.Server
	closed   bool
}

// The CLI (cmd/infercat, ticket 003) wires the gateway through exactly this interface.
var _ interface {
	usage.Snapshot
	Serve(l net.Listener) error
	ServeDev(addr string) error
	Shutdown(ctx context.Context) error
} = (*Gateway)(nil)

// New builds a gateway. logf may be nil. Nothing is listening until Serve or ServeDev is called.
func New(cfg Config, up upstream.Engine, store keys.Store, rec usage.Recorder, logf func(string, ...any)) *Gateway {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if cfg.MaxTranscriptionSeconds <= 0 {
		cfg.MaxTranscriptionSeconds = 300
	}
	g := &Gateway{
		cfg:   cfg,
		up:    up,
		store: store,
		rec:   rec,
		logf:  logf,
		lim:   newLimiter(),
		queue: slotQueue{cap: func() int { return up.Info().Slots }},

		queueTimeout: defaultQueueTimeout,
		readTimeout:  defaultReadTimeout,
		writeTimeout: defaultWriteTimeout,
		idleTimeout:  defaultIdleTimeout,
		queuedEvery:  defaultQueuedEvery,
		maxBody:      defaultMaxBody,
	}
	g.seedCounters()
	return g
}

// seedCounters gives the limiter today's history before anything is served: one Aggregate over
// usage.jsonl since UTC midnight (DESIGN §4 item 5), no new file and no second lane of truth.
// History that cannot be read is logged and skipped — a host must still start, and the only cost
// is the pre-restart limitation, counters from zero.
func (g *Gateway) seedCounters() {
	if g.cfg.DataDir == "" {
		return
	}
	day := g.lim.now().UTC().Truncate(24 * time.Hour)
	rep, err := usage.AggregateFile(g.cfg.DataDir, usage.Filter{Since: day})
	if err != nil {
		g.audioHistoryErr = err
		g.logf("gateway: today's usage history is unreadable (%v); per-key counters start at zero", err)
		return
	}
	if rep.Malformed > 0 {
		g.audioHistoryErr = fmt.Errorf("usage history has %d malformed rows", rep.Malformed)
		g.logf("audio refused: %v", g.audioHistoryErr)
	}
	g.lim.seedToday(rep, day)
}

// Handler is the routed API without CORS. Serve wraps it in an http.Server; use it directly only in tests.
func (g *Gateway) Handler() http.Handler { return http.HandlerFunc(g.serveHTTP) }

// Serve blocks serving l (the tunnel listener) until l fails or Shutdown is called. It returns nil
// after Shutdown, the listener's error otherwise, and http.ErrServerClosed if called after Shutdown.
func (g *Gateway) Serve(l net.Listener) error { return g.serve(l, g.Handler(), sessionContext) }

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
	return g.serve(l, cors(g.Handler()), nil)
}

func (g *Gateway) serve(l net.Listener, h http.Handler, connContext func(context.Context, net.Conn) context.Context) error {
	srv := &http.Server{
		Handler:           h,
		ConnContext:       connContext,
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

// Queue implements usage.Snapshot: holders of a global slot, and requests waiting for one — read
// from the queue itself, exact.
func (g *Gateway) Queue() (inFlight, waiting int) { return g.queue.counts() }

func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/console" || strings.HasPrefix(r.URL.Path, "/console/") {
		if g.cfg.RemoteConsole == nil {
			http.NotFound(w, r)
		} else {
			g.cfg.RemoteConsole.ServeHTTP(w, r)
		}
		return
	}
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
