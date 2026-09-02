---
id: 021
title: Queue departure race — a client that leaves while waiting must never take the slot (DESIGN §1.4 QueueLost row)
kind: sensitive
size: 1
status: dispatched
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

## Report
