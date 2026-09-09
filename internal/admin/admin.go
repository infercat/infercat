// Package admin is the host's authenticated management surface: a tiny HTTP server on a
// unix socket in the data dir (a loopback port on Windows), and the client the `status`
// subcommand uses. Opt-in remote access forwards only the console whitelist through the gateway.
package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/infercat/infercat/internal/adminkey"
	"github.com/infercat/infercat/internal/bridge"
	"github.com/infercat/infercat/internal/product"
	"github.com/infercat/infercat/internal/usage"
)

// File names inside the data dir.
const (
	SockName  = "admin.sock"  // unix
	PortName  = "admin.port"  // windows fallback
	TokenName = "admin.token" // every platform, per run
)

// ErrNoDaemon means nothing is listening: `serve` is not running for this data dir.
var ErrNoDaemon = errors.New("no running host found for this data dir")

// Status is the admin API's single response (docs/ARCHITECTURE.md §Admin API). Mode is "host"
// for `serve` and "bridge" for a `connect` given a data dir (ticket 029 promise 5): a bridge has
// one session, its local endpoint under Upstream, and no keys.
type Status struct {
	Bridge       *bridge.Status      `json:"bridge,omitempty"`
	Remote       *adminkey.State     `json:"remote,omitempty"`
	Audio        map[string]Upstream `json:"audio,omitempty"`
	ModelsPinned []string            `json:"models_pinned,omitempty"`

	Console  string   `json:"console,omitempty"`
	Product  string   `json:"product"`
	Version  string   `json:"version"`
	UptimeS  int64    `json:"uptime_s"`
	Mode     string   `json:"mode"`
	Name     string   `json:"name"` // the host's display name; for a bridge, the host it reaches
	Tunnel   Tunnel   `json:"tunnel"`
	Upstream Upstream `json:"upstream"`
	Queue    Queue    `json:"queue"`
	Engine   Engine   `json:"engine"`
	Process  Process  `json:"process"`
	Keys     []Key    `json:"keys"`
}

type Tunnel struct {
	Addr     string    `json:"addr"`
	Region   string    `json:"region"`
	Clients  int       `json:"clients"` // open port-80 connections right now
	Sessions []Session `json:"sessions"`
	RxBytes  int64     `json:"rx_bytes"` // over every session: received from friends
	TxBytes  int64     `json:"tx_bytes"` // sent to friends
}

// Session is one client as this process has met it (ticket 029 promise 3). On a host it is
// keyed by the client's tunnel address — tailcat derives it from the client's node key, so it is
// the client's identity — and carries what a host can see: connections, bytes, activity. Path,
// handshake and RTT are the client's to measure (tailcat 0.4.0 keeps WireGuard peer state on the
// client side only), so a host says Path "unknown" and a bridge (connect) fills them in for its
// one session.
type Session struct {
	Key         string    `json:"key"`
	Path        string    `json:"path"` // "direct", "relayed", or "unknown"
	Via         string    `json:"via,omitempty"`
	RTTMS       float64   `json:"rtt_ms,omitempty"`
	HandshakeMS int64     `json:"handshake_ms,omitempty"`
	Conns       int       `json:"conns"`
	RxBytes     int64     `json:"rx_bytes"`
	TxBytes     int64     `json:"tx_bytes"`
	Since       time.Time `json:"since"`
	LastByte    time.Time `json:"last_byte"`
	Active      bool      `json:"active"` // a byte in the last two minutes
}

// Engine is what the queue and the engine's own /metrics say (ticket 029 promise 4). Queue keeps
// the exact now; SlotsPeak is sampled once a second (the queue has no high-water mark and a
// read-only accessor cannot add one), so a burst shorter than that can pass under it. Busy,
// Waiting, MemoryBytes and KVCachePct come from /metrics when the engine offers it (Metrics
// true): llama.cpp with --metrics says busy and waiting and no memory; vLLM says all four.
type Engine struct {
	SlotsPeak   int     `json:"slots_peak_sampled"`
	TokensPerS  float64 `json:"tokens_per_s_1m"` // completion tokens of requests finished in the last minute, over the minute
	Metrics     bool    `json:"metrics"`
	Busy        int     `json:"busy"`
	Waiting     int     `json:"waiting"`
	MemoryBytes int64   `json:"memory_bytes"`
	KVCachePct  float64 `json:"kv_cache_pct"`
}

// Process is the host process for a before/after leak check (028). RSSBytes is 0 where the
// kernel does not publish it without cgo (everything but Linux).
type Process struct {
	Goroutines int    `json:"goroutines"`
	HeapBytes  uint64 `json:"heap_bytes"`
	SysBytes   uint64 `json:"sys_bytes"`
	RSSBytes   int64  `json:"rss_bytes"`
}

type Upstream struct {
	Kind         string    `json:"kind"`
	URL          string    `json:"url"`
	Healthy      bool      `json:"healthy"`
	Since        time.Time `json:"since"` // when healthy last changed (ticket 011: "NOT ANSWERING for Ns")
	ModelContext int       `json:"model_context"`
	Slots        int       `json:"slots"`
}

type Queue struct {
	InFlight int `json:"in_flight"`
	Waiting  int `json:"waiting"`
}

type Key struct {
	Connected   bool      `json:"connected"`
	Sessions    int       `json:"sessions"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	InFlight    int       `json:"in_flight"`
	RPMUsed     int       `json:"rpm_used"`
	TPMUsed     int       `json:"tpm_used"`
	TodayTokens int       `json:"today_tokens"`
	LastSeen    time.Time `json:"last_seen"`
}

// Server is the running admin endpoint.
type Server struct {
	l     net.Listener
	srv   *http.Server
	clean func()
	done  chan struct{} // closed by Close: every /events stream ends
	once  sync.Once
}

// Serve starts the admin endpoint for dataDir. status is called per GET /status; reload is called
// per POST /reload and must make an external edit to keys.json visible at once (ticket 009
// promise 9); events, when non-nil, feeds GET /events (029), one JSON event per line for as long
// as the client reads. All the host's own, over the unix socket only — never the tunnel
// (Protection 1).
func Serve(dataDir string, status func() Status, reload func() error, events *Events, api ...http.Handler) (*Server, error) {
	l, token, clean, err := listen(dataDir)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	authed := func(r *http.Request) bool {
		return token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if events == nil {
			http.Error(w, "this process has no event stream", http.StatusServiceUnavailable)
			return
		}
		ch, stop := events.Subscribe()
		defer stop()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		rc := http.NewResponseController(w)
		_ = rc.Flush()
		enc := json.NewEncoder(w)
		for {
			select {
			case <-r.Context().Done():
				return
			case <-done:
				return
			case e := <-ch:
				if enc.Encode(e) != nil || rc.Flush() != nil {
					return
				}
			}
		}
	})
	mux.HandleFunc("POST /reload", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if reload != nil {
			if err := reload(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s := status()
		s.Product, s.Version = product.Name, product.Version
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(s)
	})
	if len(api) > 0 {
		mux.Handle("/", api[0])
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	s := &Server{l: l, srv: srv, clean: clean, done: done}
	go srv.Serve(l)
	return s, nil
}

// Addr is where the admin endpoint is listening, for the startup banner.
func (s *Server) Addr() string { return s.l.Addr().String() }

// Close stops serving and removes the socket / port files.
func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		close(s.done)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err = s.srv.Shutdown(ctx)
		// Shutdown cannot close a listener whose Serve goroutine has not registered yet.
		// Close it before releasing the path: a late UnixListener.Close would otherwise
		// unlink a successor's socket. Repeated listener closes are safe.
		_ = s.l.Close()
		if s.clean != nil {
			s.clean()
		}
	})
	return err
}

// Reload tells the running host to re-read keys.json now, so a `keys` command's change is in
// force before the command returns instead of within the store's once-per-second poll. No running
// host is not an error to the caller: ErrNoDaemon means there was nothing to tell.
func Reload(ctx context.Context, dataDir string) error {
	hc, base, token, err := dial(dataDir)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/reload", nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return ErrNoDaemon
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return errors.New("admin API: " + resp.Status)
	}
	return nil
}

// Fetch asks the running host for its status. ErrNoDaemon means nothing is listening.
func Fetch(ctx context.Context, dataDir string) (Status, error) {
	var st Status
	hc, base, token, err := dial(dataDir)
	if err != nil {
		return st, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/status", nil)
	if err != nil {
		return st, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return st, ErrNoDaemon
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return st, errors.New("admin API: " + resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return Status{}, err
	}
	return st, nil
}

// Watch tails the running process's event stream, calling fn for each event, until ctx ends or
// the stream does (a host that stopped: nil, so a watcher may wait for it to come back).
// ErrNoDaemon means nothing is listening.
func Watch(ctx context.Context, dataDir string, fn func(usage.Event)) error {
	hc, base, token, err := dial(dataDir)
	if err != nil {
		return err
	}
	hc.Timeout = 0 // a stream, not a call
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events", nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return ErrNoDaemon
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("admin API: " + resp.Status)
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var e usage.Event
		if err := dec.Decode(&e); err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		fn(e)
	}
}
