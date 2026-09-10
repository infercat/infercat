package gateway

import (
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/infercat/infercat/internal/adminkey"
	"github.com/infercat/infercat/internal/usage"
)

type consoleBudget struct {
	starts []time.Time
	active int
}
type consoleFailure struct {
	started time.Time
	failed  []time.Time
	active  int
}
type consoleGateway struct {
	failures      map[netip.Addr]*consoleFailure
	now           func() time.Time
	auth          func(string) bool
	logf          func(string, ...any)
	lastLog       time.Time
	store         *adminkey.Store
	address       string
	token         func() string
	rec           usage.Recorder
	client        *http.Client
	mu            sync.Mutex
	reads, writes consoleBudget
}

// Console forwards only the explicit console API to the already-bound loopback listener.
// It never forwards caller headers or exposes the per-run local token to the tunnel.
func Console(store *adminkey.Store, address string, token func() string, rec usage.Recorder, logs ...func(string, ...any)) http.Handler {
	if a, err := netip.ParseAddrPort(address); err != nil || !a.Addr().IsLoopback() {
		address = ""
	}
	logf := func(string, ...any) {}
	if len(logs) > 0 && logs[0] != nil {
		logf = logs[0]
	}
	return &consoleGateway{now: time.Now, auth: store.Authenticate, logf: logf, failures: make(map[netip.Addr]*consoleFailure), store: store, address: address, token: token, rec: rec, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func consolePath(method, path string) bool {
	if path == "/stored" {
		return method == "GET" || method == "DELETE"
	}
	if method == "GET" {
		switch path {
		case "/status", "/keys", "/engine", "/usage", "/settings", "/runs":
			return true
		}
	}
	if path == "/settings" {
		return method == "PATCH"
	}
	if path == "/remote/enable" || path == "/remote/rotate" || path == "/remote/off" {
		return method == "POST"
	}
	if path == "/keys" {
		return method == "POST"
	}
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[1] != "keys" || !strings.HasPrefix(parts[2], "k_") || len(parts[2]) > 64 {
		return false
	}
	for _, c := range parts[2] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	if len(parts) == 3 {
		return method == "GET" || method == "PATCH"
	}
	if len(parts) == 4 && method == "POST" {
		switch parts[3] {
		case "pause", "resume", "revoke", "rotate":
			return true
		}
	}
	return false
}
func (h *consoleGateway) acquire(write bool) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, max, concurrent := &h.reads, 240, 6
	if write {
		b, max, concurrent = &h.writes, 20, 1
	}
	now := time.Now()
	for len(b.starts) > 0 && now.Sub(b.starts[0]) >= time.Minute {
		b.starts = b.starts[1:]
	}
	if len(b.starts) >= max || b.active >= concurrent {
		return nil
	}
	b.starts = append(b.starts, now)
	b.active++
	return func() { h.mu.Lock(); b.active--; h.mu.Unlock() }
}
func (h *consoleGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Second))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Second))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	started := time.Now()
	status := http.StatusOK
	endpoint := "/console/(refused)"
	defer func() {
		if h.rec != nil {
			h.rec.Record(r.Context(), usage.Event{Kind: "console", TS: time.Now().UTC(), Endpoint: endpoint, Status: status, TotalMS: time.Since(started).Milliseconds()})
		}
	}()
	fail := func(code int) { status = code; http.Error(w, http.StatusText(code), code) }
	if h.address == "" || !h.store.State().Enabled {
		fail(404)
		return
	}
	finish := h.reserveAuthentication(r)
	if finish == nil {
		w.Header().Set("Retry-After", "60")
		fail(429)
		return
	}
	secret, ok := bearer(r.Header.Get("Authorization"))
	valid := ok && h.auth(secret)
	finish(valid)
	if !valid {
		fail(401)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/console/api")
	if r.URL.Path == "/console/" {
		path = "/status"
	}
	if r.URL.RawPath != "" || !consolePath(r.Method, path) || !(strings.HasPrefix(r.URL.Path, "/console/api/") || r.URL.Path == "/console/") {
		fail(404)
		return
	}
	endpoint = "/console/api" + path
	release := h.acquire(r.Method != "GET")
	if release == nil {
		w.Header().Set("Retry-After", "60")
		fail(429)
		return
	}
	defer release()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		fail(413)
		return
	}
	// Only the known usage window and run owner filter are relayed; queries are never logged.
	query := ""
	if r.URL.RawQuery != "" {
		q := r.URL.Query()
		v := q.Get("window")
		switch {
		case path == "/usage" && len(q) == 1 && len(q["window"]) == 1 && (v == "today" || v == "week"):
			query = "?window=" + v
		case (path == "/runs" || path == "/stored") && len(q) == 1 && len(q["key_id"]) == 1 && consolePath("GET", "/keys/"+q.Get("key_id")):
			query = "?key_id=" + q.Get("key_id")
		default:
			fail(400)
			return
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, "http://"+h.address+"/api"+path+query, strings.NewReader(string(body)))
	if err != nil {
		fail(502)
		return
	}
	req.Header.Set("Authorization", "Bearer "+h.token())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Infercat-Remote", "true")
	resp, err := h.client.Do(req)
	if err != nil {
		fail(502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		fail(502)
		return
	}
	result, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(result) > 8<<20 {
		fail(502)
		return
	}
	status = resp.StatusCode
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(result)
}

// Reserve before hashing. Success refunds the reservation; failures remain until expiry.
// The trusted tunnel peer survives reconnects; neither ports nor headers define a peer.
func (h *consoleGateway) reserveAuthentication(r *http.Request) func(bool) {
	peer, ok := r.Context().Value(sessionContextKey{}).(netip.Addr)
	if !ok || !peer.IsValid() {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		peer, _ = netip.ParseAddr(host)
	}
	peer = peer.Unmap()
	h.mu.Lock()
	now := h.now()
	for key, b := range h.failures {
		if b.active == 0 && now.Sub(b.started) >= time.Minute {
			delete(h.failures, key)
		}
	}
	b := h.failures[peer]
	if b != nil {
		for len(b.failed) > 0 && now.Sub(b.failed[0]) >= time.Minute {
			b.failed = b.failed[1:]
		}
	}
	if b == nil && len(h.failures) < 4096 {
		b = &consoleFailure{started: now}
		h.failures[peer] = b
	}
	if b == nil || len(b.failed)+b.active >= 30 {
		log := h.lastLog.IsZero() || now.Sub(h.lastLog) >= time.Minute
		if log {
			h.lastLog = now
		}
		h.mu.Unlock()
		if log {
			h.logf("console: authentication failure budget exhausted; requests refused")
		}
		return nil
	}
	b.active++
	h.mu.Unlock()
	return func(valid bool) {
		h.mu.Lock()
		defer h.mu.Unlock()
		b.active--
		if !valid {
			b.failed = append(b.failed, h.now())
			b.started = h.now()
		}
	}
}
