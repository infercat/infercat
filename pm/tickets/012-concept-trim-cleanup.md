---
id: 012
title: Concept trim and CLI/store cleanup (DESIGN §4 after-launch items, §6 tickets 4–5)
kind: normal
size: 2
status: draft
updated: 2026-09-02
release: after-launch
---

# 012 — Concept trim and cleanup (after launch)

Binding, from `docs/DESIGN.md` §6 tickets 4 and 5 and §4 items 1, 4, 5, 9, 10, 11, 14, 15, 21:
replace `reorder()` with the interspersed stdlib parse loop (keep both tests); shrink the `platform`
seam; delete the store throttle and miss-path reload (stat on every call; keep the stamp compare);
seed daily counters from `usage.jsonl` at start; `status` label "N open connections"; Windows
second-serve guard via token dial and admin-before-config ordering; usage events for POST routes only
(contract line, ruled 2026-09-02); fold `retryAfterUpstreamDown`/`refreshEvery` into
`upstream.ProbeInterval`; `/v1/models` as a kind in the stage table. Concept budget negative.
Not scheduled; dispatched after the public launch.

## Log

## Report

## Backlog appended 2026-09-02 (fourth experience pass, ruled non-blocking)

Web: composer focus on chat mount · Escape/click-outside closes Settings and Limits sheets · the
returning card shows immediately after Disconnect (not only after reload) · per-minute meter refills
client-side between polls · self-probe backoff cap 10 s + probe on `visibilitychange` · composer that
looks like it holds a long draft after a refused paste · the first ~15 s after a host kill still show the
last live path reading · context meter estimate while streaming. Gateway: `/v1/models` refused at the RPM
ceiling (one line in `limits.go`).
- Fourth pass, final ruling: a "host is back" line in the thread and a faster heal (≤30 s) · composer
  height resets after send · per-minute meter "0 left" while sends still work (refill between polls) ·
  follower-tab composer placeholder · the card right after Disconnect · context-meter story around the wall.
- Founder found 2026-09-03: the connect screen sat on "Connecting to the relay…" indefinitely when the
  host behind the invite did not exist (the PM's host had failed to start). The relay handshake needs a
  bound (the bridge's 60 s is too long and today it never resolves into the honest "host didn't answer"
  state); target: ~20 s, then the asleep/offline copy with Try again. Web, size 1.
