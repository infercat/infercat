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
