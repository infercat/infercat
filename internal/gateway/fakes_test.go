package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/upstream"
	"github.com/2185Lab/bunny-network/internal/usage"
)

// The one secret every test uses, so TestMain can prove it never reached a log line or a body.
const testSecret = "SECRET-xK9v2QwLp7Rt4Yz8Nb3Mc6Hf1Jd5Sg0Vq"

var (
	globalLogs  logBuf
	seenCodesMu sync.Mutex
	seenCodes   = map[Code]bool{}
)

func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 && flag.Lookup("test.run").Value.String() == "" {
		if strings.Contains(globalLogs.String(), testSecret) {
			fmt.Fprintln(os.Stderr, "FAIL: the secret appeared in log output")
			code = 1
		}
		var missing []string
		for c := range codeTable {
			if !seenCodes[c] {
				missing = append(missing, string(c))
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "FAIL: error codes never exercised: %v\n", missing)
			code = 1
		} else {
			fmt.Printf("all %d error codes exercised; %d log lines captured, secret absent\n", len(codeTable), globalLogs.Lines())
		}
	}
	os.Exit(code)
}

type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
	n  int
}

func (l *logBuf) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.b, format+"\n", args...)
	l.n++
}
func (l *logBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }
func (l *logBuf) Lines() int     { l.mu.Lock(); defer l.mu.Unlock(); return l.n }

// ---- keys.Store ----

type fakeStore struct {
	mu   sync.Mutex
	keys map[string]*keys.Key // secret → key
	err  error
}

func (s *fakeStore) Lookup(_ context.Context, secret string) (*keys.Key, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, false, s.err
	}
	k, ok := s.keys[secret]
	if !ok {
		return nil, false, nil
	}
	cp := *k
	return &cp, true, nil
}

func (s *fakeStore) List(context.Context) ([]*keys.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*keys.Key
	for _, k := range s.keys {
		out = append(out, k)
	}
	return out, nil
}

func (s *fakeStore) set(secret string, k *keys.Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k.SecretHash = keys.HashSecret(secret)
	s.keys[secret] = k
}

// ---- usage.Recorder ----

type fakeRecorder struct {
	mu     sync.Mutex
	events []usage.Event
}

func (r *fakeRecorder) Record(_ context.Context, e usage.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

// waitFor blocks until at least n events are recorded (Record runs after the response is written).
func (r *fakeRecorder) waitFor(t *testing.T, n int) []usage.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		r.mu.Lock()
		if len(r.events) >= n {
			out := append([]usage.Event(nil), r.events...)
			r.mu.Unlock()
			return out
		}
		r.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d usage events", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *fakeRecorder) last(t *testing.T) usage.Event {
	t.Helper()
	ev := r.waitFor(t, 1)
	return ev[len(ev)-1]
}

// ---- upstream.Engine backed by an httptest engine ----

// E4 (DESIGN §3.5), at compile time: the gateway is built against Engine alone — this fake has
// Info, CountTokens and Do and nothing else, and every gateway test compiles against it.
var _ upstream.Engine = (*fakeUpstream)(nil)

type fakeUpstream struct {
	srv  *httptest.Server
	base *url.URL     // where Do sends requests; setBase changes it (a dead engine, an engine back)
	hc   *http.Client // no redirects; firstByte swaps in a transport with a header deadline

	mu         sync.Mutex
	info       upstream.Info
	countErr   error
	countGate  chan struct{} // when set, CountTokens blocks until it is closed
	countPanic bool          // when set, CountTokens panics (the handler's panic path)
	mode       string        // sse | json | usage | 500 | garbage | hang | redirect | status:NNN
	events     []string      // sse: data payloads; json and status:NNN: events[0] is the body
	gap        time.Duration // sse: between events
	stallAfter int           // sse: after this many events the engine sends nothing until cancelled; -1 = never
	delay      time.Duration // json: headers at once, then this long before the body (or until cancelled)

	// observations
	started   chan struct{} // closed when the first request reaches the handler
	cancelled chan struct{} // closed when a handler sees its context cancelled
	finished  atomic.Bool   // set when an sse handler wrote [DONE]
	wrote     bytes.Buffer  // exact bytes an sse handler wrote
	lastBody  []byte
	lastPath  string
	lastAuth  string
	requests  atomic.Int32
	tags      []string     // body["user"] of every proxied request, in arrival order
	landed    atomic.Int32 // requests that reached /landed, the redirect target
	countNow  atomic.Int32 // CountTokens calls in progress
	countMax  atomic.Int32 // the most at once
	startOnce sync.Once
	cancOnce  sync.Once
}

func newFakeUpstream() *fakeUpstream {
	f := &fakeUpstream{
		info:       upstream.Info{Kind: upstream.LlamaCPP, Health: upstream.Health{OK: true}, Slots: 1, Models: []string{"m1", "m2", "m3"}},
		hc:         noRedirectClient(0),
		mode:       "json",
		stallAfter: -1,
		events:     []string{`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi there"}}],"usage":{"prompt_tokens":4,"completion_tokens":4}}`},
		gap:        50 * time.Millisecond,
		started:    make(chan struct{}),
		cancelled:  make(chan struct{}),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	f.base, _ = url.Parse(f.srv.URL)
	f.info.URL = f.srv.URL
	return f
}

func (f *fakeUpstream) handle(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	body, _ := io.ReadAll(r.Body)
	var sent map[string]any
	_ = json.Unmarshal(body, &sent)
	f.mu.Lock()
	f.lastBody, f.lastPath, f.lastAuth = body, r.URL.Path, r.Header.Get("Authorization")
	if tag, _ := sent["user"].(string); tag != "" {
		f.tags = append(f.tags, tag)
	}
	mode, events, gap, stallAfter, delay := f.mode, f.events, f.gap, f.stallAfter, f.delay
	f.mu.Unlock()
	f.startOnce.Do(func() { close(f.started) })

	if r.URL.Path == "/v1/models" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1","object":"model"},{"id":"m2","object":"model"},{"id":"m3","object":"model"}]}`))
		return
	}
	if r.URL.Path == "/landed" {
		f.landed.Add(1)
		_, _ = w.Write([]byte(`{"choices":[]}`))
		return
	}
	switch mode {
	case "redirect":
		http.Redirect(w, r, "/landed", http.StatusFound)
	case "500":
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":{"message":"engine exploded"}}`))
	case "garbage":
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>this is not json"))
	case "hang":
		<-r.Context().Done()
		f.cancOnce.Do(func() { close(f.cancelled) })
	default: // status:NNN with events[0] as the body
		var code int
		if _, err := fmt.Sscanf(mode, "status:%d", &code); err != nil {
			panic("fake upstream: unknown mode " + mode)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(events[0]))
	case "json":
		w.Header().Set("Content-Type", "application/json")
		if delay > 0 {
			w.WriteHeader(200)
			_ = http.NewResponseController(w).Flush()
			select {
			case <-r.Context().Done():
				f.cancOnce.Do(func() { close(f.cancelled) })
				return
			case <-time.After(delay):
			}
		}
		_, _ = w.Write([]byte(events[0]))
	case "usage":
		// An honest engine: prompt_tokens = the words it was sent, completion_tokens ≤ max_tokens.
		words := len(strings.Fields(messagesText(sent["messages"])))
		maxTok, _ := sent["max_tokens"].(float64)
		done := rand.Intn(int(maxTok) + 1)
		time.Sleep(time.Duration(rand.Intn(3)) * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":%d,"completion_tokens":%d}}`, words, done)
	case "sse":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		rc := http.NewResponseController(w)
		_ = rc.Flush() // headers out at once, as a real engine's are
		write := func(s string) {
			f.mu.Lock()
			f.wrote.WriteString(s)
			f.mu.Unlock()
			_, _ = io.WriteString(w, s)
			_ = rc.Flush()
		}
		for i, e := range events {
			if i == stallAfter {
				<-r.Context().Done()
				f.cancOnce.Do(func() { close(f.cancelled) })
				return
			}
			write("data: " + e + "\n\n")
			select {
			case <-r.Context().Done():
				f.cancOnce.Do(func() { close(f.cancelled) })
				return
			case <-time.After(gap):
			}
		}
		write("data: [DONE]\n\n")
		f.finished.Store(true)
	}
}

func (f *fakeUpstream) Info() upstream.Info { f.mu.Lock(); defer f.mu.Unlock(); return f.info }

// Do mirrors the real engine's (upstream/client.go): the request is built from method, path and
// body alone — the friend's headers cannot reach it — sent by a client that never follows a
// redirect, with a first-byte deadline when firstByte set one.
func (f *fakeUpstream) Do(ctx context.Context, method, path string, body []byte, stream bool) (*http.Response, error) {
	f.mu.Lock()
	hc, base := f.hc, f.base
	f.mu.Unlock()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base.String()+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return hc.Do(req)
}

func noRedirectClient(firstByte time.Duration) *http.Client {
	return &http.Client{
		Transport:     &http.Transport{ResponseHeaderTimeout: firstByte},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// firstByte is the engine's first-byte deadline (upstream.FirstByteTimeout), shortened for a test.
func (f *fakeUpstream) firstByte(d time.Duration) {
	f.mu.Lock()
	f.hc = noRedirectClient(d)
	f.mu.Unlock()
}

func (f *fakeUpstream) setBase(rawURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.base, _ = url.Parse(rawURL)
}
func (f *fakeUpstream) setInfo(fn func(*upstream.Info)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.info)
}
func (f *fakeUpstream) set(mode string, events ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
	if len(events) > 0 {
		f.events = events
	}
}

// CountTokens: one token per whitespace-separated word, exact — so tests can build a prompt of N tokens.
// It tracks how many calls run at once and, with countGate set, parks callers until the gate closes.
func (f *fakeUpstream) CountTokens(_ context.Context, text string) (int, bool, error) {
	f.mu.Lock()
	err, gate, boom := f.countErr, f.countGate, f.countPanic
	f.mu.Unlock()
	if err != nil {
		return 0, false, err
	}
	if boom {
		panic("fake upstream: tokenize exploded")
	}
	n := f.countNow.Add(1)
	defer f.countNow.Add(-1)
	for m := f.countMax.Load(); n > m && !f.countMax.CompareAndSwap(m, n); m = f.countMax.Load() {
	}
	if gate != nil {
		<-gate
	}
	return len(strings.Fields(text)), true, nil
}

// gateTokenize makes CountTokens block until the returned func is called.
func (f *fakeUpstream) gateTokenize() (open func()) {
	gate := make(chan struct{})
	f.mu.Lock()
	f.countGate = gate
	f.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

func (f *fakeUpstream) body(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	dec := json.NewDecoder(bytes.NewReader(f.lastBody))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("upstream body not JSON: %v: %q", err, f.lastBody)
	}
	return m
}

func (f *fakeUpstream) written() string { f.mu.Lock(); defer f.mu.Unlock(); return f.wrote.String() }
func (f *fakeUpstream) order() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tags...)
}

// deadUpstream is an Upstream whose engine address refuses connections.
type deadUpstream struct{ *fakeUpstream }

func newDeadUpstream(t *testing.T) *deadUpstream {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	f := newFakeUpstream()
	t.Cleanup(f.srv.Close)
	f.base, _ = url.Parse("http://" + addr)
	return &deadUpstream{f}
}

// ---- harness ----

type harness struct {
	t     *testing.T
	up    *fakeUpstream
	store *fakeStore
	rec   *fakeRecorder
	gw    *Gateway
	srv   *httptest.Server
	key   *keys.Key
}

func defaultKey() *keys.Key {
	return &keys.Key{ID: "k_alice1", Name: "alice", Status: keys.Active, CreatedAt: time.Now(), Limits: keys.Limits{MaxOutputTokens: 2048}}
}

func newHarness(t *testing.T, cfg Config, up upstream.Engine) *harness {
	t.Helper()
	h := &harness{t: t, store: &fakeStore{keys: map[string]*keys.Key{}}, rec: &fakeRecorder{}}
	switch u := up.(type) {
	case nil:
		h.up = newFakeUpstream()
		t.Cleanup(h.up.srv.Close)
		up = h.up
	case *fakeUpstream:
		h.up = u
	case *deadUpstream:
		h.up = u.fakeUpstream
	}
	h.key = defaultKey()
	h.store.set(testSecret, h.key)
	if cfg.HostName == "" {
		cfg.HostName = "max-laptop"
	}
	h.gw = New(cfg, up, h.store, h.rec, globalLogs.logf)
	h.srv = httptest.NewServer(h.gw.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) setKey(fn func(*keys.Key)) {
	fn(h.key)
	h.store.set(testSecret, h.key)
}

// slots is what the engine reports; the gateway's queue reads it at every decision (DESIGN §1.5).
func (h *harness) slots(n int) { h.up.setInfo(func(i *upstream.Info) { i.Slots = n }) }

type resp struct {
	status  int
	header  http.Header
	body    []byte
	errCode Code
	errType string
	message string
}

// do sends a request and reads the whole body. auth "" = no header; "bearer" = the test secret.
func (h *harness) do(method, path, auth string, body string) resp {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	switch auth {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+testSecret)
	case "":
	default:
		req.Header.Set("Authorization", auth)
	}
	r, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if bytes.Contains(b, []byte(testSecret)) {
		h.t.Fatalf("secret leaked into a response body: %s", b)
	}
	out := resp{status: r.StatusCode, header: r.Header, body: b}
	var eb errorBody
	if r.StatusCode >= 400 && json.Unmarshal(b, &eb) == nil {
		out.errCode, out.errType, out.message = eb.Error.Code, eb.Error.Type, eb.Error.Message
		seenCodesMu.Lock()
		seenCodes[eb.Error.Code] = true
		seenCodesMu.Unlock()
	}
	return out
}

func (h *harness) post(path, body string) resp { return h.do(http.MethodPost, path, "bearer", body) }
func (h *harness) get(path string) resp        { return h.do(http.MethodGet, path, "bearer", "") }

// expectErr asserts the contract's status/type/code triple and the Retry-After rule.
func (h *harness) expectErr(r resp, code Code) {
	h.t.Helper()
	row := codeTable[code]
	if r.status != row.status || r.errCode != code || r.errType != row.typ {
		h.t.Fatalf("want %d %s/%s, got %d %s/%s: %s", row.status, row.typ, code, r.status, r.errType, r.errCode, r.body)
	}
	ra := r.header.Get("Retry-After")
	switch r.status {
	case 429, 503:
		if ra == "" {
			h.t.Fatalf("%s: 429/503 must carry Retry-After", code)
		}
		if n, err := fmt.Sscanf(ra, "%d", new(int)); n != 1 || err != nil {
			h.t.Fatalf("%s: Retry-After %q is not whole seconds", code, ra)
		}
	default:
		if ra != "" {
			h.t.Fatalf("%s: unexpected Retry-After %q", code, ra)
		}
	}
	if r.header.Get("Content-Type") != "application/json" {
		h.t.Fatalf("%s: error content-type %q", code, r.header.Get("Content-Type"))
	}
}

func chatBody(model string, words int, extra string) string {
	var sb strings.Builder
	sb.WriteString(`{"messages":[{"role":"user","content":"`)
	for i := 0; i < words; i++ {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString("w")
	}
	sb.WriteString(`"}]`)
	if model != "" {
		sb.WriteString(`,"model":"` + model + `"`)
	}
	if extra != "" {
		sb.WriteString("," + extra)
	}
	sb.WriteString("}")
	return sb.String()
}

// streamReq opens a streaming chat request and returns the response for incremental reading.
func (h *harness) streamReq(ctx context.Context, body string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testSecret)
	return h.srv.Client().Do(req)
}

// readEvent reads one SSE event (up to and including the blank line) from a buffered stream.
func readEvent(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		line, err := r.ReadString('\n')
		sb.WriteString(line)
		if err != nil {
			if sb.Len() > 0 && errors.Is(err, io.EOF) {
				return sb.String(), nil
			}
			return sb.String(), err
		}
		if line == "\n" || line == "\r\n" {
			return sb.String(), nil
		}
	}
}
