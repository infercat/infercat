package gateway

// Ticket 010: the invariant tables DESIGN §1.8 lists as missing — I1 (exactly-once release over
// every exit), I5 (the token ceiling under random interleavings), I6 (the settle table, one row per
// outcome), I7 (FIFO order and exact resize), I8 (the deadline fixtures), I9 (the normalization
// post-conditions, §1.7). Each test is a table; each row is one fact.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/upstream"
	"github.com/2185Lab/bunny-network/internal/usage"
)

// leaks names every counter that must be zero once nothing is in flight: bodies buffered, global
// slots held or waited for, per-key in-flight, live reservations. Negative is as wrong as positive.
func (h *harness) leaks() string {
	var sb strings.Builder
	if b := h.gw.bodies.Load(); b != 0 {
		fmt.Fprintf(&sb, "bodies=%d ", b)
	}
	if inF, w := h.gw.Queue(); inF != 0 || w != 0 {
		fmt.Fprintf(&sb, "queue=%d/%d ", inF, w)
	}
	h.gw.lim.mu.Lock()
	states := map[string]*keyState{}
	for id, st := range h.gw.lim.keys {
		states[id] = st
	}
	h.gw.lim.mu.Unlock()
	for id, st := range states {
		st.mu.Lock()
		if st.inFlight != 0 || st.reserved != 0 {
			fmt.Fprintf(&sb, "%s: inFlight=%d reserved=%d ", id, st.inFlight, st.reserved)
		}
		st.mu.Unlock()
	}
	return sb.String()
}

// clean waits for finish (which runs after the response is written) to have released everything.
func (h *harness) clean() {
	h.t.Helper()
	waitUntil(h.t, 3*time.Second, "every resource to be released: "+h.leaks(), func() bool { return h.leaks() == "" })
}

// hold opens a stream that holds a global slot until cancel is called; it returns once the engine
// has the request. Key "" is the harness key.
func (h *harness) hold(auth string) (cancel func()) {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader(chatBody("m1", 1, `"stream":true`)))
	req.Header.Set("Content-Type", "application/json")
	if auth == "" {
		auth = "Bearer " + testSecret
	}
	req.Header.Set("Authorization", auth)
	before := h.up.requests.Load()
	res, err := h.srv.Client().Do(req)
	if err != nil {
		cancel()
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { cancel(); res.Body.Close() })
	waitUntil(h.t, 3*time.Second, "the held stream to reach the engine", func() bool { return h.up.requests.Load() > before })
	return cancel
}

// bob is a second key with no limits, so the harness key's counters are the ones under test.
func (h *harness) bob() string {
	h.store.set("second", &keys.Key{ID: "k_bob", Name: "bob", Status: keys.Active})
	return "Bearer second"
}

// eventFor waits for the newest usage event recorded against keyID.
func (h *harness) eventFor(keyID string) usage.Event {
	h.t.Helper()
	var ev usage.Event
	waitUntil(h.t, 3*time.Second, "a usage event for "+keyID, func() bool {
		h.rec.mu.Lock()
		defer h.rec.mu.Unlock()
		for i := len(h.rec.events) - 1; i >= 0; i-- {
			if h.rec.events[i].KeyID == keyID {
				ev = h.rec.events[i]
				return true
			}
		}
		return false
	})
	return ev
}

// shortStreams makes every stream the engine starts from now on end almost at once, so waiters
// that were parked behind a long one finish quickly once it is cancelled.
func (h *harness) shortStreams() { h.up.set("sse", sseEvents(1, true)...) }

// ---- I1: exactly-once release ----

// For every way a request can end, after the handler returns nothing is held: bodies, global
// slots, waiters, per-key in-flight, reservations — and the next request from the key goes through.
func TestI1ExactlyOnceRelease(t *testing.T) {
	long := func(h *harness) { h.up.set("sse", sseEvents(200, true)...) }
	rows := []struct {
		name string
		run  func(t *testing.T) *harness
	}{
		{"401 invalid_key", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.expectErr(h.do(http.MethodPost, "/v1/chat/completions", "Bearer nope", chatBody("m1", 1, "")), CodeInvalidKey)
			return h
		}},
		{"403 key_paused", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.setKey(func(k *keys.Key) { k.Status = keys.Paused })
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeKeyPaused)
			h.setKey(func(k *keys.Key) { k.Status = keys.Active })
			return h
		}},
		{"403 key_revoked", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.setKey(func(k *keys.Key) { k.Status = keys.Revoked })
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeKeyRevoked)
			h.setKey(func(k *keys.Key) { k.Status = keys.Active })
			return h
		}},
		{"404 not_found", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.expectErr(h.post("/v1/nope", "{}"), CodeNotFound)
			return h
		}},
		{"403 model_not_allowed", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.setKey(func(k *keys.Key) { k.Limits.Models = []string{"m2"} })
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeModelNotAllowed)
			h.setKey(func(k *keys.Key) { k.Limits.Models = nil })
			return h
		}},
		{"413 body_too_large", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.gw.maxBody = 100
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 60, "")), CodeBodyTooLarge)
			h.gw.maxBody = defaultMaxBody
			return h
		}},
		{"400 invalid_request", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.expectErr(h.post("/v1/chat/completions", `{"model":`), CodeInvalidRequest)
			return h
		}},
		{"422 context_too_long", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 10 })
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 50, "")), CodeContextTooLong)
			h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 0 })
			return h
		}},
		{"429 rate_limited (rpm)", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.setKey(func(k *keys.Key) { k.Limits.RPM = 1 })
			h.post("/v1/chat/completions", chatBody("m1", 1, ""))
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeRateLimited)
			h.setKey(func(k *keys.Key) { k.Limits.RPM = 0 })
			return h
		}},
		{"429 rate_limited (tpm)", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.setKey(func(k *keys.Key) { k.Limits.TPM = 20 })
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 10, "")), CodeRateLimited) // 10 + 16 > 20
			h.setKey(func(k *keys.Key) { k.Limits.TPM = 0 })
			return h
		}},
		{"429 budget_exhausted", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.setKey(func(k *keys.Key) { k.Limits.DailyTokens = 20 })
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 10, "")), CodeBudgetExhausted)
			h.setKey(func(k *keys.Key) { k.Limits.DailyTokens = 0 })
			return h
		}},
		{"429 concurrency_limited", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.slots(4)
			h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 1 })
			long(h)
			cancel := h.hold("")
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeConcurrencyLimited)
			cancel()
			h.setKey(func(k *keys.Key) { k.Limits.MaxConcurrent = 0 })
			h.up.set("json")
			return h
		}},
		{"503 upstream_down (unhealthy)", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.up.setInfo(func(i *upstream.Info) { i.Health.OK = false })
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeUpstreamDown)
			h.up.setInfo(func(i *upstream.Info) { i.Health.OK = true })
			return h
		}},
		{"503 upstream_down (unreachable)", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, newDeadUpstream(t))
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeUpstreamDown)
			h.up.setBase(h.up.srv.URL) // the engine comes back for the next request
			return h
		}},
		{"503 upstream_down (key store)", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.store.mu.Lock()
			h.store.err = fmt.Errorf("disk on fire")
			h.store.mu.Unlock()
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeUpstreamDown)
			h.store.mu.Lock()
			h.store.err = nil
			h.store.mu.Unlock()
			return h
		}},
		{"502 upstream_error (engine 500)", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.up.set("500")
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeUpstreamError)
			h.up.set("json")
			return h
		}},
		{"502 upstream_error (redirect)", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.up.set("redirect")
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeUpstreamError)
			h.up.set("json")
			return h
		}},
		{"502 upstream_error (first byte)", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.up.firstByte(100 * time.Millisecond)
			h.up.set("hang")
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeUpstreamError)
			h.up.set("json")
			return h
		}},
		{"engine idle mid-stream", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.gw.idleTimeout = 100 * time.Millisecond
			h.up.set("sse", sseEvents(10, true)...)
			h.up.mu.Lock()
			h.up.stallAfter = 1
			h.up.mu.Unlock()
			if r := h.post("/v1/chat/completions", chatBody("m1", 1, `"stream":true`)); !strings.Contains(string(r.body), "upstream_error") {
				t.Fatalf("idle engine: %d %s", r.status, r.body)
			}
			h.up.mu.Lock()
			h.up.stallAfter = -1
			h.up.mu.Unlock()
			h.up.set("json")
			return h
		}},
		{"503 queue_timeout (timed out)", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.gw.queueTimeout = 100 * time.Millisecond
			long(h)
			cancel := h.hold(h.bob())
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeQueueTimeout)
			cancel()
			h.up.set("json")
			return h
		}},
		{"503 queue_timeout (waiting set full)", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.gw.queueTimeout = 5 * time.Second
			long(h)
			bob := h.bob()
			cancel := h.hold(bob)
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); h.do(http.MethodPost, "/v1/chat/completions", bob, chatBody("m1", 1, "")) }()
			}
			waitUntil(t, 3*time.Second, "two waiters", func() bool { _, w := h.gw.Queue(); return w == 2 })
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeQueueTimeout)
			h.shortStreams()
			cancel()
			wg.Wait()
			h.up.set("json")
			return h
		}},
		{"client gone while queued", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			long(h)
			cancel := h.hold(h.bob())
			ctx, cancelMe := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); _, _ = h.streamReq(ctx, chatBody("m1", 1, "")) }()
			waitUntil(t, 3*time.Second, "alice to be queued", func() bool { _, w := h.gw.Queue(); return w == 1 })
			cancelMe()
			<-done
			cancel()
			h.up.set("json")
			return h
		}},
		{"200 non-stream", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			if r := h.post("/v1/chat/completions", chatBody("m1", 1, "")); r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
			return h
		}},
		{"200 stream", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.up.set("sse", sseEvents(3, true)...)
			if r := h.post("/v1/chat/completions", chatBody("m1", 1, `"stream":true`)); r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
			h.up.set("json")
			return h
		}},
		{"200 embeddings", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.up.set("json", `{"object":"list","data":[],"usage":{"prompt_tokens":1}}`)
			if r := h.post("/v1/embeddings", `{"input":"x"}`); r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
			h.up.set("json", `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":4,"completion_tokens":4}}`)
			return h
		}},
		{"200 /v1/models", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			if r := h.get("/v1/models"); r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
			return h
		}},
		{"stop: client gone mid-stream", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			long(h)
			ctx, cancel := context.WithCancel(context.Background())
			res, err := h.streamReq(ctx, chatBody("m1", 1, `"stream":true`))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readEvent(bufio.NewReader(res.Body)); err != nil {
				t.Fatal(err)
			}
			cancel()
			res.Body.Close()
			h.up.set("json")
			return h
		}},
		{"stalled body", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.gw.readTimeout = 100 * time.Millisecond
			conn := rawConn(t, h.srv.URL)
			fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"model\":", testSecret)
			if res, err := http.ReadResponse(bufio.NewReader(conn), nil); err != nil || res.StatusCode != 400 {
				t.Fatalf("stalled body: %v %v", err, res)
			}
			h.gw.readTimeout = defaultReadTimeout
			return h
		}},
		{"stalled reader", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.gw.writeTimeout = 200 * time.Millisecond
			big := strings.Repeat("y", 256<<10)
			var events []string
			for i := 0; i < 64; i++ {
				events = append(events, `{"choices":[{"index":0,"delta":{"content":"`+big+`"}}]}`)
			}
			h.up.set("sse", events...)
			h.up.mu.Lock()
			h.up.gap = 0
			h.up.mu.Unlock()
			res, err := h.streamReq(context.Background(), chatBody("m1", 1, `"stream":true`))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { res.Body.Close() })
			if _, err := readEvent(bufio.NewReader(res.Body)); err != nil {
				t.Fatal(err)
			}
			h.up.set("json")
			return h
		}},
		{"panic in a stage", func(t *testing.T) *harness {
			h := newHarness(t, Config{}, nil)
			h.up.mu.Lock()
			h.up.countPanic = true
			h.up.mu.Unlock()
			req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader(chatBody("m1", 2, "")))
			req.Header.Set("Authorization", "Bearer "+testSecret)
			if res, err := h.srv.Client().Do(req); err == nil {
				res.Body.Close()
				t.Fatalf("a panicking handler must not produce a response: %d", res.StatusCode)
			}
			h.up.mu.Lock()
			h.up.countPanic = false
			h.up.mu.Unlock()
			return h
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			h := row.run(t)
			h.clean()
			if c := h.gw.Counters("k_alice1"); c.InFlight != 0 || c.TPMUsed != c.TodayTokens {
				t.Fatalf("counters after %s: %+v", row.name, c)
			}
			h.up.mu.Lock()
			h.up.gap = 50 * time.Millisecond
			h.up.mu.Unlock()
			if r := h.post("/v1/chat/completions", chatBody("m1", 1, "")); r.status != 200 {
				t.Fatalf("the next request after %s: %d %s", row.name, r.status, r.body)
			}
			h.clean()
		})
	}
}

// ---- I5: the ceiling ----

// Under random interleavings of admit, reserve (unbounded, none, or a cap), settle (counted or
// not, charged up to the reservation) and the clock, Σ live reservations + Σ charges in the window
// never exceeds TPM and today + reservations never exceeds the daily budget.
func TestI5CeilingRandomized(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	l := newLimiter()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	lim := keys.Limits{TPM: 200, DailyTokens: 500, MaxConcurrent: 4}
	type live struct {
		a        *admission
		prompt   int
		fitted   int
		reserved int
	}
	var inFlight []live
	check := func(step int) {
		t.Helper()
		st := l.state("k")
		st.mu.Lock()
		defer st.mu.Unlock()
		st.prune(now)
		_, charged := st.used()
		if charged+st.reserved > lim.TPM {
			t.Fatalf("step %d: window %d charged + %d reserved > TPM %d", step, charged, st.reserved, lim.TPM)
		}
		if st.today+st.reserved > lim.DailyTokens {
			t.Fatalf("step %d: today %d + %d reserved > daily %d", step, st.today, st.reserved, lim.DailyTokens)
		}
		if st.reserved < 0 || st.inFlight < 0 || st.inFlight != len(inFlight) {
			t.Fatalf("step %d: reserved %d, inFlight %d (live %d)", step, st.reserved, st.inFlight, len(inFlight))
		}
	}
	admitted, refused := 0, 0
	for step := 0; step < 5000; step++ {
		switch rng.Intn(3) {
		case 0:
			a, e := l.admit("k", lim)
			if e != nil {
				continue
			}
			prompt := rng.Intn(40)
			out := []int{-1, 0, rng.Intn(60) + 1}[rng.Intn(3)]
			fitted, e := l.reserve(a, lim, prompt, out)
			if e != nil {
				refused++
				l.settle(a, false, 0)
				continue
			}
			admitted++
			if fitted < 0 || (out > 0 && fitted > out) || (out == 0 && fitted != 0) || a.reserved != prompt+fitted {
				t.Fatalf("reserve(prompt %d, out %d) = %d, reserved %d", prompt, out, fitted, a.reserved)
			}
			inFlight = append(inFlight, live{a, prompt, fitted, a.reserved})
		case 1:
			if len(inFlight) == 0 {
				continue
			}
			i := rng.Intn(len(inFlight))
			x := inFlight[i]
			charged := []int{0, x.prompt + rng.Intn(x.fitted+1), x.reserved}[rng.Intn(3)]
			l.settle(x.a, rng.Intn(4) != 0, charged)
			inFlight = append(inFlight[:i], inFlight[i+1:]...)
		case 2:
			now = now.Add(time.Duration(rng.Intn(5)) * time.Second)
		}
		check(step)
	}
	if admitted == 0 || refused == 0 {
		t.Fatalf("the walk must both admit and refuse: %d / %d", admitted, refused)
	}
}

// The same ceiling through the pipeline: concurrent requests from one key against an engine that
// reports honest usage (prompt = the words sent, completion ≤ max_tokens), sampled continuously.
func TestI5CeilingThroughThePipeline(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.slots(4)
	h.up.set("usage")
	const tpm = 300
	h.setKey(func(k *keys.Key) { k.Limits.TPM = tpm; k.Limits.MaxConcurrent = 3; k.Limits.MaxOutputTokens = 60 })
	stop := make(chan struct{})
	var violation atomic.Value
	go func() {
		st := h.gw.lim.state("k_alice1")
		for {
			select {
			case <-stop:
				return
			default:
			}
			st.mu.Lock()
			st.prune(time.Now())
			_, charged := st.used()
			if charged+st.reserved > tpm {
				violation.Store(fmt.Sprintf("window %d charged + %d reserved > TPM %d", charged, st.reserved, tpm))
			}
			st.mu.Unlock()
			time.Sleep(200 * time.Microsecond)
		}
	}()
	var wg sync.WaitGroup
	var ok, limited atomic.Int32
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 20; i++ {
				r := h.post("/v1/chat/completions", chatBody("m1", 1+rng.Intn(30), ""))
				switch {
				case r.status == 200:
					ok.Add(1)
				case r.errCode == CodeRateLimited || r.errCode == CodeConcurrencyLimited:
					limited.Add(1)
				default:
					t.Errorf("unexpected: %d %s", r.status, r.body)
				}
			}
		}(g)
	}
	wg.Wait()
	close(stop)
	if v := violation.Load(); v != nil {
		t.Fatal(v)
	}
	if ok.Load() == 0 || limited.Load() == 0 {
		t.Fatalf("the run must both serve and limit: %d / %d", ok.Load(), limited.Load())
	}
	h.clean()
	if c := h.gw.Counters("k_alice1"); c.TPMUsed > tpm {
		t.Fatalf("after the run: %+v", c)
	}
}

// ---- I6: the settle table, one row per outcome ----

func TestI6SettleTable(t *testing.T) {
	type row struct {
		name   string
		run    func(t *testing.T, h *harness)
		count  int                            // RPM delta for the harness key
		charge func(usage.Event) (lo, hi int) // TodayTokens delta bounds, given the recorded event
	}
	exactly := func(n int) func(usage.Event) (int, int) { return func(usage.Event) (int, int) { return n, n } }
	rows := []row{
		{"Rejected: context_too_long", func(t *testing.T, h *harness) {
			h.up.setInfo(func(i *upstream.Info) { i.ModelContext = 10 })
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 50, "")), CodeContextTooLong)
		}, 0, exactly(0)},
		{"Rejected: waiting set full", func(t *testing.T, h *harness) {
			h.gw.queueTimeout = 5 * time.Second
			h.up.set("sse", sseEvents(200, true)...)
			bob := h.bob()
			cancel := h.hold(bob)
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); h.do(http.MethodPost, "/v1/chat/completions", bob, chatBody("m1", 1, "")) }()
			}
			waitUntil(t, 3*time.Second, "two waiters", func() bool { _, w := h.gw.Queue(); return w == 2 })
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeQueueTimeout)
			h.shortStreams()
			cancel()
			wg.Wait()
		}, 0, exactly(0)},
		{"QueueLost: timed out", func(t *testing.T, h *harness) {
			h.gw.queueTimeout = 100 * time.Millisecond
			h.up.set("sse", sseEvents(200, true)...)
			cancel := h.hold(h.bob())
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeQueueTimeout)
			cancel()
		}, 1, exactly(0)},
		{"QueueLost: client gone", func(t *testing.T, h *harness) {
			h.up.set("sse", sseEvents(200, true)...)
			cancel := h.hold(h.bob())
			ctx, cancelMe := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); _, _ = h.streamReq(ctx, chatBody("m1", 1, "")) }()
			waitUntil(t, 3*time.Second, "alice to be queued", func() bool { _, w := h.gw.Queue(); return w == 1 })
			cancelMe()
			<-done
			cancel()
		}, 0, exactly(0)},
		{"EngineErr: 500", func(t *testing.T, h *harness) {
			h.up.set("500")
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeUpstreamError)
		}, 1, exactly(0)},
		{"EngineErr: first byte", func(t *testing.T, h *harness) {
			h.up.firstByte(100 * time.Millisecond)
			h.up.set("hang")
			h.expectErr(h.post("/v1/chat/completions", chatBody("m1", 1, "")), CodeUpstreamError)
		}, 1, exactly(0)},
		{"Served: usage object seen", func(t *testing.T, h *harness) {
			if r := h.post("/v1/chat/completions", chatBody("m1", 2, "")); r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
		}, 1, exactly(8)},
		{"Served: no usage object (stream)", func(t *testing.T, h *harness) {
			h.up.set("sse", sseEvents(4, false)...)
			if r := h.post("/v1/chat/completions", chatBody("m1", 3, `"stream":true`)); r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
		}, 1, exactly(3 + 4)},
		{"Cut: stream, client gone", func(t *testing.T, h *harness) {
			h.up.set("sse", sseEvents(200, true)...)
			ctx, cancel := context.WithCancel(context.Background())
			res, err := h.streamReq(ctx, chatBody("m1", 1, `"stream":true`))
			if err != nil {
				t.Fatal(err)
			}
			br := bufio.NewReader(res.Body)
			for i := 0; i < 3; i++ {
				if _, err := readEvent(br); err != nil {
					t.Fatal(err)
				}
			}
			cancel()
			res.Body.Close()
		}, 1, func(ev usage.Event) (int, int) { return 1 + 3, 1 + 10 }},
		{"Cut: stream, engine idle", func(t *testing.T, h *harness) {
			h.gw.idleTimeout = 100 * time.Millisecond
			h.up.set("sse", sseEvents(10, true)...)
			h.up.mu.Lock()
			h.up.stallAfter = 2
			h.up.mu.Unlock()
			h.post("/v1/chat/completions", chatBody("m1", 1, `"stream":true`))
		}, 1, exactly(1 + 2)},
		{"Cut: non-stream, client gone before the body", func(t *testing.T, h *harness) {
			h.setKey(func(k *keys.Key) { k.Limits.MaxOutputTokens = 50 })
			h.up.mu.Lock()
			h.up.delay = 5 * time.Second
			h.up.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); _, _ = h.streamReq(ctx, chatBody("m1", 2, "")) }()
			<-h.up.started
			time.Sleep(50 * time.Millisecond) // the engine's headers are out; the body is not
			cancel()
			<-done
		}, 1, exactly(2 + 50)},
		{"Cut: non-stream, client gone before the headers", func(t *testing.T, h *harness) {
			h.setKey(func(k *keys.Key) { k.Limits.MaxOutputTokens = 50 })
			h.up.set("hang")
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); _, _ = h.streamReq(ctx, chatBody("m1", 2, "")) }()
			<-h.up.started
			cancel()
			<-done
		}, 1, exactly(2 + 50)},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			h := newHarness(t, Config{}, nil)
			before := h.gw.Counters("k_alice1")
			row.run(t, h)
			h.clean()
			after := h.gw.Counters("k_alice1")
			ev := h.eventFor("k_alice1")
			lo, hi := row.charge(ev)
			if count := after.RPMUsed - before.RPMUsed; count != row.count {
				t.Fatalf("counted %d, want %d (event %+v)", count, row.count, ev)
			}
			if charged := after.TodayTokens - before.TodayTokens; charged < lo || charged > hi {
				t.Fatalf("charged %d, want %d..%d (event %+v)", charged, lo, hi, ev)
			}
			if after.TPMUsed != after.TodayTokens {
				t.Fatalf("window and day disagree: %+v", after)
			}
		})
	}
}

// ---- I7: FIFO and exact resize ----

// With cap 2 and four waiters, the engine sees them in arrival order as slots free up; a cap
// decrease to 1 hands nothing over until in-flight is under it; an increase back to 2 lets the
// oldest waiter in before a newcomer; Queue() equals the queue at every step; the engine never
// sees more than the cap at once.
func TestI7FIFOAndResize(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.slots(2)
	h.gw.queueTimeout = 10 * time.Second
	h.up.set("sse", sseEvents(400, true)...)
	var peak atomic.Int32 // the most slots Queue() ever reported held
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if in, _ := h.gw.Queue(); int32(in) > peak.Load() {
				peak.Store(int32(in))
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()
	cancels := map[string]func(){}
	start := func(tag string) {
		ctx, cancel := context.WithCancel(context.Background())
		cancels[tag] = cancel
		body := chatBody("m1", 1, `"stream":true,"user":"`+tag+`"`)
		go func() {
			res, err := h.streamReq(ctx, body)
			if err == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				res.Body.Close()
			}
		}()
	}
	t.Cleanup(func() {
		for _, c := range cancels {
			c()
		}
	})
	queue := func(wantIn, wantWait int) {
		t.Helper()
		waitUntil(t, 3*time.Second, fmt.Sprintf("Queue() = %d, %d", wantIn, wantWait), func() bool {
			in, w := h.gw.Queue()
			return in == wantIn && w == wantWait
		})
	}
	engineSaw := func(n int, want ...string) {
		t.Helper()
		waitUntil(t, 3*time.Second, fmt.Sprintf("the engine to see %d requests", n), func() bool { return h.up.requests.Load() == int32(n) })
		if got := h.up.order(); !reflect.DeepEqual(got, want) {
			t.Fatalf("engine saw %v, want %v", got, want)
		}
	}
	start("A1")
	engineSaw(1, "A1")
	start("A2")
	engineSaw(2, "A1", "A2")
	for i, tag := range []string{"B", "C", "D", "E"} {
		start(tag)
		queue(2, i+1)
	}
	if r := h.post("/v1/chat/completions", chatBody("m1", 1, "")); r.errCode != CodeQueueTimeout || !strings.Contains(r.message, "4 requests already waiting") {
		t.Fatalf("a fifth waiter must be refused at once: %d %s", r.status, r.body)
	}
	cancels["A1"]()
	engineSaw(3, "A1", "A2", "B")
	queue(2, 3)
	cancels["A2"]()
	engineSaw(4, "A1", "A2", "B", "C")
	queue(2, 2)

	// Cap 2 → 1 while B and C run: B's release hands nothing over; C's hands D one slot.
	h.slots(1)
	cancels["B"]()
	queue(1, 2)
	time.Sleep(100 * time.Millisecond)
	if n := h.up.requests.Load(); n != 4 {
		t.Fatalf("a release above the new cap must not admit: engine saw %d", n)
	}
	cancels["C"]()
	engineSaw(5, "A1", "A2", "B", "C", "D")
	queue(1, 1)

	// Cap 1 → 2: the next arrival lets the oldest waiter (E) in first and queues behind it.
	h.slots(2)
	start("F")
	engineSaw(6, "A1", "A2", "B", "C", "D", "E")
	queue(2, 1)
	cancels["D"]()
	engineSaw(7, "A1", "A2", "B", "C", "D", "E", "F")
	queue(2, 0)
	cancels["E"]()
	cancels["F"]()
	queue(0, 0)
	close(stop)
	if m := peak.Load(); m != 2 {
		t.Fatalf("%d slots were held at once; the cap was never above 2", m)
	}
	h.clean()
}

// ---- I8: deadlines ----

// The three fixtures §1.6 adds (the stalled reader and the stalled body are TestStalledReaderFreesSlots
// and TestBodyReadDeadline): an engine that sends headers but no bytes is cut by the idle deadline,
// one that never sends headers by the first-byte deadline, and a live stream slower than every
// deadline is never cut.
func TestI8Deadlines(t *testing.T) {
	t.Run("headers but no bytes: idle deadline (stream)", func(t *testing.T) {
		h := newHarness(t, Config{}, nil)
		h.gw.idleTimeout = 200 * time.Millisecond
		h.up.set("sse", sseEvents(5, true)...)
		h.up.mu.Lock()
		h.up.stallAfter = 0
		h.up.mu.Unlock()
		start := time.Now()
		r := h.post("/v1/chat/completions", chatBody("m1", 1, `"stream":true`))
		if d := time.Since(start); r.status != 200 || !strings.Contains(string(r.body), `"code":"upstream_error"`) || d > 2*time.Second {
			t.Fatalf("idle stream: %d %s after %s", r.status, r.body, d)
		}
		if ev := h.rec.last(t); ev.Code != "upstream_error" || ev.CompletionTokens != 0 {
			t.Fatalf("event: %+v", ev)
		}
		h.clean()
	})
	t.Run("headers but no bytes: idle deadline (non-stream)", func(t *testing.T) {
		h := newHarness(t, Config{}, nil)
		h.gw.idleTimeout = 200 * time.Millisecond
		h.up.mu.Lock()
		h.up.delay = 5 * time.Second
		h.up.mu.Unlock()
		start := time.Now()
		r := h.post("/v1/chat/completions", chatBody("m1", 1, ""))
		h.expectErr(r, CodeUpstreamError)
		if d := time.Since(start); d > 2*time.Second || !strings.Contains(r.message, "stopped answering") {
			t.Fatalf("idle body: %s after %s", r.message, d)
		}
		select {
		case <-h.up.cancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("the idle engine was not cancelled")
		}
		h.clean()
	})
	t.Run("no headers: first-byte deadline", func(t *testing.T) {
		h := newHarness(t, Config{}, nil)
		h.up.firstByte(200 * time.Millisecond)
		h.up.set("hang")
		start := time.Now()
		r := h.post("/v1/chat/completions", chatBody("m1", 1, `"stream":true`))
		h.expectErr(r, CodeUpstreamError)
		if d := time.Since(start); d < 200*time.Millisecond || d > 2*time.Second || !strings.Contains(r.message, "did not answer in time") {
			t.Fatalf("first byte: %s after %s", r.message, d)
		}
		select {
		case <-h.up.cancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("the silent engine was not cancelled")
		}
		h.clean()
	})
	t.Run("slow but live stream is never cut", func(t *testing.T) {
		// Every engine- and client-side deadline is 250 ms; the engine sends one event every 120 ms
		// for 3 s (the §1.8 fixture "one byte every 10 s for 10 minutes", scaled).
		h := newHarness(t, Config{}, nil)
		h.up.firstByte(250 * time.Millisecond)
		h.gw.idleTimeout, h.gw.writeTimeout, h.gw.readTimeout = 250*time.Millisecond, 250*time.Millisecond, 250*time.Millisecond
		h.up.set("sse", sseEvents(25, true)...)
		h.up.mu.Lock()
		h.up.gap = 120 * time.Millisecond
		h.up.mu.Unlock()
		start := time.Now()
		r := h.post("/v1/chat/completions", chatBody("m1", 1, `"stream":true`))
		if d := time.Since(start); r.status != 200 || !strings.HasSuffix(strings.TrimSpace(string(r.body)), "data: [DONE]") || d < 2500*time.Millisecond {
			t.Fatalf("slow live stream was cut: %d after %s: %.200q", r.status, d, r.body)
		}
		if ev := h.rec.last(t); ev.Code != "" || ev.CompletionTokens != 25 {
			t.Fatalf("event: %+v", ev)
		}
		h.clean()
	})
}

// ---- I9: normalization post-conditions ----

// After normalize + checkBudgets, for every row of the fixture table: no denylisted key reaches the
// engine; max_tokens is present, numeric, ≤ the key's cap, ≤ what the context leaves (floor 16),
// ≤ what TPM leaves; model is present and allowed; include_usage iff stream; every other field is
// byte-identical.
func TestI9NormalizationPostConditions(t *testing.T) {
	const ctxLen, keyCap, tpm = 100, 40, 130
	overrides := `"n_predict":100000,"max_new_tokens":100000,"min_tokens":90000,"ignore_eos":true,"n":4,"n_cmpl":4,"best_of":8,` +
		`"n_probs":50,"id_slot":0,"slot_id":0,"n_keep":-1,"n_discard":0,"n_ctx":1000000,"priority":100,"lora":[{"id":0,"scale":1}]`
	passthrough := `"foo":{"bar":[1,2.50,"x",null]},"seed":12345678901234567890,"temperature":0.7,"stop":["\n"]`
	rows := []struct {
		name   string
		warm   int // requests charged 8 tokens each before the row's own
		model  string
		words  int
		extra  string
		allow  []string
		want   Code // "" = 200
		maxTok string
	}{
		{"every denylisted key", 0, "m1", 2, overrides, nil, "", "40"},
		{"max_tokens over the cap", 0, "m1", 2, `"max_tokens":500`, nil, "", "40"},
		{"max_completion_tokens over the cap", 0, "m1", 2, `"max_completion_tokens":500`, nil, "", "40"},
		{"max_tokens as a string", 0, "m1", 2, `"max_tokens":"9999"`, nil, "", "40"},
		{"max_tokens under the cap", 0, "m1", 2, `"max_tokens":7`, nil, "", "7"},
		{"absent model", 0, "", 2, "", nil, "", "40"},
		{"disallowed model", 0, "m1", 2, "", []string{"m2"}, CodeModelNotAllowed, ""},
		{"prompt under the context", 0, "m1", 50, "", nil, "", "40"},
		{"prompt at the context", 0, "m1", 100, "", nil, "", "16"},
		{"prompt over the context", 0, "m1", 101, "", nil, CodeContextTooLong, ""},
		{"stream", 0, "m1", 2, `"stream":true`, nil, "", "40"},
		{"prompt + cap over TPM", 5, "m1", 60, "", nil, "", "30"},
		{"TPM cannot hold prompt + 16", 5, "m1", 80, "", nil, CodeRateLimited, ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			h2 := newHarness(t, Config{}, nil)
			h2.up.setInfo(func(i *upstream.Info) { i.ModelContext = ctxLen })
			h2.setKey(func(k *keys.Key) { k.Limits.MaxOutputTokens = keyCap; k.Limits.TPM = tpm; k.Limits.Models = row.allow })
			for i := 0; i < row.warm; i++ {
				if r := h2.post("/v1/chat/completions", chatBody("m1", 1, "")); r.status != 200 {
					t.Fatalf("warm-up %d: %d %s", i, r.status, r.body)
				}
				h2.rec.waitFor(t, i+1)
			}
			h2.clean()
			used := h2.gw.Counters("k_alice1").TPMUsed
			engineBefore := h2.up.requests.Load()
			extra := passthrough
			if row.extra != "" {
				extra += "," + row.extra
			}
			sentRaw := chatBody(row.model, row.words, extra)
			r := h2.post("/v1/chat/completions", sentRaw)
			if row.want != "" {
				h2.expectErr(r, row.want)
				if h2.up.requests.Load() != engineBefore {
					t.Fatal("a rejected request reached the engine")
				}
				return
			}
			if r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
			got := h2.up.body(t)
			sent, _ := decodeObject([]byte(sentRaw))
			for _, k := range overrideKeys {
				if _, ok := got[k]; ok {
					t.Fatalf("%q reached the engine", k)
				}
			}
			capField := "max_tokens"
			if _, ok := sent["max_completion_tokens"]; ok {
				capField = "max_completion_tokens"
			}
			n, ok := got[capField].(json.Number)
			if !ok {
				t.Fatalf("%s absent or not numeric: %v", capField, got[capField])
			}
			v, _ := n.Int64()
			prompt := row.words
			if fmt.Sprint(v) != row.maxTok || v > keyCap || int(v) > max(ctxLen-prompt, minOutputTokens) || int(v) > tpm-used-prompt {
				t.Fatalf("%s = %d (want %s; cap %d, context leaves %d, TPM leaves %d)", capField, v, row.maxTok, keyCap, ctxLen-prompt, tpm-used-prompt)
			}
			if m, _ := got["model"].(string); m == "" || !h2.key.AllowsModel(m) {
				t.Fatalf("model %q", m)
			}
			stream, _ := sent["stream"].(bool)
			so, _ := got["stream_options"].(map[string]any)
			if (so["include_usage"] == true) != stream {
				t.Fatalf("include_usage %v for stream=%v", so, stream)
			}
			shaped := map[string]bool{"max_tokens": true, "max_completion_tokens": true, "model": true, "stream_options": true}
			for _, k := range overrideKeys {
				shaped[k] = true
			}
			for k, want := range sent {
				if shaped[k] {
					continue
				}
				if !reflect.DeepEqual(got[k], want) {
					t.Fatalf("%q changed: sent %v, engine saw %v", k, want, got[k])
				}
			}
			for k := range got {
				if _, ok := sent[k]; !ok && !shaped[k] {
					t.Fatalf("%q was invented", k)
				}
			}
		})
	}
}
