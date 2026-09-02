---
id: 019
title: TestI6SettleTable is load-sensitive — make it deterministic
kind: normal
size: 1
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 019 — Settle-table test flake

**Why.** In the fresh-clone launch gate (main `647f62b`), `go test -count=1 -v ./internal/gateway/`
reported `--- FAIL: TestI6SettleTable (0.98s)` once while the machine was under load (two Playwright
browsers and two hosts running for the experience pass). Alone, ×3, and under `-race` ×3 it passes;
four more full-package verbose runs pass. A test that fails under load is a defect in the test
(family playbook: zero flaky exemptions).

**Promises.**
1. Find the real-time dependency in `TestI6SettleTable` (candidates: the "Cut: stream, engine idle"
   and "EngineErr: first byte" rows, which exercise deadlines; any `time.Sleep`-based ordering; the
   `QueueLost: timed out` row's QueueTimeout) and remove it: inject the clock/deadlines the gateway
   already parameterises, or make the assertion wait on the event it needs rather than on elapsed time.
2. Evidence: `go test -count=25 -run TestI6SettleTable ./internal/gateway/` green, and the same while a
   CPU hog runs (`yes > /dev/null` ×4 in the background, killed after); `-race` green. Print both.
3. Sweep the other invariant tests (I1, I5, I7, I8) for the same pattern; fix any that share it within
   the budget, else list them.

**Size 1** (≤150 lines, tests only). Concept budget 0. **Scope:** `internal/gateway/*_test.go`; if the
fix needs a non-test seam (an injectable clock), contest with the symbol.

## Log

- **2026-09-02 16:45 EDT** — ACK. Base `9b88ff0` (rebased at freeze to `414a830`), lane
  `t019-test-flake`. Read the ticket, DESIGN §1.4-1.6 and §1.8, `invariants_test.go`,
  `fakes_test.go`, `request.go`, `queue.go`, `proxy.go`, `limits.go`, and the deadline constants in
  `gateway.go`.
- **2026-09-02 16:50 EDT** — Reproduced. `-count=1` alone is green; so is `-cpu 1` alone; so is
  `-count=8` under 36 `yes` hogs. The failure needs both: `-count=10 -cpu 1` under 36 hogs failed
  3/10, and `-count=30 -cpu 1` failed **5/30** — every one of them
  `TestI6SettleTable/QueueLost:_client_gone`, `counted 1, want 0`. Not the rows the ticket suspected.
- **2026-09-02 16:55 EDT** — Diagnosed with temporary `logf` instrumentation in `pipeStream` (since
  reverted; `git diff` touches no non-test file). The counted request's event was
  `Status:499 Code:client_closed QueuedMS:5 TotalMS:5`, and the key's counters after it were
  `RPMUsed:1 TPMUsed:2049 TodayTokens:2049` — a 1-token prompt charged its whole 2048-token
  reservation. Mechanism in the report below.
- **2026-09-02 16:58 EDT** — Fixed inside `internal/gateway/*_test.go` only; no non-test seam was
  needed, so no contest. Swept I1/I5/I7/I8.
- **2026-09-02 17:00 EDT** — Regression check under the exact repro condition (`-count=30 -cpu 1`,
  36 hogs, load average 48): **30/30 green** where the same command was 5/30 red.
- **2026-09-02 17:15 EDT** — Ticket evidence printed, rebased onto `414a830`, froze.

## Report

**Root cause.** Not a deadline firing early — the `QueueLost: client gone` row proceeds on a
*client-side* observation while the gateway still holds server-side state. The row cancels alice's
request, waits for her client goroutine to return (`<-done`), and then immediately cancels bob's
holding stream. But the client returning says nothing about the handler: under load alice's handler
can still be parked in `slotQueue.wait`, whose `select` has both `w.ch` and `ctx.Done()` ready once
bob's release calls `fill` (`queue.go:83-96, 110-118`). Go chooses between two ready cases at
random, so about half the time the departed request takes the slot instead of the cancellation,
reaches `callUpstream` with an already-cancelled context, and settles as `outcomeCut` — which the
settle table counts against RPM and charges the whole reservation (`request.go:445-449`). The row
expects `0 / 0` and sees `1 / 2049`. Anything that widens the gap between "the client gave up" and
"the handler noticed" — CPU pressure, `GOMAXPROCS=1`, the launch gate's two Playwright browsers —
opens the window.

**What changed** (tests only, `internal/gateway/*_test.go`; 37 added / 16 removed):

- **I6 `QueueLost: client gone`** — the row now waits for alice's *usage event* before releasing
  bob's slot. The event is recorded at the end of `finish()`, so it is proof the place was given up
  and the release can no longer race the departure. The same one-line guard went onto the identical
  I1 row (`client gone while queued`), which shares the pattern but has assertions loose enough to
  have hidden it.
- **I6 `Cut: stream, engine idle`** — the ticket's prime suspect, and a real thin margin: a 50 ms
  engine gap against a 100 ms idle deadline, so the count `1 + 2` needed the gateway to read two
  events 50 ms apart inside a deadline only twice that gap. The engine now writes both deltas back
  to back (`gap = 0`) and then goes silent, with the deadline at 1 s — roughly 1000x the delivery,
  so what the gateway counted cannot depend on how loaded the machine was.
- **I6 `Cut: stream, client gone`** — the upper charge bound `1 + 10` was a guess about how many
  more 50 ms chunks the gateway would read before noticing the friend had gone. The engine now
  emits exactly six deltas back to back and holds, so `1 + 3 .. 1 + 6` is the fixture, not the clock.
- **I6 `Cut: non-stream, client gone before the body`** — replaced `time.Sleep(50 ms) // the
  engine's headers are out; the body is not` with a channel the fake engine closes at exactly that
  moment (`fakes_test.go`: `headers`, 4 lines). The row now guarantees the state it claims; before,
  a slow machine could silently turn it into the *next* row.
- **I6 `Served: no usage object (stream)`** — `gap = 0`: 200 ms of pure wall clock removed.
- **I7** — `queueTimeout` 10 s -> 1 min. Every waiter there is released by name; nothing in that
  test should ever end on a clock, and 10 s was reachable under the load above.
- **I8** — the `slow but live stream is never cut` fixture ran a 120 ms engine gap against 250 ms
  deadlines: a 2x margin on the one test whose entire point is that a live-but-slow stream survives.
  Deadlines raised to 1.2 s (10x the gap); duration and assertions unchanged. The three
  "the deadline fired rather than hanging" upper bounds widened from 2 s to 5 s. Honest note: I8 was
  never observed failing (10/10 green under the repro load with the old values) — this is margin
  hardening, not a fixed failure.

**Stress evidence, printed.**

```
# before, the repro condition: 36 `yes` hogs, GOMAXPROCS=1, load average 48
$ go test -count=30 -cpu 1 -run TestI6SettleTable ./internal/gateway/
--- FAIL: TestI6SettleTable/QueueLost:_client_gone            (5 of 30 runs)
    counted 1, want 0 (event {... Status:499 Code:client_closed QueuedMS:5 TotalMS:5})

# after, same condition
$ go test -count=30 -cpu 1 -run TestI6SettleTable ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        44.136s

$ go test -count=25 -run TestI6SettleTable ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        37.034s

$ for i in 1 2 3 4; do yes > /dev/null & done      # the ticket's four hogs; killed after the run
$ go test -count=25 -run TestI6SettleTable ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        36.908s

$ go test -race -count=3 ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        57.352s

# sweep, still under the 36 hogs
$ go test -count=10 -cpu 1 -run 'TestI1|TestI5|TestI7|TestI8' ./internal/gateway/
ok      github.com/2185Lab/bunny-network/internal/gateway        55.511s
```

**Freeze check.** `go build ./... && go vet ./... && go test ./...` — every package ok:
**245 passed / 0 failed / 2 skipped** (gateway alone: 115 / 0 / 1 skipped — `TestLiveLlamaCPP`,
opt-in). `TestI6SettleTable` goes 0.98 s -> 1.47 s; the gateway package is unchanged at ~16.6 s.

**What else I found — one adjacent defect, not fixed (scope).**
The race the test kept tripping over is real product behaviour, and it overcharges. When a friend
disconnects while queued and a slot is handed over in the same instant, `slotQueue.wait`
(`queue.go:83-96`) can return the slot rather than the cancellation; the request then fails at
`callUpstream` before the engine sees a byte, settles as `Cut, non-stream`, and is charged its whole
reservation (`prompt + max_tokens`) plus an RPM count — 2049 tokens for a 1-token prompt in the
reproduction, for work no engine did. DESIGN §1.4 says a friend who leaves while queued is
QueueLost: uncounted, charged 0. Smallest honest fix is one line in `wait` (check `ctx.Err()` before
returning `nil`, so a handed-over slot goes back through `acquire`'s hand-it-on path) plus the
fixture that would have caught it. **Candidate ticket.**

Two further observations, no action taken:

- I7 still has one `time.Sleep(100 ms)` after the cap 2 -> 1 release. It is a *negative* assertion
  window ("no hand-over happened"), so it can only produce a false pass, never a false failure.
  Left deliberately.
- `TestI5CeilingThroughThePipeline`'s 200 microsecond sampling loop is a poll, not an ordering
  dependency; `TestI5CeilingRandomized` already injects its clock.

## Freeze

| | |
|---|---|
| base | `414a830` (origin/main at freeze; dispatched at `9b88ff0`, rebased) |
| lane | `t019-test-flake` |
| patch SHA-256 | `f31c6e5c6edc8bb41e6603982e7c4ca9690ef6f6276036905f45d9984242dc2e` (`git diff 414a830 -- internal/ \| shasum -a 256`) |
| source lines | 0 added / 0 removed — no non-test file is touched (budget: none; scope was tests only) |
| test lines | 37 added / 16 removed = **53 of the 150-line budget** (`fakes_test.go` 4/0, `invariants_test.go` 33/16) |
| doc lines | ticket record only |
| concepts | **0 of 0** — no new flag, verb, error code, config key or state file; one unexported test-fixture field (`fakeUpstream.headers`) |
| contests | none — the fix needed no injectable clock or deadline in non-test code |
| bought beyond the ticket | nothing |
| production-touching actions | none. 36 and then 4 `yes > /dev/null` CPU hogs were started locally for the stress runs and killed after (`pgrep -x yes` = 0); no other process was signalled. max-ws.lab and the shared llama-server were not touched. |
