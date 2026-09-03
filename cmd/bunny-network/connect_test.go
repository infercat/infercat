package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2185Lab/bunny-network/internal/admin"
	"github.com/2185Lab/bunny-network/internal/tunnel"
)

const testSecret = "s3cr3t-s3cr3t-s3cr3t-s3cr3t-s3cr3t-s3cr3t-0"

// fakeSession is a tunnel session over loopback TCP to a fake gateway. `dead` makes Open hang
// until its context expires, which is what a dial over a session to a sleeping host does.
type fakeSession struct {
	addr     string
	dead     atomic.Bool
	dials    atomic.Int32
	redials  atomic.Int32
	mu       sync.Mutex
	path     tunnel.Path
	pathErr  error
	closeErr error // what Close answers: ErrCloseTimeout is a relay-only close that parked (035)
}

func (f *fakeSession) Open(ctx context.Context) (net.Conn, error) {
	f.dials.Add(1)
	if f.dead.Load() {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", f.addr)
}

func (f *fakeSession) Path(context.Context) (tunnel.Path, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.path, f.pathErr
}

func (f *fakeSession) setPath(p tunnel.Path, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.path, f.pathErr = p, err
}

func (f *fakeSession) Redial(ctx context.Context) (session, error) {
	f.redials.Add(1)
	if f.dead.Load() {
		return nil, errors.New("the host did not answer the handshake")
	}
	return f, nil
}

func (f *fakeSession) Close() error { return f.closeErr }

// fakeHost speaks the gateway's HTTP API well enough for the relay to be judged: it records the
// bearer it saw, answers /me, and drives each case from the query string.
type fakeHost struct {
	*httptest.Server
	auth     chan string   // the Authorization header of every request
	release  chan struct{} // the streaming case sends one event per receive
	meHangs  atomic.Bool   // the host is gone: /me never answers
	meServed atomic.Int32
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	g := &fakeHost{auth: make(chan string, 64), release: make(chan struct{})}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.auth <- r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body) // always: a handler that never reads the body never learns the client left
		if r.URL.Path == "/me" {
			g.meServed.Add(1)
			if g.meHangs.Load() {
				<-r.Context().Done()
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"key":{"id":"k_1","name":"alice","status":"active"},"host":{"name":"Testhost","models":["m1","m2"],"relay":{"region":"Testville"}}}`)
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"object":"list","data":[{"id":"m1"}]}`)
			return
		}
		errorf := func(status int, code, typ, msg string, retry int) {
			w.Header().Set("Content-Type", "application/json")
			if retry > 0 {
				w.Header().Set("Retry-After", fmt.Sprint(retry))
			}
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"error":{"message":%q,"type":%q,"code":%q}}`, msg, typ, code)
		}
		rc := http.NewResponseController(w)
		switch r.URL.Query().Get("case") {
		case "paused":
			errorf(403, "key_paused", "permission_error", "key k_1 is paused", 0)
		case "limited":
			errorf(429, "rate_limited", "rate_limit_error", "rate limit of 20/min exceeded", 12)
		case "context":
			errorf(422, "context_too_long", "invalid_request_error", "prompt is 5000 tokens but the context is 4096", 0)
		case "unknown":
			errorf(418, "teapot", "odd_error", "the host said something new", 0)
		case "stall":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"one\"}}]}\n\n")
			rc.Flush()
			<-r.Context().Done() // never another byte
		case "slow":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			io.WriteString(w, ": queued\n\n")
			rc.Flush()
			select {
			case <-g.release:
			case <-r.Context().Done():
				return
			}
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n\ndata: [DONE]\n\n")
			rc.Flush()
		case "stream":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			for i := 1; i <= 2; i++ {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"t%d\"}}]}\n\n", i)
				rc.Flush()
				select {
				case <-g.release:
				case <-r.Context().Done():
					return
				}
			}
			io.WriteString(w, "data: {\"error\":{\"message\":\"no slot free within 30s\",\"type\":\"upstream_error\",\"code\":\"queue_timeout\",\"retry_after\":5}}\n\ndata: [DONE]\n\n")
			rc.Flush()
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"echo":%q,"method":%q}`, body, r.Method)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *fakeHost) lastAuth(t *testing.T) string {
	t.Helper()
	select {
	case a := <-g.auth:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("the fake gateway saw no request")
		return ""
	}
}

// serveConnector runs the relay on a loopback port with the given session, the way cmdConnect does.
func serveConnector(t *testing.T, sess session) (*connector, string, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	c := &connector{secret: testSecret, host: "Testhost", relayName: "Testville", sess: sess, out: &out, logRequests: true, events: admin.NewEvents(nil),
		logf: func(f string, a ...any) { fmt.Fprintf(&out, f+"\n", a...) }, wake: make(chan struct{}, 1)}
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	return c, srv.URL, &out
}

func hurry(t *testing.T, d time.Duration) {
	t.Helper()
	prev := []time.Duration{dialTimeout, silenceTimeout, probeTimeout, pathEvery, connectTimeout}
	dialTimeout, silenceTimeout, probeTimeout, pathEvery, connectTimeout = d, d, d, d, d
	t.Cleanup(func() {
		dialTimeout, silenceTimeout, probeTimeout, pathEvery, connectTimeout = prev[0], prev[1], prev[2], prev[3], prev[4]
	})
}

type gwErr struct {
	Error struct {
		Message    string `json:"message"`
		Type       string `json:"type"`
		Code       string `json:"code"`
		RetryAfter int    `json:"retry_after"`
	} `json:"error"`
}

func decodeErr(t *testing.T, b []byte) gwErr {
	t.Helper()
	var e gwErr
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("not the error format: %v\n%s", err, b)
	}
	return e
}

// Promise 1: the invite's key goes on every request and the app's own key is ignored; /v1/* and
// /me are the routes; anything else is a 404 here and never crosses the tunnel.
func TestConnectInjectsTheInviteKey(t *testing.T) {
	g := newFakeHost(t)
	sess := &fakeSession{addr: g.Listener.Addr().String()}
	_, base, _ := serveConnector(t, sess)

	req, _ := http.NewRequest(http.MethodPost, base+"/v1/chat/completions", strings.NewReader(`{"model":"m1"}`))
	req.Header.Set("Authorization", "Bearer sk-the-apps-own-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := g.lastAuth(t); got != "Bearer "+testSecret {
		t.Fatalf("the gateway saw Authorization %q; want the invite's key", got)
	}
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"echo":"{\"model\":\"m1\"}"`) || !strings.Contains(string(body), `"method":"POST"`) {
		t.Fatalf("POST relayed as %d %s", resp.StatusCode, body)
	}
	for _, path := range []string{"/me", "/v1/models"} {
		resp, err := http.Get(base + path) // no key at all: still works
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || g.lastAuth(t) != "Bearer "+testSecret {
			t.Fatalf("GET %s without a key: %d", path, resp.StatusCode)
		}
	}
	dials := sess.dials.Load()
	for _, path := range []string{"/healthz", "/", "/admin", "/v2/models", "/v1"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if e := decodeErr(t, b); resp.StatusCode != 404 || e.Error.Code != "not_found" || e.Error.Type != "invalid_request_error" {
			t.Fatalf("GET %s = %d %s; want 404 not_found in the error format", path, resp.StatusCode, b)
		}
	}
	if sess.dials.Load() != dials {
		t.Fatal("an unrouted path crossed the tunnel")
	}
}

// Promise 1: streaming is relayed with immediate flush — an event reaches the app while the host
// is still holding the next one — and the gateway's error event inside a 200 stream comes through
// in the friend's words with its code and retry_after intact, followed by [DONE].
func TestConnectStreamsEachEventAsItArrives(t *testing.T) {
	g := newFakeHost(t)
	_, base, out := serveConnector(t, &fakeSession{addr: g.Listener.Addr().String()})
	resp, err := http.Post(base+"/v1/chat/completions?case=stream", "application/json", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != 200 || ct != "text/event-stream" {
		t.Fatalf("stream head = %d %q", resp.StatusCode, ct)
	}
	br := bufio.NewReader(resp.Body)
	event := func() string {
		t.Helper()
		var lines []string
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				if line == "\n" {
					return
				}
				lines = append(lines, strings.TrimRight(line, "\n"))
			}
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("no event arrived within 3s: the relay is buffering")
		}
		return strings.Join(lines, "\n")
	}
	for i := 1; i <= 2; i++ {
		if ev := event(); !strings.Contains(ev, fmt.Sprintf(`"content":"t%d"`, i)) {
			t.Fatalf("event %d = %q", i, ev)
		}
		g.release <- struct{}{} // the host sends the next one only after this one was seen here
	}
	ev := event()
	e := decodeErr(t, []byte(strings.TrimPrefix(ev, "data: ")))
	if e.Error.Code != "queue_timeout" || e.Error.RetryAfter != 5 || e.Error.Type != "upstream_error" ||
		!strings.Contains(e.Error.Message, "Testhost is busy") || !strings.Contains(e.Error.Message, "host said: no slot free within 30s") {
		t.Fatalf("error event = %s", ev)
	}
	if ev := event(); ev != "data: [DONE]" {
		t.Fatalf("after the error event: %q; want [DONE]", ev)
	}
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("stream did not end after [DONE]: %v", err)
	}
	if !strings.Contains(out.String(), "Testhost  chat") || !strings.Contains(out.String(), "queue_timeout — Testhost is busy") {
		t.Fatalf("the terminal line does not say how the request ended:\n%s", out.String())
	}
}

// Promise 3: the gateway's status, code and Retry-After pass through; the sentence becomes the
// friend's, naming the host, with the host's own sentence kept as evidence. A code this client
// has no words for keeps the host's sentence.
func TestConnectMapsErrorsToTheFriendsWords(t *testing.T) {
	g := newFakeHost(t)
	_, base, _ := serveConnector(t, &fakeSession{addr: g.Listener.Addr().String()})
	for _, tc := range []struct {
		kase       string
		status     int
		code, typ  string
		retryAfter string
		want       string
	}{
		{"paused", 403, "key_paused", "permission_error", "", "your invite is paused — ask Testhost to resume it, then try again (host said: key k_1 is paused)"},
		{"limited", 429, "rate_limited", "rate_limit_error", "12", "too fast for this invite — Testhost allows a set number of messages a minute; the count clears on its own (host said: rate limit of 20/min exceeded)"},
		{"context", 422, "context_too_long", "invalid_request_error", "", "this conversation no longer fits the model — start a new chat, or shorten what you sent (host said: prompt is 5000 tokens but the context is 4096)"},
		{"unknown", 418, "teapot", "odd_error", "", "the host said something new"},
	} {
		resp, err := http.Post(base+"/v1/chat/completions?case="+tc.kase, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		e := decodeErr(t, b)
		if resp.StatusCode != tc.status || e.Error.Code != tc.code || e.Error.Type != tc.typ || e.Error.Message != tc.want || resp.Header.Get("Retry-After") != tc.retryAfter {
			t.Fatalf("%s: %d Retry-After=%q %s\nwant %d %s %q", tc.kase, resp.StatusCode, resp.Header.Get("Retry-After"), b, tc.status, tc.code, tc.want)
		}
	}
}

// Promise 3: a host that does not answer the dial is declared asleep at the web client's bound,
// as a 503 with Retry-After and the friend's words — and the keeper is told to look at the session.
func TestConnectDeclaresAnAsleepHostWithinTheBound(t *testing.T) {
	hurry(t, 300*time.Millisecond)
	sess := &fakeSession{addr: "127.0.0.1:1"}
	sess.dead.Store(true)
	c, base, _ := serveConnector(t, sess)
	t0 := time.Now()
	resp, err := http.Get(base + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	took := time.Since(t0)
	e := decodeErr(t, b)
	if resp.StatusCode != 503 || e.Error.Code != "host_asleep" || resp.Header.Get("Retry-After") == "" ||
		!strings.Contains(e.Error.Message, "Testhost didn't answer — it's probably asleep or offline") {
		t.Fatalf("asleep host = %d Retry-After=%q %s", resp.StatusCode, resp.Header.Get("Retry-After"), b)
	}
	if took < dialTimeout || took > 2*time.Second {
		t.Fatalf("declared asleep after %v; the bound is %v", took, dialTimeout)
	}
	select {
	case <-c.wake:
	default:
		t.Fatal("the keeper was not told the session looks dead")
	}
	t.Logf("asleep host: 503 host_asleep after %v (bound %v)", took.Round(time.Millisecond), dialTimeout)
}

// Promise 3 (the fourth silence, web/src/api.ts): a stream that goes quiet is checked against /me.
// A host that answers is a slow model — the stream continues and completes; a host that does not
// answer ends the stream with a host_stalled event and [DONE], not a silent truncation.
func TestConnectSilenceIsProbedNotAssumed(t *testing.T) {
	hurry(t, 200*time.Millisecond)
	g := newFakeHost(t)
	_, base, _ := serveConnector(t, &fakeSession{addr: g.Listener.Addr().String()})
	events := func(kase string) []string {
		t.Helper()
		resp, err := http.Post(base+"/v1/chat/completions?case="+kase, "application/json", strings.NewReader(`{"stream":true}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return strings.Split(strings.TrimSpace(string(b)), "\n\n")
	}

	// slow: the host keeps answering /me while the model takes 4 silences to say anything.
	go func() {
		time.Sleep(4 * silenceTimeout)
		g.release <- struct{}{}
	}()
	evs := events("slow")
	if len(evs) != 3 || evs[0] != ": queued" || !strings.Contains(evs[1], `"late"`) || evs[2] != "data: [DONE]" {
		t.Fatalf("slow host: %q", evs)
	}
	if g.meServed.Load() < 2 {
		t.Fatalf("/me was asked %d times during 4 silences; want probes, not patience", g.meServed.Load())
	}

	// stall: one token, then nothing, and /me hangs too.
	g.meHangs.Store(true)
	t0 := time.Now()
	evs = events("stall")
	if len(evs) != 3 || !strings.Contains(evs[0], `"one"`) || evs[2] != "data: [DONE]" {
		t.Fatalf("stalled host: %q", evs)
	}
	e := decodeErr(t, []byte(strings.TrimPrefix(evs[1], "data: ")))
	if e.Error.Code != "host_stalled" || !strings.Contains(e.Error.Message, "Testhost stopped answering mid-reply") {
		t.Fatalf("stalled host event = %s", evs[1])
	}
	t.Logf("stalled host: host_stalled + [DONE] after %v (silence %v + probe %v)", time.Since(t0).Round(time.Millisecond), silenceTimeout, probeTimeout)
}

// Promise 2 and 3: the path is re-measured and printed on change; a session that stops answering
// is replaced with backoff under the same identity, requests fail fast meanwhile, and the
// reconnect is announced with the new path.
func TestConnectReprintsThePathAndReconnectsWithBackoff(t *testing.T) {
	hurry(t, 50*time.Millisecond)
	g := newFakeHost(t)
	sess := &fakeSession{addr: g.Listener.Addr().String()}
	sess.setPath(tunnel.Path{Via: "nyc", RTT: 27 * time.Millisecond}, nil)
	c, base, out := serveConnector(t, sess)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.keep(ctx, tunnel.Path{Via: "nyc", RTT: 27 * time.Millisecond})

	waitFor := func(what string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !strings.Contains(out.String(), what); {
			if time.Now().After(deadline) {
				t.Fatalf("never printed %q:\n%s", what, out.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	sess.setPath(tunnel.Path{Direct: true, Via: "192.0.2.7:41641", RTT: 800 * time.Microsecond}, nil)
	waitFor("path      direct · 0.8 ms")
	if strings.Contains(out.String(), "relayed via") {
		t.Fatalf("an unchanged path was reprinted:\n%s", out.String())
	}

	// The host goes away: pings fail, the session is declared lost, redials fail and back off.
	sess.dead.Store(true)
	sess.setPath(tunnel.Path{}, errors.New("no pong"))
	waitFor("path      lost")
	waitFor("trying again in 1s")
	t0 := time.Now()
	resp, err := http.Get(base + "/v1/models") // fails at once: no dial against a dead session
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e := decodeErr(t, b); resp.StatusCode != 503 || e.Error.Code != "host_asleep" || time.Since(t0) > time.Second {
		t.Fatalf("request while reconnecting = %d %s after %v", resp.StatusCode, b, time.Since(t0))
	}
	waitFor("trying again in 2s")

	// The host is back: the redial succeeds and the new path is announced.
	sess.dead.Store(false)
	sess.setPath(tunnel.Path{Via: "nyc", RTT: 30 * time.Millisecond}, nil)
	waitFor("path      reconnected · relayed via Testville · 30 ms")
	if resp, err := http.Get(base + "/v1/models"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("after reconnect: %v %v", resp, err)
	}
	t.Logf("redials: %d\n%s", sess.redials.Load(), out.String())
}

// Promise 2, through the real command: the banner's order (host and models, path, local URL, the
// base-URL hint), a working endpoint, and a dead invite refused with the friend's words.
func TestConnectCommandBannerAndRefusals(t *testing.T) {
	g := newFakeHost(t)
	sess := &fakeSession{addr: g.Listener.Addr().String()}
	sess.setPath(tunnel.Path{Via: "nyc", RTT: 27 * time.Millisecond}, nil)
	sess.closeErr = tunnel.ErrCloseTimeout // the shutdown's close parks (035): connect says so and still exits 0
	plat := testPlatform(fakeAddr, nil)
	plat.dialTunnel = func(ctx context.Context, addr string, logf func(string, ...any)) (session, error) {
		if addr != fakeAddr {
			return nil, fmt.Errorf("dialled %q", addr)
		}
		return sess, nil
	}
	inv := "bn1." + fakeAddr + "." + testSecret

	for _, bad := range [][]string{{"connect"}, {"connect", "nope"}, {"connect", inv, "--listen", "0.0.0.0:11435"}, {"connect", inv, "extra"}} {
		if r := exec(t, plat, bad...); r.code == 0 {
			t.Fatalf("%v exited 0:\n%s%s", bad, r.out, r.err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errw bytes.Buffer
	code := make(chan int, 1)
	go func() {
		code <- run(ctx, []string{"connect", inv, "--listen", "127.0.0.1:0"}, &out, &errw, nil, false, plat)
	}()
	var base string
	for deadline := time.Now().Add(10 * time.Second); base == ""; {
		if m := regexp.MustCompile(`local\s+(http://127\.0\.0\.1:\d+)`).FindStringSubmatch(out.String()); m != nil {
			base = m[1]
		} else if time.Now().After(deadline) {
			t.Fatalf("no local URL in the banner:\n%s%s", out.String(), errw.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	banner := out.String()
	order := []string{"host      Testhost  ·  m1 (+1 more)", "path      relayed via Testville · 27 ms", "local     " + base, "set your app's base URL to " + base + "/v1, any API key"}
	at := -1
	for _, want := range order {
		i := strings.Index(banner, want)
		if i < 0 || i < at {
			t.Fatalf("banner lacks %q in order:\n%s", want, banner)
		}
		at = i
	}
	if strings.Contains(banner+errw.String(), testSecret) {
		t.Fatal("the banner printed the secret")
	}
	resp, err := http.Get(base + "/v1/models")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET /v1/models through the command: %v %v", resp, err)
	}
	resp.Body.Close()
	cancel()
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("exit %d\n%s%s", c, out.String(), errw.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("connect did not stop after Ctrl-C")
	}
	if !strings.Contains(errw.String(), "tunnel close timed out; continuing") {
		t.Fatalf("a close that timed out must be said on the way out:\n%s", errw.String())
	}

	// A revoked invite: /me says so, connect says it in the friend's words and exits 1.
	revoked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `{"error":{"message":"key k_1 is revoked","type":"permission_error","code":"key_revoked"}}`)
	}))
	defer revoked.Close()
	sess.addr = revoked.Listener.Addr().String()
	r := exec(t, plat, "connect", inv, "--listen", "127.0.0.1:0")
	if r.code != 1 || !strings.Contains(r.err, "this invite was revoked — ask your host for a new code") {
		t.Fatalf("revoked invite: exit %d\n%s%s", r.code, r.out, r.err)
	}
	t.Logf("banner:\n%s", banner)
}
