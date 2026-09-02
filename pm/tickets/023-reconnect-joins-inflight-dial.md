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

## Report
