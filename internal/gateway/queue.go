package gateway

import (
	"context"
	"sync"
	"time"
)

// slotQueue is the global queue in front of the engine (DESIGN §1.5): a FIFO whose capacity is the
// engine's slot count read at decision time, never pushed. A released slot goes to the oldest
// waiter; at most max(2, 2×cap) requests wait at once (006 promise 10) and one more is refused on
// the spot, so a burst degrades to fast 503s with Retry-After rather than a growing set of parked
// bodies (Protection 4). A cap decrease drains naturally (no hand-over while inFlight > cap); an
// increase takes effect on the next arrival or release — under load, milliseconds away.
type slotQueue struct {
	mu       sync.Mutex
	cap      func() int // engine.Info().Slots (override applied), min 1 — read live
	inFlight int
	waiters  []*waiter // FIFO; a released slot is handed to waiters[0]
}

type waiter struct{ ch chan struct{} } // closed when a slot is handed over

// acquire takes a slot at once when one is free, else waits for one in arrival order for at most
// wait. The outcome says how it ended: outcomeNone with a slot held; outcomeRejected when the
// waiting set was full (refused on the spot, no place was held); outcomeQueueLost when a place was
// held and then lost to the timeout (queue_timeout) or to the friend leaving (client_closed). On
// timeout or ctx the waiter removes itself; if a slot was handed over in that race it hands it on.
//
// queued, when non-nil, is the request's word to the friend that it is in line (018): called once
// on joining and again every `every` while waiting. An error from it is a friend who has gone —
// the write is what detects one who left while queued — and ends the wait as client_closed, so the
// slot never goes to a dead request. A request that gets a slot at once never calls it.
func (s *slotQueue) acquire(ctx context.Context, wait, every time.Duration, queued func() error) (outcome, *gwError) {
	s.mu.Lock()
	limit := s.limit()
	s.fill(limit)
	if s.inFlight < limit {
		s.inFlight++
		s.mu.Unlock()
		return outcomeNone, nil
	}
	if n := len(s.waiters); n >= max(2, 2*limit) {
		s.mu.Unlock()
		return outcomeRejected, errf(CodeQueueTimeout, retryAfterQueueTimeout, "the host's engine is busy; %d requests already waiting", n)
	}
	w := &waiter{ch: make(chan struct{})}
	s.waiters = append(s.waiters, w)
	s.mu.Unlock()

	err := s.wait(ctx, w, wait, every, queued)
	if err == nil {
		return outcomeNone, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.waiters {
		if x == w {
			s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
			return outcomeQueueLost, err
		}
	}
	// No longer queued: a slot was handed over as we gave up. Hand it on.
	s.inFlight--
	s.fill(s.limit())
	return outcomeQueueLost, err
}

// wait is the waiter's side of acquire: nil when the slot was handed over; otherwise why the wait
// ended — the timeout, the friend's context, or a keepalive write that failed.
func (s *slotQueue) wait(ctx context.Context, w *waiter, wait, every time.Duration, queued func() error) *gwError {
	var tick <-chan time.Time
	if queued != nil {
		if err := queued(); err != nil {
			return errf(CodeClientClosed, 0, "client went away while queued: %v", err)
		}
		t := time.NewTicker(every)
		defer t.Stop()
		tick = t.C
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	for {
		select {
		case <-w.ch:
			return nil
		case <-t.C:
			return errf(CodeQueueTimeout, retryAfterQueueTimeout, "the host's engine is busy; waited %s for a free slot", wait)
		case <-ctx.Done():
			return errf(CodeClientClosed, 0, "client went away while queued")
		case <-tick:
			if err := queued(); err != nil {
				return errf(CodeClientClosed, 0, "client went away while queued: %v", err)
			}
		}
	}
}

// release gives a slot back: to the oldest waiter when the live cap allows, else to the pool.
func (s *slotQueue) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight--
	s.fill(s.limit())
}

// fill hands slots to waiters, oldest first, while there is room under the cap. Caller holds mu.
// This is where a cap increase takes effect, and why a decrease never over-admits: a release with
// inFlight still above the new cap hands nothing over.
func (s *slotQueue) fill(limit int) {
	for len(s.waiters) > 0 && s.inFlight < limit {
		w := s.waiters[0]
		s.waiters[0] = nil
		s.waiters = s.waiters[1:]
		s.inFlight++
		close(w.ch)
	}
}

// limit is the cap read now, never below one. Caller holds mu.
func (s *slotQueue) limit() int { return max(1, s.cap()) }

// counts is what Queue() reports: holders and waiters, exact, under the one mutex.
func (s *slotQueue) counts() (inFlight, waiting int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight, len(s.waiters)
}
