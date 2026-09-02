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

## Report
