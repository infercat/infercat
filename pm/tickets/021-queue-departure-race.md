---
id: 021
title: Queue departure race — a client that leaves while waiting must never take the slot (DESIGN §1.4 QueueLost row)
kind: sensitive
size: 2
status: landed
updated: 2026-09-02
release: demo-1
---

# 021 — Queue departure race

**Why.** Found by ticket 019 while de-flaking `TestI6SettleTable`: in `slotQueue.wait`
(`internal/gateway/queue.go:83-96`) the `select` can see both the hand-over channel and `ctx.Done()`
ready; Go picks at random, so a departed request can take the slot, fail at `callUpstream`, and settle as
`Cut` — counted against RPM and charged its whole reservation. DESIGN §1.4 says QueueLost, client gone:
not counted, 0 charged. Reproduced 5/30 under CPU load.

**Promises.**
1. `wait` checks `ctx.Err()` before returning a slot; a slot handed over in that instant is handed on
   (the release path already does this on timeout — reuse it). The settle row for this case is
   QueueLost/client gone.
2. A fixture that forces the race deterministically (hand over and cancel in the same instant, ×N)
   and asserts counted = 0, charged = 0, and the slot reaches the next waiter.
3. `go test -race -count=10 ./internal/gateway/` printed green, plus the 019 stress command.

**Size 1** (≤150 lines). Concept budget 0. **Sensitive** (Protection 4 accounting).
**Scope contract.** `internal/gateway/queue.go`, its tests. Ticket 020 is web-only; no conflict.

## Log

- **2026-09-02 17:20 EDT** — ACK. Base `c0e1733`, lane `t021-queue-race`. Scope read: `queue.go`,
  `request.go` settle path, DESIGN §1.4/§1.5.
- **2026-09-02 17:22 EDT** — Promise 1 written: `wait` checks `ctx.Err()` in the `case <-w.ch`
  branch and reports the departure; `acquire`'s existing "no longer queued" path hands the slot on.
- **2026-09-02 17:24 EDT** — Promise 2 fixture written and proved to bite: with the fix removed it
  fails at **round 0** (`a friend who left took the slot: 0 <nil>` — outcomeNone, no error).
- **2026-09-02 17:26 EDT** — Rate-limit kill; the PM WIP-committed the work. Resumed, rebased onto
  `002e67a`, re-ran `-race -count=10` on the new base (215 s, green).
- **2026-09-02 17:27 EDT** — **The integration half of promise 2 does not hold.** Under four `yes`
  hogs, `-count=25` of `TestQueueDepartureIsNeverCharged` failed 1/25 with the 019 signature
  (`RPMUsed:1 TPMUsed:2049 TodayTokens:2049`).
- **2026-09-02 17:28 EDT** — Tried the only in-scope tightening (a second `ctx.Err()` check in
  `acquire` after `wait` returns nil): **still 2/25**. That rules the queue out as the location.
- **2026-09-02 17:29 EDT** — Instrumented `callUpstream` (reverted). Cause pinned, evidence in
  `## Contest`. Backed the tightening out — it moved no assertion and is not worth the code — and
  parked the integration fixture behind `t.Skip` naming the contest.
- **2026-09-02 17:35 EDT** — Froze on `002e67a` with the contest.
- **2026-09-02 17:40 EDT** — **PM ruling: contest accepted**, scope extended to `request.go`'s
  `callUpstream`. Applied, un-skipped the fixture — and it still failed **2/25** under the hogs. The
  ruling's own words are why: "gone *before `callUpstream` sends anything*". A context check taken
  before `Do` does not answer that question, because the cancellation lands *inside* `Do`, after the
  check and before the bytes reach the engine (`engine saw: [C]` while B was billed).
- **2026-09-02 17:45 EDT** — Implemented the ruling as written, with the one thing it needed and did
  not name: a way to know whether anything was sent. `httptrace.WroteRequest` on the context the
  engine seam already hands its transport. **25/25 green under four CPU hogs.**
- **2026-09-02 17:55 EDT** — Settle table's other rows re-proved untouched under the same load;
  `-race -count=10` green; full checks green. Re-froze on `002e67a`.

## Report

**Delivered.** Promise 1 in full, promise 3 in full, and promise 2 at the queue level — which is
where the queue's own rule can be made deterministic.

`slotQueue.wait`'s `case <-w.ch` now checks `ctx.Err()` before accepting a slot, and reports the
departure instead. `acquire` needs no change: a hand-over that `wait` declines leaves the waiter off
the list, so control already falls to the existing "No longer queued: a slot was handed over as we
gave up. Hand it on." path, which does `inFlight--` and `fill` — the slot goes to the next waiter,
and the outcome is `outcomeQueueLost` with `CodeClientClosed`, which `settleRow` reads as
uncounted / charged 0, exactly the DESIGN §1.4 row. 11 lines added, 3 removed, all in `queue.go`;
6 of the 11 are the comment that records the rule.

**The fixture, and that it bites** (`queue_test.go`, `TestQueueDepartureWinsTheHandover`). The race
is *forced*, not waited for. B joins the queue with a context that is already cancelled and, from
its own `queued` callback — which `acquire` runs after B is on the list and before `wait` selects —
releases the slot A holds, so `fill` hands it straight to B. By the time B selects, the hand-over
and the departure are both ready, every round, on every machine. C sits behind B. 50 rounds; each
asserts B is `outcomeQueueLost`/`client_closed`, C acquires, and the queue reads 1/0 then 0/0.

```
# the fix removed, the fixture kept
$ go test -count=1 -run TestQueueDeparture ./internal/gateway/
--- FAIL: TestQueueDepartureWinsTheHandover
    queue_test.go:59: round 0: a friend who left took the slot: 0 <nil>
```

**Evidence printed.**

```
$ go test -race -count=10 ./internal/gateway/            # promise 3, on the rebased base
ok      github.com/2185Lab/bunny-network/internal/gateway        215.012s

$ for i in 1 2 3 4; do yes > /dev/null & done            # 019's stress command; hogs killed after
$ go test -count=25 -run TestI6SettleTable ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        36.918s
$ go test -count=25 -run TestQueueDeparture ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        15.344s
```

**Freeze check.** `go build ./... && go vet ./... && go test ./...` — every package ok:
**246 passed / 0 failed / 3 skipped**. The third skip is new and is the contest below
(`TestQueueDepartureIsNeverCharged`); the other two are the pre-existing opt-in live tests.

### The ruling, applied (second freeze)

The contest below was **accepted**: scope extended to `callUpstream`. Applying it as a context check
taken before `Do` was not enough — measured, not guessed: **2/25 under the hogs**, with
`engine saw: [C]` while B was billed 2049. The cancellation lands *inside* `Do`, after any check the
caller can take and before the bytes reach the engine.

So the ruling is implemented by the fact it names — whether anything was **sent** — read from
`httptrace.WroteRequest` on the context the engine seam already hands its transport. `Do` reports
the identical `context canceled` error whether or not the request left the building; the trace is
the only thing that separates the two. `upstream.Engine` does not move (DESIGN §3.4 intact): both
the real client (`upstream/client.go:108`) and the test fake build their request with
`http.NewRequestWithContext` from the context we pass, so the trace rides through unchanged.

```go
	var sent atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(i httptrace.WroteRequestInfo) { sent.Store(i.Err == nil) },
	})
	resp, derr := q.g.up.Do(ctx, http.MethodPost, string(q.kind), payload, q.n.stream)
	if derr != nil {
		if q.r.Context().Err() != nil {
			q.outcome = outcomeCut
			if !sent.Load() {
				q.outcome = outcomeQueueLost
			}
```

**The other settle rows are untouched, and that is the point of the split.** I6's
`Cut: non-stream, client gone before the headers` sends the engine a complete request and then the
friend leaves while it is thinking: `WroteRequest` fired clean, so it stays Cut and is still charged
its reservation — §1.4's "the engine did the work" still means what it said. Only the case where
nothing was ever written changes. Re-proved under four CPU hogs: `TestI6SettleTable` ×25 green,
I1/I5/I7/I8 ×10 green.

A write still in flight when `Do` returns reads as not-sent. That errs towards the friend, which is
the right direction for a billing decision, and `-race -count=10` is green over the atomic.

**Fixture, un-skipped and strengthened.** `TestQueueDepartureIsNeverCharged` now tags B's body and
asserts the engine never saw B *before* asserting the bill — so its premise is checked rather than
assumed, and a run where B did reach the engine reports that instead of a confusing overcharge.
**25/25 green under four CPU hogs**, where the same fixture was 1-4/25 red before the ruling.

**Second freeze, evidence printed.**

```
$ for i in 1 2 3 4; do yes > /dev/null & done          # hogs killed after; pgrep -x yes = 0
$ go test -count=25 -run TestQueueDeparture ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        18.256s
$ go test -count=25 -run TestI6SettleTable ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        36.868s
$ go test -count=10 -run 'TestI1|TestI5|TestI7|TestI8' ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        55.563s

$ go test -race -count=10 ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        192.889s
```

`go build ./... && go vet ./... && go test ./...` — every package ok:
**247 passed / 0 failed / 2 skipped**. The skip that was the contest is gone; the two remaining are
the pre-existing opt-in live tests.

**Declared loudly — the line budget.** 164 added / 3 removed against a stated **150**: **14 over**.
The ruling added a second file after the budget was set; `request.go` is 14 of the added lines, and
the fixture's new premise assertion is 3. I trimmed what I could (the trace callback to one line)
rather than trim the fixture's reasons. Not silent, and yours to rule on.

## Contest

> **Resolved 2026-09-02 17:40 EDT — accepted by the PM.** Scope extended to `callUpstream`; applied
> and proved above. Kept verbatim as the record of what was contested and on what evidence. One
> correction the implementation forced: the "smallest seam I propose" below is *not* sufficient on
> its own — see the ruling section for why, and what replaced it.

**Promise 2 cannot be met inside the scope contract.** The ticket scopes the fix to
`internal/gateway/queue.go`. Promise 1 closes the select race; it does not close the *departure*
race, because there is no last moment inside the queue at which the friend can be known to still be
there. After `wait` hands the slot over, the friend can leave one instruction later, and the request
is then settled by code the scope contract does not reach.

**Evidence.** With promise 1 in place, under four `yes > /dev/null` hogs:

```
$ go test -count=25 -run TestQueueDepartureIsNeverCharged ./internal/gateway/
--- FAIL: TestQueueDepartureIsNeverCharged            (1 of 25; 2 of 25 on the next run)
    a friend who left while queued was billed: {InFlight:0 RPMUsed:1 TPMUsed:2049 TodayTokens:2049}
    engine saw: [C] (requests=2)
    DBG callUpstream cut: key=k_alice1 reserved=2049 slot=true queuedMS=11
                          err=Post "http://127.0.0.1:60279/...": context canceled
```

`engine saw: [C]` is the whole argument: A and C reached the engine, **B never did**. B was charged
2049 tokens — its full `prompt + max_tokens` reservation — plus an RPM count, for a request that
never left the gateway.

I also tried the only tightening available inside `queue.go` — a second `ctx.Err()` check in
`acquire` after `wait` returns `nil` — and it still failed **2 of 25**. The queue is not the place.

**Exact symbol.** `internal/gateway/request.go`, `func (q *request) callUpstream()`, lines 350-353:

```go
	resp, derr := q.g.up.Do(ctx, http.MethodPost, string(q.kind), payload, q.n.stream)
	if derr != nil {
		if q.r.Context().Err() != nil {
			q.outcome = outcomeCut          // ← here
			return errf(CodeClientClosed, 0, "client went away")
		}
```

`settleRow` (`request.go:444-446`) then reads `outcomeCut` + non-stream as
`return true, q.adm.reserved`. DESIGN §1.4 justifies that row as "the engine did the work" — true
when the friend leaves while the engine is answering, false here: `up.Do` returned before a byte was
sent, so no work exists to charge for.

**Smallest seam I propose.** *(Superseded — this was one line short of correct; the sent/not-sent
fact has to be read from the transport, not from a context check. See the ruling section.)* At
`request.go:352` set `q.outcome = outcomeQueueLost` instead of `outcomeCut`. The friend left before the engine was ever
called, which is exactly the QueueLost row; `settleRow`'s existing
`return q.ev.Code == string(CodeQueueTimeout), 0` then yields uncounted / charged 0, and
`fail` already stamps the event 499 `client_closed`. Nothing else moves — `finish` still releases
the slot, the reservation, and the per-key in-flight the same way.

If the PM would rather keep the outcome name, the equivalent one-line alternative is in `settleRow`:
charge 0 for a Cut that never reached the engine. I prefer the first: `outcomeCut` should keep
meaning "bytes were exchanged", which is what makes charging it honest.

**What lands with the ruling.** Deleting the `t.Skip` at the top of
`TestQueueDepartureIsNeverCharged` (`queue_test.go`) turns the fixture on; it already asserts
counted 0, charged 0, no reservation standing, alice's event 499/`client_closed`, and that the slot
reached C. Estimated cost: 1 source line, 1 test line, ~10 minutes.
*(Actual: 14 source lines and 3 test lines — the estimate was wrong for the reason above.)*

## Freeze

| | |
|---|---|
| base | `002e67a` (origin/main; dispatched at `c0e1733`, rebased) |
| lane | `t021-queue-race` (PM's WIP commit squashed into one) |
| patch SHA-256 | `0b1bf9aaecd249ae5ec95cb8f73d74abfb9b1bfcc42324d7c678d96f9079cc7b` (`git diff 002e67a -- internal/ \| shasum -a 256`) — second freeze, supersedes `0a282ab…` |
| source lines | `queue.go` 11 added / 3 removed (6 of the 11 are comment); `request.go` 14 added / 0 removed (6 comment, 1 import) |
| test lines | `queue_test.go` 140 added / 0 removed |
| total | 164 added / 3 removed — **14 over the 150-line budget, declared** (the ruling added `request.go` after the budget was set: 14 of the 14) |
| concepts | **0 of 0** — no new outcome, error code, flag, config key or state file; `httptrace` is stdlib, no dependency added |
| contests | **1, resolved** — raised, accepted, applied in this same lane |
| bought beyond the ticket | nothing |
| production-touching actions | none. Four `yes > /dev/null` hogs started locally for the stress runs and killed after (`pgrep -x yes` = 0). max-ws.lab and the shared llama-server untouched. Temporary `logf` instrumentation in `request.go` was reverted; the diff touches only `queue.go`, `request.go` (`callUpstream`) and `queue_test.go`. |
| a reviewer might mistake for a bug | `outcomeCut` still charges the full reservation for a non-stream request whose friend left — that is DESIGN §1.4 and is deliberate. What changed is only which requests are Cut at all. |

## Ruling (PM, 2026-09-02 18:10)

**Contest accepted** (the remaining hole was in `request.go`, not the queue) and **landed** on main; gateway
`-race` and full tests green, printed. Re-priced to 2: the overrun is the scope the ruling added after
pricing. The `httptrace.WroteRequest` reading of "the engine received something" is accepted as the
truthful test — a context check cannot see a cancellation that lands inside the send. The settle table's
other rows are untouched (a request whose bytes reached the engine stays `Cut`). Lesson written for the PM:
when a contest is accepted mid-ticket, re-price at the same moment.
