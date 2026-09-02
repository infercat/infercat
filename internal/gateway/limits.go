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
//   - TPM = prompt+completion tokens charged in the last 60 s, from real usage reported by the upstream,
//     plus the worst case (prompt + max_tokens) reserved by requests still in flight, settled to the
//     charge when they end (DESIGN §1.4). So Σ reservations + Σ charges never exceeds TPM: the limit
//     is a ceiling for tokens, not for prompts.
// What a request counts and what it is charged is decided in one place, the settle table
// (request.settleRow): an admission that did not count is removed from the log by its own seq, so a
// friend retrying against a 4xx does not dig the hole deeper. The log is pruned on every touch,
// bounding memory to one minute of traffic per key.
//
// Daily budget = prompt+completion tokens charged since UTC midnight, plus live reservations.
// Retry-After is seconds to the next UTC midnight.
//
// Per-key concurrency is an immediate 429 (never queued): one person's burst must not hold a global
// slot (or a queue position) for the others; the friend's own request finishing is what frees it.

const window = 60 * time.Second

type logEntry struct {
	t      time.Time
	req    int    // 1 for an admission, 0 for a token charge
	seq    uint64 // admissions: which admission wrote it, so settle removes exactly its own
	tokens int
}

type keyState struct {
	mu       sync.Mutex
	log      []logEntry
	seq      uint64 // the last admission seq handed out
	inFlight int
	reserved int       // prompt + max_tokens of requests in flight, counted by TPM and daily until settled
	day      time.Time // UTC midnight of the day `today` counts
	today    int
	lastSeen time.Time
}

// admission is what admit hands out and settle takes back: the key, the RPM entry this admission
// wrote (by seq), and the tokens reserve later put on it. The request record holds it; nothing
// else ends it.
type admission struct {
	key      string
	seq      uint64
	reserved int
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
// bodies and in engine tokenize calls alike. The caller settles the admission exactly once.
func (l *limiter) admit(id string, lim keys.Limits) (*admission, *gwError) {
	st := l.state(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := l.now()
	st.prune(now)
	st.lastSeen = now

	if lim.MaxConcurrent > 0 && st.inFlight >= lim.MaxConcurrent {
		return nil, errf(CodeConcurrencyLimited, 1, "this key allows %d request(s) at a time; %d in flight", lim.MaxConcurrent, st.inFlight)
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
		return nil, errf(CodeRateLimited, secondsUntil(oldest.Add(window), now), "rate limit: %d requests per minute; %d used", lim.RPM, reqs)
	}
	st.inFlight++
	st.seq++
	st.log = append(st.log, logEntry{t: now, req: 1, seq: st.seq})
	return &admission{key: id, seq: st.seq}, nil
}

// minOutputTokens is the floor when max_tokens is shrunk to fit the context or a token budget.
const minOutputTokens = 16

// fitBudget is the shrink-to-fit rule (DESIGN §1.4) against one ceiling: the request's worst case,
// prompt + out, must fit in what the ceiling has left; when it does not, out shrinks to the room
// left when that is at least minOutputTokens — the rule context already applies — else the request
// does not fit. out < 0 is an unbounded output (a chat with no cap anywhere): the ceiling becomes
// the cap. out == 0 is no output (embeddings): the prompt alone must fit. A zero ceiling is none.
func fitBudget(limit, used, prompt, out int) (int, bool) {
	if limit <= 0 {
		return out, true
	}
	room := limit - used - prompt
	switch {
	case out == 0:
		return 0, room >= 0
	case out > 0 && out <= room:
		return out, true
	case room >= minOutputTokens:
		return room, true
	}
	return 0, false
}

// floorFor is the least output fitBudget will grant for out: nothing for none, the floor for an
// unbounded or shrinkable cap, the cap itself when it is already under the floor.
func floorFor(out int) int {
	switch {
	case out == 0:
		return 0
	case out < 0:
		return minOutputTokens
	}
	return min(out, minOutputTokens)
}

// reserve is the second half of admission, once the prompt is tokenized: the request's worst case,
// prompt + out tokens, must fit TPM over the sliding window and the daily budget, both counting the
// charges and the live reservations of the key's other requests. When it does not, out shrinks to
// what fits (fitBudget), else the request is refused with the numbers and an exact Retry-After.
// On success exactly prompt + out is reserved on the admission until settle, and the cap in force
// is returned so the caller can write it into the body (out < 0 comes back unchanged only when no
// ceiling is set: nothing bounded it).
func (l *limiter) reserve(a *admission, lim keys.Limits, prompt, out int) (int, *gwError) {
	st := l.state(a.key)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := l.now()
	st.prune(now)
	_, charged := st.used()
	live := charged + st.reserved
	need := prompt + floorFor(out)
	fitted, ok := fitBudget(lim.TPM, live, prompt, out)
	if !ok {
		// Walk the log oldest-first until enough tokens have expired for the least this request
		// can take to fit; a reservation never expires by time, so it may not fit before the window.
		retry := 1
		remaining := live
		for _, e := range st.log {
			remaining -= e.tokens
			if remaining+need <= lim.TPM {
				retry = secondsUntil(e.t.Add(window), now)
				break
			}
		}
		if remaining+need > lim.TPM {
			retry = secondsUntil(now.Add(window), now)
		}
		return 0, errf(CodeRateLimited, retry, "token limit: %d tokens per minute; %d used, this request needs at least %d", lim.TPM, live, need)
	}
	out = fitted
	if fitted, ok = fitBudget(lim.DailyTokens, st.today+st.reserved, prompt, out); !ok {
		midnight := st.day.Add(24 * time.Hour)
		return 0, errf(CodeBudgetExhausted, secondsUntil(midnight, now), "daily budget: %d tokens per day (UTC); %d used, this request needs at least %d", lim.DailyTokens, st.today+st.reserved, need)
	}
	out = fitted
	a.reserved = prompt + max(out, 0)
	st.reserved += a.reserved
	return out, nil
}

// settle ends an admission exactly once, as the settle table decided (DESIGN §1.4): frees the
// per-key slot, drops the reservation, removes the admission's own RPM entry when the request did
// not count, and charges what the request cost against the window and the day. Nothing is
// clamped at zero on purpose: a second settle would show up as a negative counter (I1).
func (l *limiter) settle(a *admission, counted bool, charged int) {
	st := l.state(a.key)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := l.now()
	st.prune(now)
	st.inFlight--
	st.reserved -= a.reserved
	if !counted {
		for i, e := range st.log {
			if e.req == 1 && e.seq == a.seq {
				st.log = append(st.log[:i], st.log[i+1:]...)
				break
			}
		}
	}
	if charged > 0 {
		st.log = append(st.log, logEntry{t: now, tokens: charged})
		st.today += charged
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
	return usage.KeyCounters{InFlight: st.inFlight, RPMUsed: reqs, TPMUsed: tokens + st.reserved, TodayTokens: st.today + st.reserved, LastSeen: st.lastSeen}
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
