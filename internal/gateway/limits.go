package gateway

import (
	"math"
	"sync"
	"time"

	"github.com/2185Lab/bunny-network/internal/keys"
	"github.com/2185Lab/bunny-network/internal/usage"
)

// Per-key limits live entirely in memory and reset on restart (known v1 limitation: the daily budget
// therefore also resets on restart; docs/ARCHITECTURE.md gives counters no persistence).
//
// RPM and TPM share one mechanism: a sliding 60 s log of admissions and token charges per key.
//   - RPM = number of admissions in the last 60 s. Chosen over a token bucket or fixed window because
//     it is exact ("at most N requests in any 60 s"), needs no background ticker, gives an exact
//     Retry-After (when the oldest admission leaves the window), and is the same number /me reports
//     as rpm_used — the friend's usage bar and the limiter never disagree.
//   - TPM = prompt+completion tokens charged in the last 60 s, from real usage reported by the upstream.
// Rejected requests are not logged (an admission that is rejected before it reaches the queue is
// aborted, which un-counts it), so a friend retrying against a 4xx does not dig the hole deeper.
// The log is pruned on every touch, bounding memory to one minute of traffic per key.
//
// Daily budget = prompt+completion tokens charged since UTC midnight. Retry-After is seconds to the
// next UTC midnight.
//
// Per-key concurrency is an immediate 429 (never queued): one person's burst must not hold a global
// slot (or a queue position) for the others; the friend's own request finishing is what frees it.

const window = 60 * time.Second

type logEntry struct {
	t      time.Time
	req    int // 1 for an admission, 0 for a token charge
	tokens int
}

type keyState struct {
	mu       sync.Mutex
	log      []logEntry
	inFlight int
	day      time.Time // UTC midnight of the day `today` counts
	today    int
	lastSeen time.Time
}

// limiter owns all keyState. now is injectable for tests.
type limiter struct {
	mu   sync.Mutex
	keys map[string]*keyState
	now  func() time.Time
}

func newLimiter() *limiter {
	return &limiter{keys: map[string]*keyState{}, now: time.Now}
}

func (l *limiter) state(id string) *keyState {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.keys[id]
	if !ok {
		st = &keyState{}
		l.keys[id] = st
	}
	return st
}

// prune drops entries older than the window and rolls the day. Caller holds st.mu.
func (st *keyState) prune(now time.Time) {
	cutoff := now.Add(-window)
	i := 0
	for i < len(st.log) && !st.log[i].t.After(cutoff) {
		i++
	}
	if i > 0 {
		st.log = append(st.log[:0], st.log[i:]...)
	}
	day := now.UTC().Truncate(24 * time.Hour)
	if !day.Equal(st.day) {
		st.day, st.today = day, 0
	}
}

// used returns (requests, tokens) in the window. Caller holds st.mu after prune.
func (st *keyState) used() (reqs, tokens int) {
	for _, e := range st.log {
		reqs += e.req
		tokens += e.tokens
	}
	return
}

func secondsUntil(t, now time.Time) int {
	s := int(math.Ceil(t.Sub(now).Seconds()))
	if s < 1 {
		s = 1
	}
	return s
}

// touch marks the key as seen (any authenticated request).
func (l *limiter) touch(id string) {
	st := l.state(id)
	st.mu.Lock()
	st.lastSeen = l.now()
	st.mu.Unlock()
}

// admit atomically checks per-key concurrency and RPM and, on success, counts the admission and
// takes one in-flight slot. It runs right after auth, before a byte of body is read (ticket 006,
// promise 2): cheap and in memory, so a burst from one key is bounded by max_concurrent in buffered
// bodies and in engine tokenize calls alike. The caller ends the admission exactly once: release once
// the request reached the queue or the engine, abort if it was rejected before that.
func (l *limiter) admit(id string, lim keys.Limits) *gwError {
	st := l.state(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := l.now()
	st.prune(now)
	st.lastSeen = now

	if lim.MaxConcurrent > 0 && st.inFlight >= lim.MaxConcurrent {
		return errf(CodeConcurrencyLimited, 1, "this key allows %d request(s) at a time; %d in flight", lim.MaxConcurrent, st.inFlight)
	}
	reqs, _ := st.used()
	if lim.RPM > 0 && reqs >= lim.RPM {
		// The oldest admission leaving the window admits one more.
		var oldest time.Time
		for _, e := range st.log {
			if e.req == 1 {
				oldest = e.t
				break
			}
		}
		return errf(CodeRateLimited, secondsUntil(oldest.Add(window), now), "rate limit: %d requests per minute; %d used", lim.RPM, reqs)
	}
	st.inFlight++
	st.log = append(st.log, logEntry{t: now, req: 1})
	return nil
}

// checkTokens is the second half of admission, once the prompt is tokenized: TPM over the sliding
// window and the daily budget, both including the pre-check prompt count so a request that alone
// would exceed the remaining minute or day is refused with the numbers in the message. Read-only;
// the caller aborts the admission on an error.
func (l *limiter) checkTokens(id string, lim keys.Limits, promptTokens int) *gwError {
	st := l.state(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := l.now()
	st.prune(now)
	_, tokens := st.used()
	if lim.TPM > 0 && tokens+promptTokens > lim.TPM {
		// Walk the log oldest-first until enough tokens have expired for this request to fit.
		retry := 1
		remaining := tokens
		for _, e := range st.log {
			remaining -= e.tokens
			if remaining+promptTokens <= lim.TPM {
				retry = secondsUntil(e.t.Add(window), now)
				break
			}
		}
		if remaining+promptTokens > lim.TPM {
			retry = secondsUntil(now.Add(window), now) // cannot fit even with an empty window
		}
		return errf(CodeRateLimited, retry, "token limit: %d tokens per minute; %d used, this request needs %d", lim.TPM, tokens, promptTokens)
	}
	if lim.DailyTokens > 0 && st.today+promptTokens > lim.DailyTokens {
		midnight := st.day.Add(24 * time.Hour)
		return errf(CodeBudgetExhausted, secondsUntil(midnight, now), "daily budget: %d tokens per day (UTC); %d used, this request needs %d", lim.DailyTokens, st.today, promptTokens)
	}
	return nil
}

// abort ends an admission that never reached the queue: frees the in-flight slot and drops the newest
// admission from the RPM log (with max_concurrent > 1 that may be a sibling's entry a few milliseconds
// apart; the count is exact either way).
func (l *limiter) abort(id string) {
	st := l.state(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.inFlight > 0 {
		st.inFlight--
	}
	for i := len(st.log) - 1; i >= 0; i-- {
		if st.log[i].req == 1 {
			st.log = append(st.log[:i], st.log[i+1:]...)
			break
		}
	}
}

// release frees the in-flight slot taken by admit and charges the real token usage.
func (l *limiter) release(id string, tokens int) {
	st := l.state(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := l.now()
	st.prune(now)
	if st.inFlight > 0 {
		st.inFlight--
	}
	if tokens > 0 {
		st.log = append(st.log, logEntry{t: now, tokens: tokens})
		st.today += tokens
	}
}

func (l *limiter) counters(id string) usage.KeyCounters {
	l.mu.Lock()
	st, ok := l.keys[id]
	l.mu.Unlock()
	if !ok {
		return usage.KeyCounters{}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.prune(l.now())
	reqs, tokens := st.used()
	return usage.KeyCounters{InFlight: st.inFlight, RPMUsed: reqs, TPMUsed: tokens, TodayTokens: st.today, LastSeen: st.lastSeen}
}

func (l *limiter) allCounters() map[string]usage.KeyCounters {
	l.mu.Lock()
	ids := make([]string, 0, len(l.keys))
	for id := range l.keys {
		ids = append(ids, id)
	}
	l.mu.Unlock()
	out := make(map[string]usage.KeyCounters, len(ids))
	for _, id := range ids {
		out[id] = l.counters(id)
	}
	return out
}
