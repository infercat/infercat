package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/usage"
)

// endpoint is which proxied POST a request is. The pipeline is the same; only the body shaping differs.
type endpoint string

const (
	chatEndpoint       endpoint = "/v1/chat/completions"
	embeddingsEndpoint endpoint = "/v1/embeddings"
	// modelsEndpoint has no stage list; it is put on the record so the settle table can say what
	// the request was (ticket 014 promise 5).
	modelsEndpoint endpoint = "/v1/models"
)

// countsAgainstRPM: RPM is the friend's message allowance, so only a call that asks the model to do
// work spends one of them. Listing the models an invite may use is bookkeeping the client does on
// connect; counting it made the friend's first message read "2 of 20 used this minute" (ticket 014
// ruling; measured on the real stack, 014 Log).
func (e endpoint) countsAgainstRPM() bool {
	return e == chatEndpoint || e == embeddingsEndpoint
}

// outcome is how a request ended, set by the stage that ended it (DESIGN §1.4). finish reads it
// through the settle table: it alone decides what the request counted and what it is charged.
type outcome uint8

const (
	outcomeNone      outcome = iota // still running
	outcomeRejected                 // refused before the queue: 4xx/5xx from stages 0–6, or the waiting set was full
	outcomeQueueLost                // client gone or QueueTimeout while holding a place in the queue
	outcomeEngineErr                // dial failed, or a non-2xx from the engine
	outcomeServed                   // 2xx relayed to the end (usage object seen or not)
	outcomeCut                      // the engine was asked (2xx started or not), then: client stopped reading, client gone, engine idle/error mid-stream
)

// normalized is what normalize returns (DESIGN §1.7): the friend's JSON as the engine will see it,
// plus the facts the later stages read instead of re-inspecting the map.
type normalized struct {
	body     map[string]any
	model    string
	stream   bool
	text     string   // the prompt text the count is over
	maxTok   int      // output cap in force after every shrink; 0 = none in force
	stripped []string // override keys removed, for the host's log
}

// request is one friend's request through the gateway: the record that owns every resource the
// request takes — the key's admission (per-key slot, RPM entry, token reservation), the buffered
// body, the global slot, the read, write, first-byte and idle deadlines, the upstream response —
// and has exactly one exit, finish, which releases them all in reverse order, settles the
// admission by the outcome, and records the usage event. serveHTTP defers finish, so success,
// rejection, client abort, timeout, and panic all leave through it; no stage releases anything
// itself (ticket 006 design ruling).
type request struct {
	g     *Gateway
	w     http.ResponseWriter
	r     *http.Request
	rc    *http.ResponseController
	start time.Time
	ev    usage.Event

	// Identity and input, filled stage by stage.
	key    *keys.Key
	kind   endpoint
	n      normalized
	prompt int // tokens in n.text: the pre-check estimate

	// Resources. Each is taken by one stage and released only by finish.
	adm            *admission // per-key slot, RPM entry, reservation (limiter.admit, .reserve)
	buffered       bool       // counted in g.bodies
	slot           bool       // holds a global slot
	cancelUpstream context.CancelFunc
	idle           *time.Timer // engine idle deadline, armed by relay
	resp           *http.Response

	// Response state.
	outcome     outcome
	wroteHeader bool
	ttftSet     bool
	noEvent     bool        // 401: nothing to record (promise 7b)
	stalled     bool        // the body read deadline fired: net/http cancelled r.Context, the friend is still there
	engineIdle  atomic.Bool // the idle deadline fired: the engine, not the friend, stopped
}

func (g *Gateway) newRequest(w http.ResponseWriter, r *http.Request) *request {
	q := &request{g: g, w: w, r: r, rc: http.NewResponseController(w), start: time.Now()}
	q.ev.TS = q.start
	q.ev.Endpoint = r.URL.Path
	return q
}

// serve authenticates, then routes. Every route leaves through finish.
func (q *request) serve() {
	if err := q.authenticate(); err != nil {
		q.fail(err)
		return
	}
	r := q.r
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/me":
		q.me()
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		q.models()
	case r.Method == http.MethodPost && r.URL.Path == string(chatEndpoint):
		q.proxy(chatEndpoint)
	case r.Method == http.MethodPost && r.URL.Path == string(embeddingsEndpoint):
		q.proxy(embeddingsEndpoint)
	default:
		q.fail(errf(CodeNotFound, 0, "no route for %s %s", r.Method, r.URL.Path))
	}
}

// proxy is the pipeline for a proxied POST, top to bottom. Each stage takes what it needs onto the
// record and returns the first error; finish gives everything back. The order is the protection
// (promise 2): the cheap in-memory admission bounds a key's burst before a byte of body is read or
// the engine is asked to tokenize, and nothing waits for a global slot before its budgets are known.
// A stage that ends the request past the queue sets the outcome; one that fails before it leaves
// it unset, which is a rejection.
func (q *request) proxy(kind endpoint) {
	q.kind = kind
	for _, stage := range []func() *gwError{
		q.checkHealth,  // the engine is up (cheapest; consumes nothing when it is not)
		q.admitKey,     // per-key concurrency + RPM
		q.readBody,     // under the read deadline, into the record
		q.normalize,    // strip override aliases, fill model, clamp max_tokens, stream_options
		q.count,        // tokenize the prompt: an engine call, bounded by the per-key slot
		q.checkBudgets, // context, TPM, daily: shrink max_tokens to fit or reject; reserve the worst case
		q.acquireSlot,  // global slot: FIFO, bounded wait, bounded waiting set
		q.callUpstream, // the engine, no redirects, under the first-byte deadline; 4xx is the friend's
		q.relay,        // stream or body to the friend under the idle and write deadlines
	} {
		if err := stage(); err != nil {
			if q.outcome == outcomeNone {
				q.outcome = outcomeRejected
			}
			q.fail(err)
			return
		}
	}
	q.outcome = outcomeServed
}

// ---- stages ----

// authenticate resolves the bearer secret through the store on every request (no caching: hot reload
// is the store's job). The secret is never logged, never echoed, never put in an event. A paused or
// revoked key is still put on the record so the rejection is recorded against it and last_seen moves
// (promise 7a); a request with no valid key records nothing at all (7b).
func (q *request) authenticate() *gwError {
	secret, ok := bearer(q.r.Header.Get("Authorization"))
	if !ok {
		q.noEvent = true
		return errf(CodeInvalidKey, 0, "missing or malformed Authorization header; expected: Bearer <invite secret>")
	}
	k, found, err := q.g.store.Lookup(q.r.Context(), secret)
	if err != nil {
		q.g.logf("gateway: key store lookup failed: %v", err)
		return errf(CodeUpstreamDown, 1, "the host's key store is unavailable; try again")
	}
	if !found {
		q.noEvent = true
		return errf(CodeInvalidKey, 0, "unknown key; check the invite")
	}
	q.key = k
	q.ev.KeyID = k.ID
	q.g.lim.touch(k.ID)
	switch k.Status {
	case keys.Active:
		return nil
	case keys.Revoked:
		return errf(CodeKeyRevoked, 0, "this key has been revoked by the host")
	default: // Paused, or anything unexpected: fail closed as paused
		return errf(CodeKeyPaused, 0, "this key is paused by the host")
	}
}

func (q *request) checkHealth() *gwError {
	if !q.g.up.Info().Health.OK {
		return errf(CodeUpstreamDown, retryAfterUpstreamDown, "the host's engine is not reachable right now")
	}
	return nil
}

// admitKey takes the per-key slot and the RPM entry onto the record; finish settles them by the
// outcome (a rejection hands them back un-counted: a friend retrying against a 4xx does not dig
// the hole deeper).
func (q *request) admitKey() *gwError {
	a, err := q.g.lim.admit(q.key.ID, q.key.Limits)
	if err != nil {
		return err
	}
	q.adm = a
	return nil
}

// readBody reads the body into the record under the body cap and the read deadline serveHTTP armed:
// a declared Content-Length over the cap is refused before a byte is read; an undeclared one is cut
// off by MaxBytesReader; a body that does not arrive in time is a 400 (promise 4).
func (q *request) readBody() *gwError {
	limit := q.g.maxBody
	if q.r.ContentLength > limit {
		return errf(CodeBodyTooLarge, 0, "request body is %d bytes; the limit is %d", q.r.ContentLength, limit)
	}
	q.buffered = true
	q.g.bodies.Add(1)
	raw, err := io.ReadAll(http.MaxBytesReader(q.w, q.r.Body, limit))
	if err != nil {
		var mbe *http.MaxBytesError
		switch {
		case errors.As(err, &mbe):
			return errf(CodeBodyTooLarge, 0, "request body exceeds the limit of %d bytes", limit)
		case errors.Is(err, os.ErrDeadlineExceeded):
			q.stalled = true
			return errf(CodeInvalidRequest, 0, "request body was not received within %s", q.g.readTimeout)
		}
		return errf(CodeInvalidRequest, 0, "reading request body: %v", err)
	}
	// Body in hand: clear the read deadline. Left armed, net/http's background read (which starts at
	// body EOF) would hit it during a long stream and cancel the request as a client disconnect.
	_ = q.rc.SetReadDeadline(time.Time{})
	q.n.body, err = decodeObject(raw)
	if err != nil {
		return errf(CodeInvalidRequest, 0, "request body must be a JSON object")
	}
	return nil
}

// normalize is the one place the friend's JSON becomes the engine's (DESIGN §1.7): the function
// returns a value; the stage puts it on the record and the event. Shrink-to-fit (005) needs the
// token count and so runs in checkBudgets.
func (q *request) normalize() *gwError {
	n, err := normalize(q.kind, q.n.body, q.key, q.g.up.Info().Models)
	if err != nil {
		return err
	}
	q.n = n
	if len(n.stripped) > 0 {
		q.g.logf("gateway: removed %v from a request by key %s", n.stripped, q.key.ID)
	}
	q.ev.Model, q.ev.Stream = n.model, n.stream
	if q.g.cfg.LogPrompts {
		q.ev.Prompt = n.text
	}
	return nil
}

// count tokenizes the prompt through the engine (an estimate when it cannot). The provisional
// prompt count on the event is replaced by the engine's usage when the response carries one.
func (q *request) count() *gwError {
	q.prompt = q.countTokens(q.n.text)
	q.ev.PromptTokens = q.prompt
	return nil
}

// checkBudgets: the prompt must fit the effective context (max_tokens shrinks to what remains: 005),
// then the request's worst case — prompt + max_tokens — must fit TPM and the daily budget the same
// way (DESIGN §1.4: max_tokens shrinks to what the window has left, floor 16, else 429 with the
// numbers), and exactly that worst case is reserved so concurrent requests from one key see each
// other; finish settles the reservation to what the request cost. A chat with no cap anywhere is
// unbounded, so a ceiling that is set becomes its cap.
func (q *request) checkBudgets() *gwError {
	if err := q.fitContext(); err != nil {
		return err
	}
	out := q.n.maxTok
	if q.kind == chatEndpoint && out == 0 {
		out = -1
	}
	fitted, err := q.g.lim.reserve(q.adm, q.key.Limits, q.prompt, out)
	if err != nil {
		return err
	}
	if fitted > 0 && fitted != q.n.maxTok {
		q.setMaxTok(fitted)
	}
	return nil
}

// acquireSlot joins the global queue (DESIGN §1.5). Refused on the spot is a rejection (no place
// was held); a place held and lost is QueueLost — the settle table counts the timeout, not the
// friend leaving.
func (q *request) acquireSlot() *gwError {
	qstart := time.Now()
	o, err := q.g.queue.acquire(q.r.Context(), q.g.queueTimeout)
	q.ev.QueuedMS = time.Since(qstart).Milliseconds()
	if err != nil {
		q.outcome = o
		return err
	}
	q.slot = true
	return nil
}

// callUpstream sends the normalized body through the engine seam (DESIGN §3.4: the engine adds
// its bearer, refuses redirects — promise 6 — and bounds its own first byte, §1.6). A 2xx puts the
// response on the record; anything else is the engine's outcome — unless the friend left while
// the engine was working for them, which is Cut.
func (q *request) callUpstream() *gwError {
	payload, err := json.Marshal(q.n.body)
	if err != nil {
		return errf(CodeInvalidRequest, 0, "request body could not be re-encoded: %v", err)
	}
	ctx, cancel := context.WithCancel(q.r.Context())
	q.cancelUpstream = cancel
	resp, derr := q.g.up.Do(ctx, http.MethodPost, string(q.kind), payload, q.n.stream)
	if derr != nil {
		if q.r.Context().Err() != nil {
			q.outcome = outcomeCut
			return errf(CodeClientClosed, 0, "client went away")
		}
		q.outcome = outcomeEngineErr
		return q.upstreamErr(derr)
	}
	q.resp = resp
	if resp.StatusCode/100 != 2 {
		q.outcome = outcomeEngineErr
		return upstreamStatusErr(resp)
	}
	return nil
}

// relay pipes the engine's response to the friend under the engine idle deadline (DESIGN §1.6: a
// timer that cancels the upstream, re-armed by every read) and the per-line client write deadline.
// The pipes set the outcome when either party stops; reaching the end is Served.
func (q *request) relay() *gwError {
	q.idle = time.AfterFunc(q.g.idleTimeout, func() {
		q.engineIdle.Store(true)
		q.cancelUpstream()
	})
	body := idleReader{r: q.resp.Body, t: q.idle, d: q.g.idleTimeout}
	if q.n.stream {
		return q.pipeStream(body)
	}
	return q.pipeBody(body)
}

// ---- the one exit ----

// finish releases everything the request holds, in reverse order of acquisition, settles the
// admission by the settle table, and records the usage event. It is deferred by serveHTTP and is
// the only way out.
func (q *request) finish() {
	if q.idle != nil {
		q.idle.Stop()
	}
	if q.resp != nil {
		q.resp.Body.Close()
	}
	if q.cancelUpstream != nil {
		q.cancelUpstream()
	}
	if q.slot {
		q.g.queue.release()
	}
	if q.adm != nil {
		counted, charged := q.settleRow()
		q.g.lim.settle(q.adm, counted, charged)
	}
	if q.buffered {
		q.g.bodies.Add(-1)
	}
	if q.noEvent {
		return
	}
	q.ev.TotalMS = time.Since(q.start).Milliseconds()
	if len(q.ev.Endpoint) > maxEndpointLen { // the path is the friend's to choose; the log line is not
		q.ev.Endpoint = q.ev.Endpoint[:maxEndpointLen]
	}
	if q.g.rec != nil {
		q.g.rec.Record(context.WithoutCancel(q.r.Context()), q.ev)
	}
}

// settleRow is the settle table (DESIGN §1.4), read by finish: whether the request counted against
// RPM and what it is charged against TPM and the daily budget.
//
//	Any /v1/models call               not counted; 0 (RPM is the message allowance — 014 promise 5)
//	Rejected                          not counted; charged 0 (the reservation is released)
//	QueueLost, timed out              counted (a place was held); 0
//	QueueLost, client gone            not counted; 0
//	EngineErr                         counted; 0
//	Served, usage object seen         counted; prompt + completion as reported
//	Served, no usage object (stream)  counted; pre-check prompt + delta chunks seen (002's blessed deviation)
//	Cut, stream                       counted; pre-check prompt + delta chunks seen
//	Cut, non-stream (client gone)     counted; the reservation — the engine did the work
//
// A request that never reached an outcome (the pipeline was abandoned by a panic) is a rejection.
// The event keeps what was observed; only the non-stream Cut charge differs from its token sum.
func (q *request) settleRow() (counted bool, charged int) {
	if !q.kind.countsAgainstRPM() {
		return false, 0
	}
	switch q.outcome {
	case outcomeQueueLost:
		return q.ev.Code == string(CodeQueueTimeout), 0
	case outcomeEngineErr:
		return true, 0
	case outcomeServed:
		return true, q.ev.PromptTokens + q.ev.CompletionTokens
	case outcomeCut:
		if !q.ev.Stream {
			return true, q.adm.reserved
		}
		return true, q.ev.PromptTokens + q.ev.CompletionTokens
	}
	return false, 0
}

// fail writes e as the response: a full error response if nothing was written yet, an SSE error
// event if a stream is under way, or nothing if the friend has already gone (recorded as 499).
func (q *request) fail(e *gwError) {
	if q.wroteHeader {
		q.ev.Code = string(e.Code)
		if e.Code != CodeClientClosed && q.r.Context().Err() == nil {
			writeStreamError(q.w, e)
		}
		return
	}
	if e.Code == CodeClientClosed || (q.r.Context().Err() != nil && !q.stalled) {
		q.ev.Status, q.ev.Code = 499, string(CodeClientClosed)
		return
	}
	q.ev.Status, q.ev.Code = e.Status(), string(e.Code)
	q.armWrite()
	writeError(q.w, e)
}

func (q *request) writeHeader(status int) {
	q.wroteHeader = true
	q.ev.Status = status
	q.armWrite()
	q.w.WriteHeader(status)
}

// armWrite gives the next write or flush to the friend writeTimeout to complete (promise 3). Called
// before every response and re-armed per stream line, so a friend who stops reading fails the write
// and the request leaves through finish; a friend who keeps reading is never cut. net/http clears the
// deadline when the handler returns.
func (q *request) armWrite() {
	_ = q.rc.SetWriteDeadline(time.Now().Add(q.g.writeTimeout))
}

func (q *request) markTTFT() {
	if !q.ttftSet {
		q.ttftSet = true
		q.ev.TTFTMS = time.Since(q.start).Milliseconds()
	}
}
