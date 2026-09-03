package main

// connect (ticket 026): the host binary as a client. One tunnel session to the host, reused for
// every request; a loopback OpenAI-compatible endpoint any app can point at; the invite's key
// injected so the app needs none; and the friend's words for whatever the host says. The relay is
// hand-rolled the way the gateway's is (internal/gateway/proxy.go): every SSE event is flushed as
// it arrives, a stream that stops early says why, and every wait on the host is bounded.

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/2185Lab/bunny-network/internal/admin"
	"github.com/2185Lab/bunny-network/internal/invite"
	"github.com/2185Lab/bunny-network/internal/product"
	"github.com/2185Lab/bunny-network/internal/tunnel"
	"github.com/2185Lab/bunny-network/internal/usage"
)

const connectListen = "127.0.0.1:11435"

// The bounds, one owner each, mirrored from the web client (web/src/api.ts) so a friend on either
// client learns the same things at the same moments. Variables so a test can hurry them.
var (
	connectTimeout = 20 * time.Second // the relay handshake, at start and on every reconnect (033: the web app's bound)
	dialTimeout    = 15 * time.Second // a dial over a dead session hangs: the web client's asleep bound (answerMs)
	silenceTimeout = 15 * time.Second // no byte from the host for this long: ask /me whether it is there (idleMs)
	probeTimeout   = 10 * time.Second // the /me that decides (ME_TIMEOUT_MS)
	writeTimeout   = 60 * time.Second // an app that stops reading, per write (the gateway's client write deadline)
	pathEvery      = 30 * time.Second // the path line is re-measured this often and printed on change
)

// session is what connect needs from internal/tunnel, so the relay can be tested over plain TCP.
type session interface {
	Open(ctx context.Context) (net.Conn, error)
	Path(ctx context.Context) (tunnel.Path, error)
	Redial(ctx context.Context) (session, error)
	Close() error
}

type tunnelSession struct{ *tunnel.Session }

func (s tunnelSession) Redial(ctx context.Context) (session, error) {
	n, err := s.Session.Redial(ctx)
	if err != nil {
		return nil, err
	}
	return tunnelSession{n}, nil
}

func (e *env) cmdConnect(ctx context.Context, pre string, args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	listen := fs.String("listen", connectListen, "loopback address to serve the API on")
	verbose := fs.Bool("verbose", false, "print the tunnel engine's log on the terminal")
	logRequests := fs.Bool("log-requests", false, "print one line per request on this terminal (never prompt content)")
	dd := fs.String("data-dir", pre, "serve an admin socket there, so `status --data-dir` can watch this bridge")
	if err := e.parse(fs, connectHelp, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fmt.Fprint(e.errw, "connect takes exactly one invite\n\n", connectHelp)
		return errUsage
	}
	inv, err := invite.Decode(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("invite: %w", err)
	}
	ln, err := listenLoopback(*listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	var tunLogf func(string, ...any)
	if *verbose {
		tunLogf = e.logf
	}
	e.logf("connecting to the host through its relay…")
	dctx, cancel := context.WithTimeout(ctx, connectTimeout)
	t0 := time.Now()
	sess, err := e.plat.dialTunnel(dctx, inv.Addr, tunLogf)
	cancel()
	if err != nil {
		return fmt.Errorf("%s (%v)", (&connector{}).words("host_asleep", ""), err)
	}
	c := &connector{secret: inv.Secret, sess: sess, out: e.out, logf: e.logf, wake: make(chan struct{}, 1), handshake: time.Since(t0),
		logRequests: *logRequests, events: admin.NewEvents(nil), addr: inv.Addr, local: "http://" + ln.Addr().String(), started: time.Now()}
	defer func() { c.mu.Lock(); c.sess.Close(); c.mu.Unlock() }()
	me, herr := c.me(ctx)
	if herr != nil && (herr.Code == "invalid_key" || herr.Code == "key_revoked" || herr.Status == 0) {
		return errors.New(herr.Message)
	}
	c.host, c.relayName = me.Host.Name, me.Host.Relay.Region
	p, _ := c.path(ctx)
	c.setPath(p)
	if *dd != "" { // a bridge with a data dir is watchable like a host (029 promise 5)
		adm, err := admin.Serve(*dd, c.status, nil, c.events)
		if err != nil {
			return fmt.Errorf("admin API: %w", err)
		}
		defer adm.Close()
	}
	fmt.Fprintf(e.out, "%s %s\n", product.Name, product.Version)
	fmt.Fprintf(e.out, "host      %s  ·  %s\n", orDash(me.Host.Name), modelList(me.Host.Models))
	if herr != nil {
		fmt.Fprintf(e.out, "invite    %s — %s\n", herr.Code, herr.Message)
	}
	fmt.Fprintf(e.out, "path      %s\n", c.describe(p))
	fmt.Fprintf(e.out, "local     http://%s\n", ln.Addr())
	fmt.Fprintf(e.out, "          set your app's base URL to http://%s/v1, any API key\n", ln.Addr())
	go c.keep(ctx, p)
	srv := &http.Server{Handler: c, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute, ErrorLog: log.New(e.errw, "connect: http: ", 0)}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		fmt.Fprintln(e.out, "\nshutting down")
	case err := <-errc:
		return err
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}

// listenLoopback refuses anything but loopback: the local endpoint carries the invite's key for
// whoever reaches it, so it is for the apps on this machine.
func listenLoopback(addr string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("--listen %q: %w", addr, err)
	}
	ip := net.ParseIP(cmp.Or(strings.Replace(host, "localhost", "127.0.0.1", 1)))
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("--listen %q refused: only loopback addresses (127.0.0.1 or ::1) are served", addr)
	}
	return net.Listen("tcp", net.JoinHostPort(ip.String(), port))
}

// connector is the local endpoint: the session it relays over, and the words it uses.
type connector struct {
	secret    string
	host      string // the host's display name; the friend's words say "your host" when it has none
	relayName string // the host's relay as it names itself (/me), for the path line
	out       io.Writer
	logf      func(string, ...any)

	mu        sync.Mutex
	sess      session
	down      bool          // the session is being replaced: requests fail fast instead of dialling a corpse
	wake      chan struct{} // a request that found the session dead asks the keeper to look now
	last      tunnel.Path   // the path as last measured, for status
	handshake time.Duration // how long the relay handshake took, at start or the last reconnect

	// Observability (029): the request line on the terminal when asked, and the same events on
	// the admin socket when a data dir was given.
	logRequests bool
	events      *admin.Events
	addr, local string // the host's tunnel address; this bridge's URL
	started     time.Time
	inFlight    atomic.Int32
}

func (c *connector) setPath(p tunnel.Path) {
	c.mu.Lock()
	c.last = p
	c.mu.Unlock()
}

// status is the bridge's admin status: the one session it holds, described the host's way —
// with the path, RTT and handshake time a host cannot see but a client measures.
func (c *connector) status() admin.Status {
	c.mu.Lock()
	p, down, hs := c.last, c.down, c.handshake
	c.mu.Unlock()
	path := "unknown"
	switch {
	case p.Direct:
		path = "direct"
	case p != (tunnel.Path{}):
		path = "relayed"
	}
	return admin.Status{
		Mode: "bridge", Name: c.host, UptimeS: int64(time.Since(c.started).Seconds()),
		Tunnel: admin.Tunnel{Addr: c.addr, Region: c.relayName, Sessions: []admin.Session{
			{Key: "host", Path: path, Via: p.Via, RTTMS: float64(p.RTT) / float64(time.Millisecond), HandshakeMS: hs.Milliseconds(),
				Since: c.started, Active: !down, Conns: int(c.inFlight.Load())}}},
		Upstream: admin.Upstream{Kind: "bridge", URL: c.local, Healthy: !down},
		Queue:    admin.Queue{InFlight: int(c.inFlight.Load())},
		Process:  admin.ProcessStats(),
		Keys:     []admin.Key{},
	}
}

// hostError is one failure the app sees: the gateway's status and code, the friend's sentence.
type hostError struct {
	Status     int
	Code, Type string
	Message    string
	RetryAfter int
}

// friendWords is the friend's sentence for each code the web app has one for (web/src/api.ts
// COPY), with {host} for the host's name. Codes not here keep the host's own sentence.
var friendWords = map[string]string{
	"invalid_key":         "{host} does not recognise this invite — it may have been rotated or deleted; ask for a fresh code",
	"key_paused":          "your invite is paused — ask {host} to resume it, then try again",
	"key_revoked":         "this invite was revoked — ask {host} for a new code",
	"host_asleep":         "{host} didn't answer — it's probably asleep or offline; try again in a minute",
	"host_stalled":        "{host} stopped answering mid-reply — try again; if it keeps happening, their machine may have gone to sleep",
	"rate_limited":        "too fast for this invite — {host} allows a set number of messages a minute; the count clears on its own",
	"concurrency_limited": "one reply at a time — this invite may have one request in flight",
	"budget_exhausted":    "today's token budget is used up — {host} sets a daily cap per invite",
	"queue_timeout":       "{host} is busy — every slot was taken; try again shortly",
	"upstream_down":       "{host}'s engine is offline — their machine is reachable but the model server is not running",
	"upstream_error":      "{host}'s engine returned an error",
	"model_not_allowed":   "that model is not shared with you — GET /v1/models lists the ones this invite may use",
	"context_too_long":    "this conversation no longer fits the model — start a new chat, or shorten what you sent",
}

func (c *connector) words(code, said string) string {
	w, ok := friendWords[code]
	if !ok {
		return said
	}
	w = strings.ReplaceAll(w, "{host}", cmp.Or(c.host, "your host"))
	if said != "" {
		w += " (host said: " + said + ")"
	}
	return w
}

func (c *connector) asleep() hostError {
	return hostError{Status: http.StatusServiceUnavailable, Code: "host_asleep", Type: "upstream_error", Message: c.words("host_asleep", ""), RetryAfter: 30}
}

func (c *connector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	c.inFlight.Add(1)
	defer c.inFlight.Add(-1)
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	var code, msg string
	if r.URL.Path == "/me" || strings.HasPrefix(r.URL.Path, "/v1/") {
		code, msg = c.relay(sw, r)
	} else {
		code, msg = c.reply(sw, hostError{Status: http.StatusNotFound, Code: "not_found", Type: "invalid_request_error",
			Message: fmt.Sprintf("no route for %s %s — this endpoint serves /v1/* and /me", r.Method, r.URL.Path)})
	}
	c.log(r, start, sw.status, code, msg)
}

// statusWriter remembers the status the app was sent, for the request line and the event.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int)        { s.status = code; s.ResponseWriter.WriteHeader(code) }
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// relay forwards one request over a fresh tunnel conn and streams the answer back. It returns
// what the log line says: the code the request ended with, if any, and the sentence.
func (c *connector) relay(w http.ResponseWriter, r *http.Request) (string, string) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	rc := http.NewResponseController(w)
	if r.ContentLength != 0 { // bound the app's body, as the gateway bounds the friend's
		_ = rc.SetReadDeadline(time.Now().Add(30 * time.Second))
	}
	conn, err := c.open(ctx)
	if err != nil {
		if ctx.Err() == nil { // not the app leaving: the session may be dead — the keeper looks now
			select {
			case c.wake <- struct{}{}:
			default:
			}
		}
		return c.reply(w, c.asleep())
	}
	go func() { <-ctx.Done(); conn.Close() }() // the app hung up, or we are done: free the tunnel conn
	wc := &watched{Conn: conn, probe: c.probe}
	wc.arm()
	defer wc.t.Stop()

	out := outbound(r, c.secret)
	_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err := out.Write(conn); err != nil {
		return c.reply(w, c.asleep())
	}
	_ = conn.SetWriteDeadline(time.Time{})
	_ = rc.SetReadDeadline(time.Time{})
	resp, err := http.ReadResponse(bufio.NewReaderSize(wc, 64<<10), out)
	if err != nil {
		if ctx.Err() != nil {
			return "client_closed", "the app went away"
		}
		return c.reply(w, c.asleep())
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return c.reply(w, c.hostErr(resp))
	}
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		if _, err := io.Copy(w, resp.Body); err != nil && ctx.Err() != nil {
			return "client_closed", "the app went away"
		}
		return "", ""
	}
	return c.pipeStream(ctx, w, rc, resp.Body)
}

// pipeStream copies SSE bytes verbatim, flushing at every event boundary, and rewrites the one
// kind of event that is about the friend rather than the model: the gateway's error event. A
// stream the host stops early ends with an error event and [DONE], never a silent truncation.
func (c *connector) pipeStream(ctx context.Context, w http.ResponseWriter, rc *http.ResponseController, body io.Reader) (string, string) {
	br := bufio.NewReaderSize(body, 64<<10)
	ended, said := "", "" // the gateway's error event, if the stream carried one: the log line names it
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if bytes.HasPrefix(line, []byte(`data: {"error"`)) {
				var he hostError
				line, he = c.rewriteEvent(line)
				ended, said = he.Code, he.Message
			}
			_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, werr := w.Write(line); werr != nil {
				return "client_closed", "the app stopped reading"
			}
			if len(bytes.TrimRight(line, "\r\n")) == 0 {
				_ = rc.Flush()
			}
		}
		if err != nil {
			_ = rc.Flush()
			if errors.Is(err, io.EOF) {
				return ended, said
			}
			if ctx.Err() != nil {
				return "client_closed", "the app went away"
			}
			he := hostError{Code: "host_stalled", Type: "upstream_error", Message: c.words("host_stalled", "")}
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", errorJSON(he, true))
			_ = rc.Flush()
			return he.Code, he.Message
		}
	}
}

// rewriteEvent puts the friend's words into a gateway error event, keeping code, type and
// retry_after; a line that is not the documented shape passes through untouched.
func (c *connector) rewriteEvent(line []byte) ([]byte, hostError) {
	var ev struct {
		Error struct {
			Message    string `json:"message"`
			Type       string `json:"type"`
			Code       string `json:"code"`
			RetryAfter int    `json:"retry_after,omitempty"`
		} `json:"error"`
	}
	if json.Unmarshal(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("data:")), &ev) != nil || ev.Error.Code == "" {
		return line, hostError{}
	}
	he := hostError{Code: ev.Error.Code, Type: ev.Error.Type, Message: c.words(ev.Error.Code, ev.Error.Message), RetryAfter: ev.Error.RetryAfter}
	return append(append([]byte("data: "), errorJSON(he, true)...), '\n'), he
}

// hostErr reads a gateway error response into the friend's words. Status, code, type and
// Retry-After are the gateway's; only the sentence changes.
func (c *connector) hostErr(resp *http.Response) hostError {
	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = json.Unmarshal(raw, &body)
	retry, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
	said := cmp.Or(body.Error.Message, strings.TrimSpace(string(raw)))
	return hostError{Status: resp.StatusCode, Code: body.Error.Code, Type: cmp.Or(body.Error.Type, "upstream_error"),
		Message: c.words(body.Error.Code, said), RetryAfter: retry}
}

func errorJSON(he hostError, inStream bool) []byte {
	e := map[string]any{"message": he.Message, "type": he.Type, "code": he.Code}
	if inStream && he.RetryAfter > 0 {
		e["retry_after"] = he.RetryAfter
	}
	b, _ := json.Marshal(map[string]any{"error": e})
	return b
}

// reply writes he as a full response in the gateway's error format and returns what the log line
// says. Callers must not have written headers yet.
func (c *connector) reply(w http.ResponseWriter, he hostError) (string, string) {
	b := errorJSON(he, false)
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	if he.RetryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(he.RetryAfter))
	}
	w.WriteHeader(he.Status)
	_, _ = w.Write(b)
	return he.Code, he.Message
}

// log records one event per request — never a prompt, never the key (Protection 3, Protection
// 2) — for the admin socket's stream, and with --log-requests prints the shared request line
// (the other party being the host) with the friend's sentence when the request failed.
func (c *connector) log(r *http.Request, start time.Time, status int, code, msg string) {
	ev := usage.Event{TS: start, Endpoint: r.URL.Path, Status: status, Code: code, TotalMS: time.Since(start).Milliseconds()}
	if code == "client_closed" {
		ev.Status = 499
	}
	c.events.Record(r.Context(), ev)
	if !c.logRequests {
		return
	}
	line := requestLine(ev, cmp.Or(c.host, "host"))
	if msg != "" && code != "" {
		line += " — " + msg
	}
	fmt.Fprintln(c.out, line)
}

// dropHeaders are the hop-by-hop headers and the ones the relay sets itself.
var dropHeaders = map[string]bool{"Authorization": true, "Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true,
	"Proxy-Authorization": true, "Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "Content-Length": true, "Host": true}

// outbound is the request as the gateway sees it: the app's method, path and body, the invite's
// key in place of whatever key the app sent (the invite is the key; the app's is ignored), one
// connection per request like the web client.
func outbound(r *http.Request, secret string) *http.Request {
	out := &http.Request{Method: r.Method, URL: &url.URL{Path: r.URL.Path, RawQuery: r.URL.RawQuery}, Proto: "HTTP/1.1",
		ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header, len(r.Header)), Host: "bunny", Close: true, ContentLength: r.ContentLength}
	copyHeaders(out.Header, r.Header)
	out.Header.Set("Authorization", "Bearer "+secret)
	if r.ContentLength != 0 {
		out.Body = r.Body
	}
	return out
}

func copyHeaders(dst, src http.Header) {
	for k, v := range src {
		if !dropHeaders[k] {
			dst[k] = v
		}
	}
}

// watched is the tunnel conn under the silence rule (web/src/api.ts): a byte re-arms the timer;
// silenceTimeout without one asks the host for /me, and only a host that does not answer that
// ends the request — a slow model, a long prefill, a queue are never called asleep.
type watched struct {
	net.Conn
	probe func() bool
	t     *time.Timer
	seen  atomic.Int64
}

func (x *watched) arm() { x.t = time.AfterFunc(silenceTimeout, x.silent) }

func (x *watched) Read(p []byte) (int, error) {
	n, err := x.Conn.Read(p)
	if n > 0 {
		x.seen.Add(1)
		x.t.Reset(silenceTimeout)
	}
	return n, err
}

func (x *watched) silent() {
	at := x.seen.Load()
	if x.probe() || x.seen.Load() != at {
		x.t.Reset(silenceTimeout)
		return
	}
	x.Conn.Close() // the read in flight fails; relay says host_asleep or host_stalled by where it was
}

// open dials the gateway over the current session, bounded; fails at once while a reconnect is
// in progress, so a request never waits on a session already known to be dead.
func (c *connector) open(ctx context.Context) (net.Conn, error) {
	c.mu.Lock()
	s, down := c.sess, c.down
	c.mu.Unlock()
	if down {
		return nil, errors.New("the session is being re-established")
	}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	return s.Open(dctx)
}

// fetch is one bounded GET over the session: connect's own /me at start, and the probe.
// Closing the body closes the conn.
func (c *connector) fetch(ctx context.Context, path string) (*http.Response, error) {
	conn, err := c.open(ctx)
	if err != nil {
		return nil, err
	}
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}
	req := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: path}, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Authorization": {"Bearer " + c.secret}}, Host: "bunny", Close: true}
	resp, err := (*http.Response)(nil), req.Write(conn)
	if err == nil {
		resp, err = http.ReadResponse(bufio.NewReader(conn), req)
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{resp.Body, conn}
	return resp, nil
}

// probe is "is the host there": any HTTP answer to /me means the host is awake, whatever its
// engine is doing; the gateway's own deadlines end a request the engine abandons.
func (c *connector) probe() bool {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	resp, err := c.fetch(ctx, "/me")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// meInfo is what connect reads from /me: who the host is, what it shares, where its relay is.
type meInfo struct {
	Key  struct{ Status string } `json:"key"`
	Host struct {
		Name   string                  `json:"name"`
		Models []string                `json:"models"`
		Relay  struct{ Region string } `json:"relay"`
	} `json:"host"`
}

// me verifies the invite. A gateway answer that is not 200 comes back as a hostError in the
// friend's words; a host that does not answer at all is a hostError with Status 0.
func (c *connector) me(ctx context.Context) (meInfo, *hostError) {
	var me meInfo
	fctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	resp, err := c.fetch(fctx, "/me")
	if err != nil {
		he := c.asleep()
		he.Status = 0
		he.Message += " (" + err.Error() + ")"
		return me, &he
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		he := c.hostErr(resp)
		return me, &he
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me)
	return me, nil
}

func (c *connector) path(ctx context.Context) (tunnel.Path, error) {
	c.mu.Lock()
	s := c.sess
	c.mu.Unlock()
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return s.Path(pctx)
}

// describe is the path line: the truth about the path, never a green dot (pm/BELIEFS.md).
func (c *connector) describe(p tunnel.Path) string {
	if p == (tunnel.Path{}) {
		return "unknown — the host did not answer a ping"
	}
	rtt := fmt.Sprintf("%.0f ms", float64(p.RTT)/float64(time.Millisecond))
	if p.RTT < 10*time.Millisecond {
		rtt = fmt.Sprintf("%.1f ms", float64(p.RTT)/float64(time.Millisecond))
	}
	if p.Direct {
		return "direct · " + rtt
	}
	return "relayed via " + cmp.Or(c.relayName, p.Via) + " · " + rtt
}

// keep re-measures the path every pathEvery and prints it when it changes; a check that fails is
// repeated soon, and a second failure in a row — or a request that found the session dead — has
// the session replaced, with backoff, under the same identity.
func (c *connector) keep(ctx context.Context, last tunnel.Path) {
	t := time.NewTicker(pathEvery)
	defer t.Stop()
	fails := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.wake:
			fails++
		}
		p, err := c.path(ctx)
		if err == nil {
			fails = 0
			t.Reset(pathEvery)
			c.setPath(p)
			if p.Direct != last.Direct || p.Via != last.Via {
				fmt.Fprintf(c.out, "path      %s\n", c.describe(p))
				last = p
			}
			continue
		}
		if fails++; fails < 2 {
			t.Reset(2 * time.Second)
			continue
		}
		last, fails = c.reconnect(ctx), 0
	}
}

func (c *connector) reconnect(ctx context.Context) tunnel.Path {
	c.mu.Lock()
	c.down = true
	s := c.sess
	c.mu.Unlock()
	fmt.Fprintf(c.out, "path      lost — the host stopped answering; reconnecting\n")
	for wait := time.Second; ; wait = min(2*wait, 30*time.Second) {
		rctx, cancel := context.WithTimeout(ctx, connectTimeout)
		t0 := time.Now()
		n, err := s.Redial(rctx)
		cancel()
		if err == nil {
			c.mu.Lock()
			c.sess, c.down, c.handshake = n, false, time.Since(t0)
			c.mu.Unlock()
			p, _ := c.path(ctx)
			c.setPath(p)
			fmt.Fprintf(c.out, "path      reconnected · %s\n", c.describe(p))
			return p
		}
		if ctx.Err() != nil {
			return tunnel.Path{}
		}
		c.logf("reconnect: %v — trying again in %s", err, wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return tunnel.Path{}
		}
	}
}

const connectHelp = `Usage: bunny-network connect <invite> [--listen 127.0.0.1:11435]

Uses an invite from this machine instead of the browser: opens the tunnel to the host once, then
serves an OpenAI-compatible API on loopback that any app can use — the OpenAI SDKs, curl, Open
WebUI, Cursor, Claude Code. The invite's key is added to every request, so the app needs none.

  bunny-network connect bn1.tc….…
  OPENAI_BASE_URL=http://127.0.0.1:11435/v1 OPENAI_API_KEY=x python3 app.py
  curl http://127.0.0.1:11435/v1/models

What reaches the host: /v1/* and /me, exactly as the browser sends them. Anything else is a 404
here. Errors come back in the OpenAI error format with the host's code and your words: paused,
revoked, asleep (no answer in 15s), rate limited (with Retry-After), busy.

The path line says how your bytes travel — relayed via a region, or direct once the two machines
found each other — and is re-checked every 30s. When the host stops answering, connect says so and
reconnects on its own.

Flags:
  --listen ADDR    loopback address to serve on (default 127.0.0.1:11435); other addresses are refused
  --log-requests   print one line per request: when, what, how long, how it ended — never the prompt
  --data-dir DIR   also serve an admin socket there, so "status --data-dir DIR [--watch]" shows this
                   bridge: the path, what is in flight, and the request stream
  --verbose        print the tunnel engine's log on the terminal
`
