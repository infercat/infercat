package gateway

import (
	"io"
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
type consoleGateway struct {
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
func Console(store *adminkey.Store, address string, token func() string, rec usage.Recorder) http.Handler {
	if a, err := netip.ParseAddrPort(address); err != nil || !a.Addr().IsLoopback() {
		address = ""
	}
	return &consoleGateway{store: store, address: address, token: token, rec: rec, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func consolePath(method, path string) bool {
	if method == "GET" {
		switch path {
		case "/status", "/keys", "/engine", "/usage", "/settings":
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
	secret, ok := bearer(r.Header.Get("Authorization"))
	if !ok || !h.store.Authenticate(secret) {
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
	// Only usage has a query; all other query strings are refused, never relayed or logged.
	query := ""
	if r.URL.RawQuery != "" {
		q := r.URL.Query()
		v := q.Get("window")
		if path != "/usage" || len(q) != 1 || len(q["window"]) != 1 || (v != "today" && v != "week") {
			fail(400)
			return
		}
		query = "?window=" + v
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
