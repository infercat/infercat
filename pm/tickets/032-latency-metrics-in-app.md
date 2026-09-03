---
id: 032
title: Web app — show TTFT and time-per-output-token on each reply, honestly attributed
kind: normal
size: 1
status: dispatched
updated: 2026-09-03
release: demo-1
---

# 032 — TTFT / TPOT in the reply footer

**Why.** Founder (2026-09-03): show latency metrics in the web app. The technical launch audience
reads these numbers; the desktop reviewer already verified the token counts against the host, and
latency is the next thing they will check. The client has the data: first-delta timestamp, last-delta
timestamp, completion tokens from the usage chunk, and the `queued` keepalives (018) that mark time
spent waiting for a slot.

**Promises.**
1. Reply footer gains `ttft 61 ms · 42 tok/s` (tokens per second is what this audience says; also
   expose TPOT = 1000/tok/s in the hover/sheet). Measured **from the device**: TTFT = send → first
   content-or-reasoning delta; tok/s = completion tokens ÷ (last delta − first delta). Stated as such.
2. **Attribution is honest:** if the request was queued (keepalives seen), the footer shows
   `waited 4.2 s for a slot · ttft 61 ms` so the wait is not blamed on the model; if the path is
   relayed, the sheet says the relay hop is included (path RTT from the pill). Thinking tokens count in
   tok/s only if they streamed (they do); the sheet says whether the number includes thinking.
3. A per-chat average in the Limits/Settings sheet (median TTFT and tok/s over this chat), plus the
   same two numbers in `/me`-independent local state so they survive reload with the chat.
4. Evidence: vitest on the derivation (with queued keepalives, with thinking-only replies, with
   abort); a real-host screenshot; cross-check one reply's numbers against the host's `usage` line for
   the same request (they will differ by the relay hop — say by how much in the report).

**Size 1** (≤150 lines). Concept budget 0. **Scope:** `web/src/stream.ts` (timestamps), `Message.tsx`,
the sheet; tests. Same lane as 031 (same footer).

## Log

- **2026-09-03 03:22 ACK.** Base `3eb9033`, lane `t031-web-footer` (shared with 031; 031 lands first,
  one commit each).
- **03:26 Read before edit.** The gateway writes `: queued` on joining the slot queue and every 5 s
  (`internal/gateway/request.go:317`, `defaultQueuedEvery`) and **nothing when the slot is granted**, so
  from the device the wait for a slot is a lower bound (send → last keepalive) at 5 s granularity, while
  send → first delta is exact. The footer will say the exact number and the bound, never a split it
  cannot see. Timestamps go on the reply as it is reduced (`reduceReply` takes a clock, default
  `Date.now()`), persist with the message, and the sheet's medians are derived from the chat's messages
  — no second store, nothing to keep in step.

## Report
