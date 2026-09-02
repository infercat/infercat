package gateway

// Ticket 021: a friend who leaves while waiting must never be handed the slot. The hand-over and
// the departure can become ready in the same instant and Go's select picks between ready cases at
// random, so the rule has to live in the code rather than in the scheduler — and the slot has to
// reach the next waiter instead of dying with the request that left.

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/2185Lab/bunny-network/internal/usage"
)

// The race, forced. B joins the queue with a context that is already cancelled and, from its own
// `queued` callback — which acquire runs after B is on the list and before wait selects — releases
// the slot A holds, so fill hands it straight to B. By the time B selects, the hand-over and the
// departure are both ready, every round. C is on the list behind B. Every round must end the same
// way: B loses its place as QueueLost/client gone, and the slot lands on C.
func TestQueueDepartureWinsTheHandover(t *testing.T) {
	for round := 0; round < 50; round++ {
		q := &slotQueue{cap: func() int { return 1 }}
		if o, err := q.acquire(context.Background(), time.Minute, time.Minute, nil); o != outcomeNone || err != nil {
			t.Fatalf("round %d: A must take the free slot: %v %v", round, o, err)
		}
		gone, leave := context.WithCancel(context.Background())
		leave() // B's friend is already away when the slot arrives

		handOver := make(chan struct{}) // closed once C is behind B: B's callback then frees A's slot
		var bOut outcome
		var bErr *gwError
		bDone := make(chan struct{})
		go func() {
			defer close(bDone)
			bOut, bErr = q.acquire(gone, time.Minute, time.Minute, func() error {
				<-handOver
				q.release()
				return nil
			})
		}()
		waitUntil(t, 3*time.Second, "B on the list", func() bool { _, w := q.counts(); return w == 1 })

		var cOut outcome
		var cErr *gwError
		cDone := make(chan struct{})
		go func() {
			defer close(cDone)
			// C's wait is bounded so a regression fails in seconds rather than parking here: when
			// the rule holds, B's acquire hands C the slot long before C can reach the timer.
			cOut, cErr = q.acquire(context.Background(), 10*time.Second, time.Minute, nil)
		}()
		waitUntil(t, 3*time.Second, "C behind B", func() bool { _, w := q.counts(); return w == 2 })

		close(handOver)
		<-bDone
		<-cDone
		if bOut != outcomeQueueLost || bErr == nil || bErr.Code != CodeClientClosed {
			t.Fatalf("round %d: a friend who left took the slot: %v %v", round, bOut, bErr)
		}
		if cOut != outcomeNone || cErr != nil {
			t.Fatalf("round %d: the slot did not reach the next waiter: %v %v", round, cOut, cErr)
		}
		if in, w := q.counts(); in != 1 || w != 0 {
			t.Fatalf("round %d: queue is %d/%d, want 1/0", round, in, w)
		}
		q.release()
		if in, w := q.counts(); in != 0 || w != 0 {
			t.Fatalf("round %d: queue is %d/%d after C, want 0/0", round, in, w)
		}
	}
}

// The same rule through the pipeline, where what is at stake is the settle row: A holds the only
// slot, B queues and leaves, C queues behind B, and B's departure and A's release are fired
// together. However that falls, B is QueueLost/client gone — nothing counted, nothing charged, no
// reservation left standing — and C gets the slot. (The interleaving here is the machine's; the
// round-by-round proof is the fixture above.)
func TestQueueDepartureIsNeverCharged(t *testing.T) {
	h := newHarness(t, Config{}, nil)
	h.gw.queueTimeout = time.Minute
	h.up.set("sse", sseEvents(200, true)...)
	bob := h.bob()
	releaseA := h.hold(bob)

	ctxB, leaveB := context.WithCancel(context.Background())
	bDone := make(chan struct{})
	go func() { defer close(bDone); _, _ = h.streamReq(ctxB, chatBody("m1", 1, `"user":"B"`)) }()
	waitUntil(t, 3*time.Second, "alice queued", func() bool { _, w := h.gw.Queue(); return w == 1 })

	cDone := make(chan struct{})
	go func() {
		defer close(cDone)
		h.do(http.MethodPost, "/v1/chat/completions", bob, chatBody("m1", 1, `"stream":true,"user":"C"`))
	}()
	waitUntil(t, 3*time.Second, "C behind alice", func() bool { _, w := h.gw.Queue(); return w == 2 })

	h.shortStreams() // C's stream, once it gets the slot; A keeps the events it started with
	var wg sync.WaitGroup
	fire := make(chan struct{})
	for _, act := range []func(){leaveB, releaseA} {
		wg.Add(1)
		go func(act func()) { defer wg.Done(); <-fire; act() }(act)
	}
	close(fire)
	wg.Wait()
	<-bDone
	<-cDone

	waitUntil(t, 3*time.Second, "the slot to reach C", func() bool {
		for _, tag := range h.up.order() {
			if tag == "C" {
				return true
			}
		}
		return false
	})
	h.clean()
	// The premise, asserted rather than assumed: B never reached the engine, so there is no work
	// its key could honestly be charged for.
	for _, tag := range h.up.order() {
		if tag == "B" {
			t.Fatalf("B reached the engine; this fixture is about a request that never did: %v", h.up.order())
		}
	}
	if c := h.gw.Counters("k_alice1"); c.RPMUsed != 0 || c.TPMUsed != 0 || c.TodayTokens != 0 {
		t.Fatalf("a friend who left while queued was billed: %+v (engine saw %v)", c, h.up.order())
	}
	var ev usage.Event
	for _, e := range h.rec.waitFor(t, 1) {
		if e.KeyID == "k_alice1" {
			ev = e
		}
	}
	if ev.Status != 499 || ev.Code != string(CodeClientClosed) {
		t.Fatalf("alice's event: %+v", ev)
	}
}
