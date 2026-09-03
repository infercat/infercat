---
id: 023
title: Reconnect joins the in-flight self-probe dial instead of starting a second one
kind: normal
size: 1
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 023 — One dial per host

**Why.** Found by 022's evidence run: after the session degrades, the self-probe (promise 1) opens a
fresh dial; if the friend presses Reconnect while that dial is in flight (5 s after degrading, up to
60 s), a second dial starts under the same tunnel identity and the app hangs on "Opening a fresh
connection…" until a reload. Reproduced twice on the real host. The fourth pass will press that button.

**Promises.**
1. One in-flight dial per host: Reconnect joins the dial the self-probe already started (the
   `connecting` state carries the redial target); a second request while one is in flight is a no-op
   that shows the same progress.
2. The redial's `/me` has a timeout (10 s) so a hung dial resolves to `degraded` with the honest copy,
   never a permanent "Opening a fresh connection…".
3. Two vitests over the reducer (join, timeout) and one real-host probe: press Reconnect 6 s after
   killing the host, restart the host, connected within 30 s; printed. Checks printed.

**Size 1** (≤150 lines). Concept budget 0. **Scope:** `web/src/session.ts`, the reconnect effect, tests,
`web/dev/real-check.mjs`. Ports 6720–6729.

## Log

All times 2026-09-02, EDT, laptop.

- 19:31 — ACK. Worktree `t023-one-dial` at `fa45fe6` (main, with 022 and its follow-up). Scope:
  `session.ts`, the reconnect effect in `App.tsx`, tests, `real-check.mjs`; ≤150 lines; 0 concepts.
- 19:33 — **Premise corrected before editing: 022 broke Reconnect outright, not only in the race.**
  `App.tsx useRedial` (022) keyed its effect on `state.name`; its own `sessionUp` moves the machine
  `connecting → verifying`, which runs the effect's cleanup, sets `live = false`, and the `.then`
  then *closes* the verified transport instead of dispatching `verified` — every Reconnect that
  dials for itself hangs on "Opening a fresh connection…". The two-dials-one-identity hazard 022's
  report named is real too (two sessions under one key at the relay), but it was the second cause,
  not the first. Both are fixed by the same shape: the attempt's lifetime is its target.
- 19:34 — Design: `connecting`/`verifying` carry `redial?: Live` (a field, not a state); `redial`
  sets it; `sessionUp` carries it forward; `verified` from `connecting` is adopted when the
  candidate is for that host (`sameHost`: address + invite) — the self-probe's dial that Reconnect
  joined — and the card's own attempts, which carry no target, adopt nothing; `meError` from a
  redial goes back to the target, degraded (`afterFailure`), so the chat stays and the probe keeps
  asking; `redial` while one is in flight is a no-op. `App.tsx`: `dialAgain` keeps one in-flight
  dial per host address (a map; whoever asks joins), `dialFresh` bounds `/me` with `ME_TIMEOUT_MS`,
  the effect is keyed on the target (same reference across `connecting` and `verifying`), the
  "Opening a fresh connection…" screen derives from the state (`redial` present) — no `dialling`
  state, no `target` ref. The 022 test "closed after Reconnect" is rewritten to Disconnect: after
  Reconnect the candidate is the dial it joined.

## Report
