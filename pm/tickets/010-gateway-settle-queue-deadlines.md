---
id: 010
title: Gateway settle table, FIFO slot queue, engine-side deadlines (DESIGN §1.4–1.6)
kind: sensitive
size: 3
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 010 — Gateway settle table, slot queue, deadlines

## Binding

**Why.** `docs/DESIGN.md` §1.3 shows what the landed pipeline (006) still leaves implicit: the charge
rule in three places, a boolean that counts refused requests against RPM, a channel semaphore that is
not FIFO and over-admits on resize, and an absolute 300 s cap that cuts a slow-but-live public host
mid-answer. A public launch should not wait on none of these; it should wait on all of them.

**Promises.** Implement §1.4, §1.5, §1.6 and §1.7 of `docs/DESIGN.md` as written, on top of main:
1. `outcome` on the request record, set by the stage that ends the request; one `settle(a, counted,
   charged)` on the limiter replacing `release` + `abort`; `finish` reads the settle table verbatim
   (§1.4). Admission entries carry a `seq` so a rejection removes exactly its own entry.
2. Reservation at `checkBudgets` = `prompt + max_tokens`, shrunk to fit TPM and daily the way context
   shrinks (floor 16, else 429 `rate_limited` with the numbers); settle corrects to the charged amount.
3. `slotQueue` (§1.5): FIFO, cap read live from `Info().Slots` (override applied, min 1), waiting set
   capped at max(2, 2×cap) with immediate 503 for overflow, exact `Queue()` under one mutex. `SetSlots`
   deleted from the gateway, `gatewayServer`, `main.go`, and `refreshLoop`'s slot plumbing.
4. Deadlines per §1.6 table: engine first-byte (`ResponseHeaderTimeout` 120 s) and engine idle (60 s,
   reset per line) added; absolute `RequestTimeout` deleted with its flag and config key;
   `--queue-timeout` and `--max-body` become constants (flags and config keys deleted; `config.json`
   with the old keys still loads — ignore unknown keys).
5. `normalized` value + the §1.7 post-condition table pinned by a test.
6. **Evidence:** the invariant tests DESIGN §1.8 lists as missing — I1 (every exit × every counter = 0),
   I5 (randomized ceiling: Σ reservations + Σ charges ≤ TPM), I6 (one test row per outcome), I7 (FIFO
   order and resize without over-admission), I8 (five deadline fixtures incl. the slow-but-live stream
   that must NOT be cut) — plus the existing 41 gateway tests adapted where the contract changed (name
   each adaptation in the report). `go test -race -count=1 ./internal/gateway/` printed.

**Size 3** (≤900 source lines net). Concept budget 0 (−1 flag, −1 config key, −1 method, +1 internal
enum). **Sensitive** (Protection 4).

**Scope contract.** `internal/gateway/**`; `cmd/bunny-network/serve.go`, `config.go`, `main.go` only
for the `SetSlots` and flag/config-key deletions; `docs/ARCHITECTURE.md` is the PM's (list the contract
lines that changed in the report; do not edit). Ticket 009 is concurrently editing `cmd/bunny-network`
(banner, keys, usage words) — keep your `cmd/` edits to the deletions so the rebase is trivial.

## Background

- `docs/DESIGN.md` §1 is the design; anchors are at `5456e59`, main has moved slightly (README, 008).
- 006's report and ruling in `pm/tickets/006-*.md` explain the current shape and judgment 7 (which
  this ticket overrides by ruling).

## Log

- 2026-09-02 11:30 ACK. Base fe9a139 (origin/main), lane t010-gateway-settle. Read BELIEFS, DESIGN §0/§1/§3/§4/§6, tickets 006/008/011, internal/gateway/** and the cmd seam. Baseline `go build/vet/test ./...` 11 packages ok. No contest: §1.4–1.7 fit inside internal/gateway/** plus the cmd deletions. Two things the design leaves to the implementer, decided up front: (1) the engine first-byte bound cannot be `ResponseHeaderTimeout` on the engine transport in this ticket (client.go is 011's file), so it is a timer on the upstream context owned by `callUpstream`, for 011 to replace; (2) `Config.Slots` goes with `SetSlots` (the queue reads `Info().Slots`), so `QueueTimeout`/`MaxBody`/`Slots` leave `gateway.Config` and tests set the fake engine's Slots and unexported knobs (006 judgment 9).
- 2026-09-02 11:58 FROZEN on base f9b1f2e (rebased over 007 + handoff, no overlap). Code commit 0f36afe.
- 2026-09-02 12:05 RE-FROZEN on base 19e3598 at the PM's request (009 landed first; two conflict hunks in serve.go/config.go, 009's additions kept beside the deletions). Code commit 1b8ee5b. Checks re-run and re-printed; the freeze block below is the current one.

## Report

Engineer Claude Fable 5.1, 2026-09-02. Lane `t010-gateway-settle`, rebased on `origin/main` `19e3598` (007, the
handoff and 009 landed after dispatch; 009's `serve.go`/`config.go` edits — `--web-url`, `configPath`, `hostName` —
conflicted with this ticket's flag deletions in two hunks, resolved keeping both). Code commit `1b8ee5b`; this
report is a docs-only commit on top. Not merged.

### The core, shown working

1. **One settle table decides what counts and what is charged.** `request.go` `settleRow` is DESIGN §1.4's table
   verbatim, read by `finish` (the one exit); every stage past the queue sets `outcome`, a stage that fails before it
   leaves it unset and that is a rejection. `limits.go` `settle(a, counted, charged)` replaces `release` + `abort`;
   the admission carries its own RPM entry's `seq`, so an un-counted request removes exactly its entry (proven with
   `max_concurrent > 1` in `TestLimiterWindowsWithFakeClock`). `TestI6SettleTable`, twelve rows, prints per row the
   RPM delta and the tokens charged: Rejected (context, waiting-set full) 0/0 · QueueLost timed out 1/0 · QueueLost
   client gone 0/0 · EngineErr (500, first byte) 1/0 · Served with usage 1/8 · Served stream without usage 1/(3+4) ·
   Cut stream client gone 1/(1+chunks) · Cut stream engine idle 1/(1+2) · Cut non-stream client gone, before and
   after the headers, 1/**52 = the reservation** (was 0: the 006 backlog row closes).
2. **The reservation is the worst case, shrunk to fit.** `checkBudgets` reserves `prompt + max_tokens`; `fitBudget`
   applies the context rule's shape to TPM and to the daily budget (floor 16, else 429 with the numbers).
   `TestTPMAndDailyAfterRealUsage` on a TPM-26 key: the engine saw `max_tokens` **24**, then **16**, then
   `429 rate_limited "token limit: 26 tokens per minute; 16 used, this request needs at least 18"`; a 10-token-TPM
   key is refused outright. `TestI5CeilingRandomized` (5 000 random admit/reserve/settle/clock steps, seed printed)
   and `TestI5CeilingThroughThePipeline` (6 goroutines × 20 requests against an engine reporting honest usage, sampled
   every 200 µs) never see `Σ reservations + Σ charges > TPM` nor `today + reservations > daily`.
3. **The queue is a queue.** `queue.go` `slotQueue`: FIFO, cap = `up.Info().Slots` read under the mutex at every
   decision, waiting set `max(2, 2×cap)`, `Queue()` exact. `TestI7FIFOAndResize`: cap 2, four waiters B C D E, the
   engine sees `[A1 A2 B C D E F]` in arrival order as slots free; cap **2 → 1** mid-run: B's release admits nobody
   (`Queue() = 1, 2`, engine requests unchanged after 100 ms), C's hands D one slot; cap **1 → 2**: a newcomer F lets
   the older E in first and queues behind it; a sampler on `Queue()` reports a peak of exactly 2 slots held. `SetSlots`
   is gone from `Gateway`, `gatewayServer`, `refreshLoop` and `main.go`.
4. **Every deadline bounds one party.** `TestI8Deadlines`: an engine that sends headers and nothing else is cut by the
   idle deadline (stream: SSE `upstream_error` event; non-stream: 502 "stopped answering for 200ms"), one that never
   sends headers by the first-byte deadline (502 "did not answer within 200ms", engine cancelled), and **a live
   stream slower than every deadline is never cut** (every deadline 250 ms, one event per 120 ms for 3 s → `[DONE]`,
   25 tokens, no code). The absolute `RequestTimeout` is deleted with `--request-timeout`/`request_timeout`;
   `--queue-timeout`/`--max-body` and their keys too (an old `config.json` with the three keys still loads:
   `TestConfigRoundTripAndDefaults`).
5. **Exactly-once release, one table.** `TestI1ExactlyOnceRelease`: 29 exits (the 14 codes with their variants,
   success non-stream/stream/embeddings/models, stop mid-stream, client gone while queued, waiting set full, stalled
   body, stalled reader, engine idle, panic) × `bodies == 0`, `Queue() == (0, 0)`, every key's `inFlight == 0`,
   reservations 0 (negative would fail too: settle's clamps are gone), and the next request goes through.
6. **Normalization pinned.** `normalize` returns `normalized{body, model, stream, text, maxTok, stripped}`;
   `TestI9NormalizationPostConditions`, 13 fixture rows, asserts after the pipeline: no denylisted key at the engine,
   the cap present and numeric and `≤ key cap`, `≤ max(eff − prompt, 16)`, `≤ TPM − used − prompt`; model present and
   allowed; `include_usage` iff `stream`; every other field byte-identical (`json.Number`), nothing invented.
7. **Live, through the pipeline** (shared llama-server `127.0.0.1:18080`, requests only, Gemma 4 E2B):
   stream 200, TTFT 76 ms, **170.4 tok/s**, usage `{27, 120}`; non-stream 200; `max_tokens 8 + n_predict 40 +
   ignore_eos` → completion_tokens **8**; 422 with the real count; 429 with Retry-After 59.

### Verification (printed, on base `f9b1f2e`)

- `go build ./...` exit 0 · `go vet ./...` exit 0 · `go test ./...`: **11 packages ok, 0 failed**. `gofmt -l` empty.
- `go test -race -count=1 ./internal/gateway/`: **48 passed / 0 failed / 1 skipped** (the opt-in live test) at top
  level, 59 subtests passed / 0 failed; TestMain gate `all 14 error codes exercised; 28 log lines captured, secret
  absent`. Stable under `-race -count=3` (52 s).
- `BN_LIVE_UPSTREAM=http://127.0.0.1:18080 go test -race -run TestLiveLlamaCPP -v`: **1 passed** (numbers in core 7).

### Adaptations to existing tests (each named; the contract changed underneath)

- `gateway_test.go`: `TestBodyCap` (body cap is the `maxBody` knob) · `TestTPMAndDailyAfterRealUsage` (TPM/daily 26
  instead of 10; asserts the 24 → 16 shrink and the "at least 18" message; the 10-token key is asserted refused) ·
  `TestPerKeyConcurrency`, `TestGlobalQueueTimeout` (engine slots / `queueTimeout` knob) ·
  `TestSetSlotsResizesTheGlobalQueue` → **`TestQueueFollowsEngineSlots`** (the queue follows `Info().Slots` on the next
  acquire; 0 means 1) · `TestUpstreamFailures` (`firstByteTimeout` knob; the hanging engine is now the first-byte
  502) · `TestStreamUpstreamDiesMidway` (the engine stalls after two events and the idle deadline cuts it; exactly 2
  charged; engine cancelled) · `TestLimiterWindowsWithFakeClock` (admission/settle API; a row proving settle removes
  its own entry by `seq`).
- `hardening_test.go`: `TestAdmissionBeforeBody`, `TestPreQueueRejectionIsUncounted`, `TestStalledReaderFreesSlots`,
  `TestModelsMetered` (knobs and engine slots only) · `TestWaitingQueueBounded` (adds: the two refused on the spot are
  not counted, `RPMUsed == 3`).
- `live_test.go`: `Config` without `Slots`. `fakes_test.go`: default engine `Slots` 1 (the gateway's old default);
  `stallAfter`, `delay`, an honest `usage` mode, arrival `tags`, `h.slots(n)`; sse headers flushed at once.
- `cmd/bunny-network/main_test.go`: `TestConfigRoundTripAndDefaults` (retired keys out of the round-trip; a pre-010
  `config.json` carrying them loads; `durOr` gone) · `TestServeRoutesTunnelLogAndFollowsSlots` (captures the engine
  from `newGateway` and waits for `Info().Slots == 3` instead of a `SetSlots` push; the "engine slots: 3 (was 1)" line
  is still asserted) · `fakeGateway` loses `SetSlots`.

### Contract lines that changed (docs/ARCHITECTURE.md is the PM's; not edited)

- **§Concurrency & queue:** "Global semaphore = upstream slots" → a FIFO queue whose capacity is the engine's slot
  count read live; waiting set `max(2, 2×slots)`, overflow 503 `queue_timeout` at once and **not** counted against
  RPM; a queue timeout is counted. `--queue-timeout` → constant 30 s. `--request-timeout` (300 s) → **deleted**;
  in its place engine first-byte 120 s, engine idle 60 s, client write 60 s, body read 30 s (§1.6 table). `--max-body`
  → constant 4 MiB.
- **Limits:** TPM and daily are ceilings for tokens: reservation `prompt + max_tokens`, shrunk to fit (floor 16) else
  429 "needs at least N"; settled to the charge. A key whose TPM cannot hold `prompt + 16` is refused.
- **Charging:** the §1.4 settle table (a non-stream request the friend abandons is charged its reservation).
- **`config.json`:** `queue_timeout`, `request_timeout`, `max_body` retired (ignored on load, dropped on save).
  **`serve` flags:** three fewer. **Admin `queue:{in_flight, waiting}`:** exact.

### Judgment calls (declared)

1. **First-byte bound lives in `callUpstream` as a timer on the upstream context**, not as `ResponseHeaderTimeout`:
   the engine transport is `client.go`, 011's file. Same bound (dial + write + headers ≤ 120 s), one owner, and a
   slow-but-live stream is never touched by it; 011 replaces ~10 lines with the transport field.
2. **`Config.Slots`, `QueueTimeout`, `RequestTimeout`, `MaxBody` leave `gateway.Config`** (the queue reads the engine;
   the rest are constants); the deadlines are unexported test knobs (006 judgment 9). `wire.go` got the same four-line
   deletion (not in the scope list; it could not compile otherwise).
3. **Client gone while the engine was working for them, before its first byte, is Cut** (the table has no row): a
   stream is charged the prompt (0 chunks), a non-stream request the reservation — the alternative (EngineErr, 0)
   would let a friend burn up to 120 s of engine time per RPM for free. An engine that dies mid-body is EngineErr (0).
   The `outcomeCut` comment gained "(2xx started or not)"; both rows are in I6.
4. **The event keeps what was observed**; only the non-stream Cut row's charge (the reservation) differs from its
   event's token sum, so `usage` history and live counters differ by the reserved completion on that one row.
5. **A chat with no cap anywhere is unbounded:** a TPM or daily ceiling that is set becomes its cap (`max_tokens :=
   room`); with no ceiling set `max_tokens` stays absent (`TestMaxTokensClampAndPassthrough`'s "not invented" holds).
6. **FIFO across a cap increase:** `fill` hands slots to waiters before a newcomer takes one; the design's pseudo-code
   ("if inFlight < cap → take") would let a newcomer jump the queue in that one window. Every other case is as written.
7. **The idle deadline is re-armed per read** (an `idleReader` on the engine body, one owner: `relay`), which also
   bounds a non-stream body; the design says "per line" — same bound.
8. **A panic (no outcome) is settled as a rejection** (not counted, 0): `TestPanicLeavesThroughFinish`'s `RPMUsed 0`.
9. **§1.7's "`max_tokens ≤ eff − prompt`" is `≤ max(eff − prompt, 16)`:** the context floor (005 10c, "never rejects")
   lets `prompt + 16` exceed the context at the boundary; TPM and daily reject at the floor instead.
10. **`refreshLoop` keeps the "engine slots: N (was M)" line** as a host surface; the push and the `gw` parameter go.
11. **The TPM/daily 429 message** says "this request needs at least N" (prompt + floor) instead of the prompt.

### Not verified

- vLLM live (llama.cpp only); the first-byte timer against a real slow engine (the live one answers in 76 ms).
- Through 001's real tunnel listener: httptest only. Timing-based rows (I6 cut, I7, I8) passed ×3 under `-race` on
  macOS; Linux scheduler timing untested. The I5 pipeline sampler is best-effort (200 µs); the limiter-level walk is
  the exact check.

### Candidates for the PM (not done)

- `intOr` in `config.go` was dead before this ticket (6 lines). `countTimeout` still wraps the model list (011 deletes
  it). If the PM wants the non-stream Cut event to carry the charge instead of the observation, it is one line in
  `settleRow`.

### Freeze

- **Base:** `19e3598` (`origin/main` at freeze; dispatched at `fe9a139`, rebased over 007, the handoff and 009).
  **Lane:** `t010-gateway-settle`. Code commit `1b8ee5b`; this report is a docs-only commit on top.
- **Checks at this base (re-run after the 009 rebase, printed):** `go build ./...` 0 · `go vet ./...` 0 ·
  `go test ./...` 11 packages ok · `go test -race -count=1 ./internal/gateway/` **48 passed / 0 failed / 1 skipped**,
  59 subtests, TestMain gate "all 14 error codes exercised, secret absent" · `gofmt -l` empty.
- **Patch SHA-256** (`git diff 19e3598..1b8ee5b -- internal cmd | shasum -a 256`):
  `cce889698033a6b073d540a2ca6b14a55bf288627ea8b460d27b577182e2a680`
- **Source diff** (tests excluded): +532 / −354, **net +178** — `queue.go` +100 (new), `request.go` +177/−85,
  `limits.go` +109/−63, `proxy.go` +82/−31, `gateway.go` +39/−108, `cmd/bunny-network` `config.go` +10/−31,
  `serve.go` +9/−21, `main.go` +3/−8, `wire.go` +3/−7. Gateway package non-blank-non-comment source 1266 → 1403.
  Size 3 ceiling ≤900: 20 % net, 886 changed.
- **Tests:** +1173 / −85 — `invariants_test.go` 1004 (new: I1, I5 ×2, I6, I7, I8, I9), `gateway_test.go` +88/−57,
  `fakes_test.go` +52/−10, `hardening_test.go` +12/−7, `main_test.go` +16/−10, `live_test.go` +1/−1.
  48 tests + 59 subtests + TestMain gate + 1 opt-in live.
- **Concepts:** budget 0; net **−5**: −3 flags (`--queue-timeout`, `--request-timeout`, `--max-body`), −3 config keys,
  −1 interface method (`SetSlots`), +1 internal enum (`outcome`); `slotQueue`, `admission`, `normalized` are internal
  structure. No new error codes, routes, files, or dependencies (stdlib only; `go.mod` untouched).
- **Files outside `internal/gateway/**`:** `cmd/bunny-network/{config,main,serve,wire}.go` and `main_test.go` for the
  deletions only; this ticket file. `docs/` untouched.
- **Production-touching actions:** none. The shared llama-server received the opt-in live test's requests only; nothing
  restarted or reconfigured; no secrets, no dotenvx, no max-ws.lab.
