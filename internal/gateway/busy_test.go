package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Ticket 018: a busy host is not an asleep host. A streaming request that has to wait for a slot
// gets its response head at once and a `: queued` comment now and every keepalive, so the friend's
// client can tell "in line" from "not there"; the queue timeout then arrives inside that stream.
// Non-streaming requests, and every request refused before the queue, keep their 503.

// queuedStream opens a streaming chat for the harness key while bob holds the only slot, and
// returns the response, a reader positioned at its first event, and when it was sent.
func queuedStream(t *testing.T, h *harness, ctx context.Context) (*http.Response, *bufio.Reader, time.Time) {
	t.Helper()
	start := time.Now()
	res, err := h.streamReq(ctx, chatBody("m1", 1, `"stream":true`))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res, bufio.NewReader(res.Body), start
}

// Promise 1: the head and the first `: queued` are out within 100 ms of admission, the session's
// TTFT is still the engine's first byte, and the relay follows on the same response.
func TestQueuedStreamHeadAtOnce(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.queueTimeout = 5 * time.Second
	h.up.set("sse", sseEvents(200, true)...)
	release := h.hold(h.bob())

	res, br, sent := queuedStream(t, h, context.Background())
	headAt := time.Since(sent)
	first, err := readEvent(br)
	firstAt := time.Since(sent)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" || res.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("queued stream head: %d %v", res.StatusCode, res.Header)
	}
	if first != ": queued\n\n" {
		t.Fatalf("first bytes of a queued stream: %q", first)
	}
	if headAt > 100*time.Millisecond || firstAt > 100*time.Millisecond {
		t.Fatalf("head after %s, first comment after %s: not at once", headAt, firstAt)
	}
	if in, w := h.gw.Queue(); in != 1 || w != 1 {
		t.Fatalf("Queue() = %d, %d; want bob running and alice waiting", in, w)
	}

	// The slot frees: the engine's stream follows on the same response, byte for byte, to [DONE].
	time.Sleep(30 * time.Millisecond) // a wait long enough to show in queued_ms
	h.shortStreams()
	release()
	rest, _ := io.ReadAll(br)
	if !strings.HasPrefix(string(rest), `data: {"id":"c"`) || !strings.HasSuffix(strings.TrimSpace(string(rest)), "data: [DONE]") {
		t.Fatalf("relay after the queue: %q", rest)
	}
	ev := h.eventFor("k_alice1")
	if ev.Status != 200 || ev.Code != "" || !ev.Stream || ev.QueuedMS < 30 || ev.TTFTMS < ev.QueuedMS || ev.CompletionTokens != 1 {
		t.Fatalf("queued-then-served event: %+v", ev)
	}
	if c := h.gw.Counters("k_alice1"); c.RPMUsed != 1 || c.InFlight != 0 {
		t.Fatalf("counters: %+v", c)
	}
	h.clean()
}

// Promise 1: a comment every keepalive interval while waiting. The interval is shortened the way
// the deadlines are in this suite; the constant a host gets is asserted by value.
func TestQueuedStreamKeepalive(t *testing.T) {
	if defaultQueuedEvery != 5*time.Second {
		t.Fatalf("keepalive interval is %s; the contract says 5 s", defaultQueuedEvery)
	}
	h := newHarness(t, Config{}, nil)
	h.gw.queueTimeout = 5 * time.Second
	h.gw.queuedEvery = 100 * time.Millisecond
	h.up.set("sse", sseEvents(200, true)...)
	release := h.hold(h.bob())

	_, br, _ := queuedStream(t, h, context.Background())
	start := time.Now()
	var at []time.Duration
	for len(at) < 5 {
		e, err := readEvent(br)
		if err != nil {
			t.Fatal(err)
		}
		if e != ": queued\n\n" {
			t.Fatalf("event %d while queued: %q", len(at), e)
		}
		at = append(at, time.Since(start))
	}
	// One at once, then one per interval: the fifth lands around 400 ms, never before the fourth tick.
	if at[0] > 50*time.Millisecond || at[4] < 380*time.Millisecond || at[4] > 900*time.Millisecond {
		t.Fatalf("keepalives at %v; want the first at once and then every ~100 ms", at)
	}
	for i := 1; i < len(at); i++ {
		if gap := at[i] - at[i-1]; gap < 80*time.Millisecond {
			t.Fatalf("keepalives %d and %d only %s apart: %v", i-1, i, gap, at)
		}
	}
	h.shortStreams()
	release()
	if rest, _ := io.ReadAll(br); !strings.HasSuffix(strings.TrimSpace(string(rest)), "data: [DONE]") {
		t.Fatalf("stream after the keepalives: %q", rest)
	}
	h.clean()
}

// Promise 1: a queued stream that times out ends with an SSE error event carrying the code and
// retry_after, then [DONE]; the settle table's row is unchanged — counted, charged 0.
func TestQueuedStreamTimeoutIsAnSSEError(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.queueTimeout = 200 * time.Millisecond
	h.up.set("sse", sseEvents(200, true)...)
	release := h.hold(h.bob())
	defer release()

	res, br, _ := queuedStream(t, h, context.Background())
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var events []string
	for {
		e, err := readEvent(br)
		if e != "" {
			events = append(events, e)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			break
		}
	}
	if len(events) != 3 || events[0] != ": queued\n\n" || events[2] != "data: [DONE]\n\n" {
		t.Fatalf("queued stream that timed out: %q", events)
	}
	var body errorBody
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(events[1]), "data: ")), &body); err != nil {
		t.Fatalf("error event %q: %v", events[1], err)
	}
	if body.Error.Code != CodeQueueTimeout || body.Error.Type != "upstream_error" || body.Error.RetryAfter != retryAfterQueueTimeout || !strings.Contains(body.Error.Message, "waited 200ms") {
		t.Fatalf("error event: %+v", body.Error)
	}
	seenCodesMu.Lock()
	seenCodes[body.Error.Code] = true
	seenCodesMu.Unlock()
	ev := h.eventFor("k_alice1")
	if ev.Status != 200 || ev.Code != string(CodeQueueTimeout) || !ev.Stream || ev.QueuedMS < 150 || ev.TTFTMS != 0 {
		t.Fatalf("queue-timeout event: %+v", ev)
	}
	// QueueLost, timed out: counted (a place was held), charged nothing (DESIGN §1.4).
	if c := h.gw.Counters("k_alice1"); c.RPMUsed != 1 || c.TodayTokens != 0 || c.InFlight != 0 {
		t.Fatalf("settle row for a stream queue timeout: %+v", c)
	}
	if in, w := h.gw.Queue(); in != 1 || w != 0 {
		t.Fatalf("Queue() = %d, %d after the timeout", in, w)
	}
}

// The non-stream path is untouched: a queued JSON request still gets today's 503 with the header
// and no retry_after in the body; a stream refused before the queue (waiting set full) does too.
func TestQueuedNonStreamUnchanged(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.queueTimeout = 150 * time.Millisecond
	h.up.set("sse", sseEvents(200, true)...)
	release := h.hold(h.bob())
	defer release()

	r := h.post("/v1/chat/completions", chatBody("m1", 1, ""))
	h.expectErr(r, CodeQueueTimeout)
	if r.header.Get("Retry-After") != "5" || strings.Contains(string(r.body), "retry_after") {
		t.Fatalf("non-stream queue timeout: %v %s", r.header, r.body)
	}
	if ev := h.eventFor("k_alice1"); ev.Status != 503 || ev.Code != string(CodeQueueTimeout) {
		t.Fatalf("non-stream event: %+v", ev)
	}

	// Two streams may wait (max(2, 2×1)); a third is refused at the door, head and all.
	h.gw.queueTimeout = 5 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 2; i++ {
		_, br, _ := queuedStream(t, h, ctx)
		if e, _ := readEvent(br); e != ": queued\n\n" {
			t.Fatalf("waiter %d: %q", i, e)
		}
	}
	h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, `"stream":true`)), CodeQueueTimeout)
	if in, w := h.gw.Queue(); in != 1 || w != 2 {
		t.Fatalf("Queue() = %d, %d", in, w)
	}
}

// A friend who leaves while queued drops their place: not counted, the slot never handed to them.
// The keepalive is what a leaving friend fails — at the queue level, a write that fails ends the
// wait as client_closed, and a slot handed over in that same instant is handed on, never leaked.
func TestQueuedClientGoneFreesThePlace(t *testing.T) {
	t.Run("through the pipeline", func(t *testing.T) {
		h := newHarness(t, Config{}, nil)
		h.gw.queueTimeout = 5 * time.Second
		h.up.set("sse", sseEvents(200, true)...)
		release := h.hold(h.bob())

		ctx, leave := context.WithCancel(context.Background())
		_, br, _ := queuedStream(t, h, ctx)
		if e, _ := readEvent(br); e != ": queued\n\n" {
			t.Fatalf("queued: %q", e)
		}
		leave()
		waitUntil(t, 3*time.Second, "alice's place to be dropped", func() bool { _, w := h.gw.Queue(); return w == 0 })
		if ev := h.eventFor("k_alice1"); ev.Code != string(CodeClientClosed) {
			t.Fatalf("event for a friend who left the queue: %+v", ev)
		}
		if c := h.gw.Counters("k_alice1"); c.RPMUsed != 0 || c.InFlight != 0 {
			t.Fatalf("a friend who left is not counted: %+v", c)
		}
		h.shortStreams()
		release()
		waitUntil(t, 3*time.Second, "the slot to go back to the pool", func() bool { in, _ := h.gw.Queue(); return in == 0 })
		if n := h.up.requests.Load(); n != 1 {
			t.Fatalf("the engine saw %d requests; the dead one must never reach it", n)
		}
		h.clean()
	})
	t.Run("at the queue: a failing keepalive", func(t *testing.T) {
		q := &slotQueue{cap: func() int { return 1 }}
		if o, err := q.acquire(context.Background(), time.Second, time.Hour, nil); o != outcomeNone || err != nil {
			t.Fatalf("first acquire: %v %v", o, err)
		}
		calls := 0
		o, err := q.acquire(context.Background(), time.Second, 20*time.Millisecond, func() error {
			calls++
			if calls == 3 {
				return errors.New("write: broken pipe")
			}
			return nil
		})
		if o != outcomeQueueLost || err == nil || err.Code != CodeClientClosed || calls != 3 {
			t.Fatalf("failing keepalive: %v %v after %d calls", o, err, calls)
		}
		if in, w := q.counts(); in != 1 || w != 0 {
			t.Fatalf("counts after the waiter left: %d, %d", in, w)
		}
		// The race: the slot is handed over during the very keepalive whose write fails.
		o, err = q.acquire(context.Background(), time.Second, time.Hour, func() error {
			q.release() // the holder finishes now: fill hands the slot to this waiter
			return errors.New("write: connection reset")
		})
		if o != outcomeQueueLost || err == nil || err.Code != CodeClientClosed {
			t.Fatalf("keepalive failing as the slot arrives: %v %v", o, err)
		}
		if in, w := q.counts(); in != 0 || w != 0 {
			t.Fatalf("a slot handed to a dead waiter must be handed on: %d, %d", in, w)
		}
	})
}
