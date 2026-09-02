---
id: 016
title: Host — seed daily counters from usage.jsonl at start; invite line after the QR
kind: normal
size: 1
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 016 — Host: durable daily counters, invite line placement

## Binding

**Why.** Both friend personas saw their usage meter snap to zero after the host restarted ("I'd used
5.8k tokens a minute earlier"), and one host persona lost the invite line under a 30-line QR.

**Promises.**
1. At gateway start, per-key `today` counters (and `last_seen`) are seeded from `usage.jsonl` since UTC
   midnight (one `Aggregate` call; `docs/DESIGN.md` §4 item 5). Test: record usage, restart the gateway
   with the same recorder file, `/me.usage.today_tokens` equals the pre-restart value.
2. `keys add` / `keys rotate` print the invite (or link) line again **after** the QR, so the last thing
   on screen is the thing to copy; `--no-qr`/`--json`/non-TTY behaviour unchanged.
3. `go test ./...` printed; a before/after of the `keys add` output in the report.

**Size 1** (≤150 source lines). Concept budget 0. **Normal**.

**Scope contract.** `internal/gateway/limits.go` (+ `gateway.go` for the seed call and a `Config`
field or constructor argument for the aggregate source — keep it to what promise 1 needs; ticket 014
is concurrently making a one-row change in `request.go`), `cmd/bunny-network/serve.go` (wire the
seed), `cmd/bunny-network/keys.go` (invite line), tests.

## Log

- 2026-09-02T19:25Z — ACK. Base `2103438`, lane `t016-host-counters`. Read BELIEFS Protections,
  ARCHITECTURE (pipeline, usage events, admin status), DESIGN §4 item 5, `limits.go`, `gateway.go`,
  `usage/aggregate.go`, `serve.go`, `keys.go`. No contest: promise 1 is exactly one `Config` field
  (`DataDir`) plus a seed call in `New`; the CLI's own `gatewayOptions` seam needs the same field
  mirrored (one line in `main.go`, one in `wire.go`) because `newGateway` takes no data dir.
  `request.go` untouched (ticket 014).
- 2026-09-02T19:52Z — FROZEN. Rebased on `origin/main` `efeccee` (the handoff commit; touched none of
  these files). Code commit `a836a29`; this report is a docs-only commit on top. Not merged.

## Report

Engineer Claude Fable 5.1, 2026-09-02. Lane `t016-host-counters`, base `efeccee`, code commit `a836a29`.

### The core, shown working

1. **A restart keeps the day.** `gateway.New` calls `seedCounters`: one `usage.AggregateFile(DataDir,
   Filter{Since: UTC midnight})`, then `limiter.seedToday` sets each key's `day`, `today`
   (prompt+completion, the same sum `settle` charges) and `lastSeen`. No new file, no second lane of
   truth, nothing pushed. `TestRestartKeepsTodaysCountersAndLastSeen` (internal/gateway) drives two real
   chat completions through the gateway with a **real `usage.FileRecorder`**, closes the server and the
   file, builds a second gateway over the same data dir, and reads it before anything touches it:

       today_tokens before restart: 16   /me: 16
       today_tokens after  restart: 16   /me: 16      last_seen restored (within a minute of the live value)
       rpm_used after restart: 0                      the sliding minute is deliberately NOT restored

   With `g.seedCounters()` removed the same test fails `today_tokens after restart: 0, want the 16
   charged before` — it is not vacuous. Cost: one `Config` field (`DataDir`), mirrored one line each in
   the CLI's own `gatewayOptions` (`main.go`) and its mapping (`wire.go`), because `newGateway` takes no
   data dir; `serve` passes the dir it already resolved. No contest was needed.
2. **The invite comes back under the QR.** `keys add alice`, before → after (QR rows collapsed):

       before                                          after
       …                                               …
       alice pastes this code into the web app…        alice pastes this code into the web app…
       <QR: 23 rows>                                   <QR: 23 rows>
                                                       ⏎  bn1.tcFAKE….mDO46HtF2Bv…      ← added
       Lost it? `… keys rotate k_90c63d` …             Lost it? `… keys rotate k_7598d1` …

   With a `web-url` configured the **link** is what the QR carries and what comes back, matching the line
   above it. `keys rotate` ends on that line. `TestInviteIsRepeatedUnderTheQR` asserts the count (twice,
   the second below the QR) for both the code and the link case, and that `--no-qr` and a piped stdout
   print it exactly once; `--json` is untouched (`printDestination` is not on that path).

**Edges seen and handled:** a data dir with no `usage.jsonl` (empty report, not an error) · an unreadable
one (logged once, counters start at zero, the host still starts) · events with an empty `key_id` (skipped:
not anyone's day) · a key with no history today (stays zero) · a malformed final line (`Aggregate` already
counts and skips it) · the seeded `day` is the same UTC midnight `prune` computes, so the totals survive
until the day genuinely rolls · reservations and the 60 s window are not restored, by design.

**One deliberate thing a reviewer might read as a miss:** in `keys add` the one-line `Lost it? …` hint
still prints after the repeated invite (it is the *last* line, the invite the last thing to copy). The
ticket asked for the line "again after the QR"; moving the hint was outside the promise.

### Verification (printed)

- `GOTOOLCHAIN=auto go build ./... && go vet ./... && go test ./...` — exit 0, 11 packages ok, 0 failed;
  `gofmt -l ./cmd ./internal` empty.
- `go test ./... -count=1 -v`: **135 passed / 0 failed / 2 skipped** (the two env-gated live tests,
  `TestLiveLlamaCPP` and `TestSavedAddrLive`). The gateway's own TestMain gate still prints `all 14 error
  codes exercised; secret absent`.
- Manual: the before/after above was captured by running the real `keys add` path (`run()` with tty on)
  against the pre-change and post-change `keys.go`. No binary was served, no live host, no secrets used.

### Freeze

- **Base:** `efeccee` (`origin/main` at freeze). **Lane:** `t016-host-counters`. Code commit `a836a29`;
  this report is a docs-only commit on top. Not merged.
- **Patch SHA-256** (`git diff efeccee..a836a29 -- internal cmd | shasum -a 256`):
  `81680c13f1d9d4c4429e5e9edf266e5f2ac8f87140a351a2533b85b59c58ad34`
- **Source diff** (tests excluded): **+55 / −6, net +49** — `internal/gateway/gateway.go` +23/−1,
  `limits.go` +21/−2, `cmd/bunny-network/keys.go` +8/−3, `main.go` +1, `serve.go` +1, `wire.go` +1.
  ~24 of the 55 are comment lines. Size 1 ceiling ≤150: **37 % of budget**.
- **Tests:** +145 / −1 — `gateway_test.go` +72 (the restart test + a `meUsage` helper),
  `main_test.go` +50 (the QR-repeat test), `fakes_test.go` +23/−1 (a tee recorder so a harness with
  `DataDir` writes the real usage.jsonl).
- **Concepts:** budget 0, **0 spent**. No new CLI verb, flag, error code, config key or state file.
  `Config.DataDir` + the mirrored `gatewayOptions.DataDir` are the constructor input the ticket's scope
  contract authorised. No new dependencies. `internal/gateway/request.go` untouched (ticket 014).
