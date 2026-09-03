---
id: 033
title: Bound the relay handshake — a dead host resolves into the honest "didn't answer" state, never a spinner forever
kind: normal
size: 1
status: dispatched
updated: 2026-09-03
release: demo-1
---

# 033 — Connect never hangs

**Why.** The founder hit it (2026-09-03 01:30): the connect screen sat on "Connecting to the relay…"
indefinitely because the host behind the invite did not exist. The wasm bridge's `connect` retries the
meow handshake for up to 60 s and the app only shows the outcome after that; a stranger reads a spinner
with no end as "broken".

**Promises.**
1. The session machine bounds the handshake at ~20 s: if no `sessionUp` by then, the attempt is
   cancelled (close the bridge session) and the session lands in the existing failure state with the
   host-asleep copy ("<host> didn't answer. It's probably asleep or offline…", Try again). The progress
   line reads "Still connecting…" at ~8 s so the wait is visibly alive. Auto-connect from a link and a
   manual Connect behave the same.
2. Relay unreachable (the wasm cannot open its WebSocket at all) is distinguished from host-not-there:
   the bridge's log lines say which; map the first to "Can't reach the relay from this network" (with
   the relay name) and the second to the asleep copy.
3. Tests over the session reducer (fake clock: no `sessionUp` for 20 s → failure state with the right
   reason; `sessionUp` at 19 s → connected); one real-host screenshot with the host stopped.

**Size 1** (≤150 lines). Concept budget 0. **Scope:** `web/src/session.ts`, the connect effect, `Connect.tsx`
copy, tests, one screenshot. Note: lane 031 is editing `Message.tsx`/settings; lane 030 is editing
`index.html`/meta — stay out of those files.

## Log

## Report
