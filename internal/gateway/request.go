package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/usage"
)

// endpoint is which proxied POST a request is. The pipeline is the same; only the body shaping differs.
type endpoint string

const (
	chatEndpoint       endpoint = "/v1/chat/completions"
	embeddingsEndpoint endpoint = "/v1/embeddings"
)

// request is one friend's request through the gateway: the record that owns every resource the
// request takes — the key's admission (per-key slot and RPM entry), the buffered body, the tokens
// reserved against TPM and the daily budget, the global slot, the read and write deadlines, the
// upstream response — and has exactly one exit, finish, which releases them all in reverse order and
// records the usage event. serveHTTP defers finish, so success, rejection, client abort, timeout, and
// panic all leave through it; no stage releases anything itself (ticket 006 design ruling).
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
	body   map[string]any // decoded and normalized
	text   string         // the prompt text the count is over
	prompt int            // tokens in text: the pre-check estimate
	maxTok int            // output cap in force, 0 = none

	// Resources. Each is taken by one stage and released only by finish.
	admitted       bool // per-key slot and RPM entry (limiter.admit)
	queued         bool // reached acquireSlot: the admission counts even if the request fails after
	buffered       bool // counted in g.bodies
	reserved       int  // tokens reserved by checkBudgets, settled against the engine's usage
	releaseSlot    func()
	cancelUpstream context.CancelFunc
	resp           *http.Response

	// Response state.
	wroteHeader bool
	ttftSet     bool
	noEvent     bool // 401: nothing to record (promise 7b)
	stalled     bool // the body read deadline fired: net/http cancelled r.Context, the friend is still there
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
func (q *request) proxy(kind endpoint) {
	q.kind = kind
	for _, stage := range []func() *gwError{
		q.checkHealth,  // the engine is up (cheapest; consumes nothing when it is not)
		q.admitKey,     // per-key concurrency + RPM
		q.readBody,     // under the read deadline, into the record
		q.normalize,    // strip override aliases, fill model, clamp max_tokens, stream_options
		q.count,        // tokenize the prompt: an engine call, bounded by the per-key slot
		q.checkBudgets, // context (reject or shrink-to-fit), then reserve the estimate against TPM/daily
		q.acquireSlot,  // global slot: bounded wait, bounded waiting set
		q.callUpstream, // the engine, no redirects; 4xx is the friend's, the rest is the host's
		q.relay,        // stream or body to the friend under a per-line write deadline
	} {
		if err := stage(); err != nil {
			q.fail(err)
			return
		}
	}
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
	if !q.g.up.Info().Healthy {
		return errf(CodeUpstreamDown, retryAfterUpstreamDown, "the host's engine is not reachable right now")
	}
	return nil
}

// admitKey takes the per-key slot and the RPM entry. Until acquireSlot marks the request queued, a
// rejection hands them back un-counted (a friend retrying against a 4xx does not dig the hole deeper).
func (q *request) admitKey() *gwError {
	if err := q.g.lim.admit(q.key.ID, q.key.Limits); err != nil {
		return err
	}
	q.admitted = true
	return nil
}

// readBody reads the body into the record under MaxBody and the read deadline serveHTTP armed: a
// declared Content-Length over the cap is refused before a byte is read; an undeclared one is cut
// off by MaxBytesReader; a body that does not arrive in time is a 400 (promise 4).
func (q *request) readBody() *gwError {
	limit := q.g.cfg.MaxBody
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
	q.body, err = decodeObject(raw)
	if err != nil {
		return errf(CodeInvalidRequest, 0, "request body must be a JSON object")
	}
	return nil
}

// normalize is the one place the body is shaped before the engine sees it: engine-override aliases
// are stripped (promise 1, overrideKeys), a missing model is filled and the allowlist enforced, the
// output cap is clamped to the key's, and streams get include_usage. Shrink-to-fit (005) needs the
// token count and so runs in checkBudgets.
func (q *request) normalize() *gwError {
	if removed := stripOverrides(q.body); len(removed) > 0 {
		q.g.logf("gateway: removed %v from a request by key %s", removed, q.key.ID)
	}
	model, err := q.resolveModel()
	if err != nil {
		return err
	}
	q.ev.Model = model
	switch q.kind {
	case chatEndpoint:
		q.ev.Stream, _ = q.body["stream"].(bool)
		q.text = messagesText(q.body["messages"])
		q.maxTok = clampMaxTokens(q.body, q.key.Limits)
		if q.ev.Stream {
			setIncludeUsage(q.body)
		}
	case embeddingsEndpoint:
		q.text = inputText(q.body["input"])
	}
	if q.g.cfg.LogPrompts {
		q.ev.Prompt = q.text
	}
	return nil
}

// count tokenizes the prompt through the engine (an estimate when it cannot). The provisional
// prompt count on the event is replaced by the engine's usage when the response carries one.
func (q *request) count() *gwError {
	q.prompt = q.countTokens(q.text)
	q.ev.PromptTokens = q.prompt
	return nil
}

// checkBudgets: the prompt must fit the effective context (max_tokens shrinks to what remains: 005),
// then the prompt estimate is reserved against TPM and the daily budget so concurrent requests from
// one key see each other; finish settles the reservation to what the engine reported.
func (q *request) checkBudgets() *gwError {
	if err := q.fitContext(); err != nil {
		return err
	}
	if err := q.g.lim.reserve(q.key.ID, q.key.Limits, q.prompt); err != nil {
		return err
	}
	q.reserved = q.prompt
	return nil
}

// acquireSlot joins the global queue. From here the request counts as one of the key's requests
// whatever happens: it has reached the engine's door.
func (q *request) acquireSlot() *gwError {
	q.queued = true
	qstart := time.Now()
	release, err := q.g.acquire(q.r.Context())
	q.ev.QueuedMS = time.Since(qstart).Milliseconds()
	if err != nil {
		return err
	}
	q.releaseSlot = release
	return nil
}

// callUpstream sends the normalized body to the engine under RequestTimeout (which also bounds the
// whole relay), never following a redirect (promise 6). A 2xx puts the response on the record.
func (q *request) callUpstream() *gwError {
	payload, err := json.Marshal(q.body)
	if err != nil {
		return errf(CodeInvalidRequest, 0, "request body could not be re-encoded: %v", err)
	}
	ctx, cancel := context.WithTimeout(q.r.Context(), q.g.cfg.RequestTimeout)
	q.cancelUpstream = cancel
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, q.upstreamURL(string(q.kind)), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if q.ev.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := q.doUpstream(req)
	if err != nil {
		return q.upstreamErr(err)
	}
	q.resp = resp
	if resp.StatusCode/100 != 2 {
		return upstreamStatusErr(resp)
	}
	return nil
}

// relay pipes the engine's response to the friend: SSE events flushed as they arrive, or the body
// whole. Tokens are charged for a 2xx (full or partial) response only, which finish reads off the
// event's status.
func (q *request) relay() *gwError {
	if q.ev.Stream {
		return q.pipeStream()
	}
	return q.pipeBody()
}

// ---- the one exit ----

// finish releases everything the request holds, in reverse order of acquisition, and records the
// usage event. It is deferred by serveHTTP and is the only way out.
func (q *request) finish() {
	if q.resp != nil {
		q.resp.Body.Close()
	}
	if q.cancelUpstream != nil {
		q.cancelUpstream()
	}
	if q.releaseSlot != nil {
		q.releaseSlot()
	}
	if q.admitted {
		if q.queued {
			charged := 0
			if q.wroteHeader && q.ev.Status/100 == 2 {
				charged = q.ev.PromptTokens + q.ev.CompletionTokens
			}
			q.g.lim.release(q.key.ID, q.reserved, charged)
		} else {
			q.g.lim.abort(q.key.ID, q.reserved)
		}
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
