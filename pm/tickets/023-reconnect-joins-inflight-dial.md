---
id: 023
title: Reconnect joins the in-flight self-probe dial instead of starting a second one
kind: normal
size: 1
status: landed
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
- 22:15 — Evidence run 1 (race/stall/heal) after the rate-limit cut: the race **connected 5 s
  after the host came back** (the ticket's number), `stall`'s own Reconnect — the case that hung
  before — "and the thread works again", `heal` unchanged. But the race's Try again produced a row
  with no answer, twice. A diagnostic (race, then a *fresh* send, with network logging) showed the
  healed session answers /me and pings — and every chat request fails instantly, client-side, with
  an empty-message error, no request on the wire, persistent across sends. A real defect, not a flake.
- 22:22 — First theory (old session closed at `redial` while the same-identity probe session is
  mid-handshake poisons the relay) implemented in `dropped()` — keep the redial target open until
  adoption — and **refuted by the same diagnostic: identical failure**. Kept anyway: it is the timing
  a self-probe heal uses and the tests now pin it, but it is not what broke chat.
- 22:26 — Second theory: `redial → connecting` makes `live` null, App unmounts the chat behind a
  full-screen "Opening…" and remounts it at adoption; a self-probe heal never unmounts. Experiment:
  render the chat from the redial target during the attempt (same host + key ⇒ same React key ⇒ no
  remount) — **both fresh sends after the race delivered** (53 in · 2 out, 72 in · 2 out). That is
  the cause; the mechanism inside the bridge (why a React remount leaves a fresh wasm session
  unable to dial) is not chased here. Consequences: the "Opening a fresh connection…" screen is
  gone — Reconnect keeps the chat on screen, composer off, header saying "not answering", exactly
  like a heal; Chat's 30 s poll and the cooldown refresh are gated while reconnecting.
- 22:33 — Size: 163 source lines of change against 150. The three-item design the PM priced
  (join, target, timeout) fit; the mount-continuity fix the evidence forced is the +13. Declared,
  not trimmed further (the last pass cut comment text and moved the number by one).

- 22:40 — Evidence run 2 with the full fix: `stall` (Reconnect after the host is back — the case
  that hung) "and the thread works again"; `heal` unchanged, 31 s after the host's return. The
  official `race` timed out once: that run's host took 31 s to become healthy again (its own relay
  re-registration after a SIGKILL), the client's in-flight bridge dial has a 60 s bound, and the
  harness waited only 60 s past healthz. Not a deadlock: a diagnostic with a *longer* outage
  (host dead ~50 s, Reconnect pressed while dead) connected **10 s after the host came back** and
  both fresh sends delivered. Harness now waits 120 s so a slow heal prints its number and fails
  the ≤ 30 s check honestly instead of throwing; the check itself is unchanged.
- 22:55 — Final diagnosis (ruling: fix ≤ 30 lines or document). The mount-continuity result was a
  false positive: the diagnostics that passed had used a *fresh* host identity (a new data dir per
  run), where the host re-registers at the relay in 0.3 s; the harness reuses one identity across
  SIGKILLs — the production condition, `host.key.json` persists — and there the host takes ~31 s to
  come back at the relay. Re-run under the reused identity with the same source: Reconnect pressed
  while the host is dead → connected 5 s after it returned → every send "The connection to Max's
  laptop broke", client side, no request on the wire. `heal` (self-probe, no Reconnect) and `stall`
  (Reconnect after the host is back) chat fine under the same identity. So the limitation is exactly
  "Reconnect pressed while the host is still down, host restarting under the same identity": a
  same-identity session replacement inside the bridge/relay, not the reducer, not ≤ 30 lines.
  Documented with the repro; the mount-continuity change is kept (it is correct on its own terms —
  the chat never disappears behind a full-screen "Opening…" — and it is what the tests pin).

## Report

### Read this first: two causes fixed, one limitation documented (by ruling)

022's report blamed the Reconnect hang on two dials under one tunnel identity. Working the fix found
the real first cause, fixed both, and the evidence run found a third that this ticket cannot fix:

1. **022 broke Reconnect outright** — fixed. Its `useRedial` effect was keyed on `state.name`; its
   own `sessionUp` (connecting → verifying) ran the cleanup, `live` went false, and the `.then` shut
   the verified transport instead of dispatching `verified`. Every Reconnect that dialled for itself
   hung. The effect is now keyed on the redial target, which the reducer carries through `connecting`
   and `verifying`. **`stall` — Reconnect pressed after the host is back, the case that hung on main
   — now reconnects and chats.**
2. **Two dials, one identity** (the ticket) — fixed. `dialAgain` keeps one in-flight dial per host,
   a Reconnect during it joins that dial, `verified` from `connecting` is adopted for the host being
   redialled (`sameHost`); the card's own attempts adopt nothing. The redial's `/me` is bounded
   (10 s) and a failed redial resolves to `degraded`, never the connect screen or a permanent
   "Opening…".
3. **Known limitation — Reconnect pressed while the host is still down, host restarting under the
   same identity.** Exact repro on the real host: kill the host; send (fails at 15 s, Reconnect
   offered); press Reconnect 6 s later, host still dead; restart the host (same data dir, so the same
   `host.key.json`). Two outcomes, both seen: the host takes ~31 s to re-register at the relay and
   the client does not adopt a session within 120 s; or it re-registers at once, the client
   connects 5 s later — and every send fails client side ("The connection to Max's laptop broke",
   no request on the wire). Under a *fresh* host identity the same steps connect in 5–10 s and chat.
   `heal` (the self-probe, nothing pressed) and `stall` (Reconnect after the host is back) chat fine
   under the reused identity. The mechanism is a same-identity session replacement inside the
   bridge/relay while the host's own identity is re-registering — outside this ticket's scope and
   not a ≤ 30-line fix. Escape: the reader's next send fails once ("The connection … broke"),
   which degrades the session; the self-probe replaces it within ~10 s (measured: "not answering"
   at +5 s, "relayed via" at +10 s) and the send after that delivers ("66 tokens in · 2 out"). Or a
   reload. Nothing is stuck; one send is lost and said so.

### The core, shown working

Production bundle over the real New York relay against a real `bunny-network serve` on 6720,
keys minted with the host's own CLI (`dev/real-check.mjs`, one scenario per run, 22:15–22:45), plus
two diagnostics that add a fresh send after the race (job tmp, not the repo):

```
race     (promise 3, reused host identity) host killed · Reconnect offered at 15 s · pressed at 21 s, host
         still dead · host healthy again at 52 s (its own relay re-registration) · no session adopted in
         120 s — the limitation above, twice
race′    (diagnostic, fresh host identity) host dead ~50 s · Reconnect pressed while dead · host back
         +0 s "not answering · last 83 ms 51 s ago" · +10 s "relayed via New York · 35 ms", action
         "Try again" → connected 10 s after the host came back · fresh send "53 tokens in · 2 out" ·
         another "72 tokens in · 2 out" — the joined session chats. A ~21 s outage: connected +7.5 s, same.
race″    (diagnostic, reused host identity, harness timing) Reconnect pressed while dead · host back 0.3 s
         later · connected +5 s · send "The connection to Max's laptop broke", again 3 s later — the
         limitation, with the restart the other way round
stall    (014, the case that hung on main) host SIGKILLed mid-reply · ended on its own at 25 s ·
         Reconnect pressed 2 s after the host came back → "and the thread works again"
heal     (022, unchanged) host dead 40 s → healed by itself 31 s after it came back · Try again delivered
```


Reducer vitests (session.test.ts, +3 for 023; two 022 tests rewritten to the new timing): the
target carried through `connecting` and `verifying` with a second Reconnect a no-op and the old
session closed exactly once, at adoption; the probe's dial adopted straight from `connecting` for
the host being redialled, a stranger's or a card-attempt candidate closed instead; a dial that
fails or a /me that hangs resolving to the degraded session with the chat's payload intact and the
probe still armed, the card's own /me failure still ending on the connect screen. 232 tests.

### Edge awareness, one line

Handled: Reconnect before the first probe (dials for itself, through `verifying`); Reconnect during
the probe's dial (joins it, adopts from `connecting`); Reconnect after the probe healed (no button
to press); a second Reconnect while one is in flight (no-op, same screen); Disconnect mid-redial
(old and candidate both closed); a dial that opened and a /me that never answered (the new
transport closed, the old kept, degraded); the card's own attempts (no target, unchanged); the 30 s
poll and the cooldown refresh while reconnecting (gated — the session on screen is the one being
replaced); a stray probe candidate landing in a card attempt (closed).

### Verified (printed)

```
$ pnpm install --frozen-lockfile  → Done in 159ms using pnpm v11.13.0                    exit 0
$ pnpm typecheck                  → tsc --noEmit, no output                             exit 0
$ pnpm test                       → Test Files 10 passed (10) | Tests 232 passed (232)  exit 0
                                    0 failed, 0 skipped   (022 left 229; +3 here)
$ pnpm lint                       → eslint ., no output                                 exit 0
$ pnpm build                      → index 230 kB (gzip 74); Chat 358 kB (gzip 109); css 13.8 kB  exit 0
$ node dev/real-check.mjs stall   → "and the thread works again" — every promise held      exit 0
$ node dev/real-check.mjs heal    → healed by itself 31 s after the host's return           exit 0
$ node dev/real-check.mjs race    → reused host identity: no session adopted in 120 s (×2) — the
                                    limitation, printed, not hidden                        exit 1
$ diagnostics (job tmp)           → fresh identity: connected +10 s / +7.5 s, chats; reused identity:
                                    connected +5 s, sends fail, self-probe heals +10 s, then chats
```

`package.json` and `pnpm-lock.yaml` unchanged: **no new dependencies.** Processes: only the hosts,
previews and browsers the harness and my diagnostics started (6720–6725), all gone; the shared
llama-server only received requests; max-ws.lab untouched.

### Judgment calls

- **Reconnect no longer shows "Opening a fresh connection…".** The chat stays on screen with the
  composer off and the header saying the host is not answering — the same face as a self-probe
  heal — and flips to live values when the fresh session verifies. Made for cause 3 on a false
  positive (the passing diagnostics had a fresh host identity); kept because it is the better face
  — the reader's thread never disappears — and the tests pin it.
- **The old session is closed at adoption, never at `redial`.** Implemented for the relay theory,
  refuted as the cause of the chat failure, kept because it is the one timing every path (heal,
  Reconnect, join) now shares and the tests pin it. W1's "at most one live transport" holds for
  every path but the window between a dial starting and its adoption, as it already did for a heal.
- **A redial that fails resolves to `degraded`, not the connect screen.** The ticket's promise 2.
  014's "Still can't reach — reload" copy is gone; the header's "not answering" and the thread's
  Reconnect are the honest copy, and the self-probe keeps asking.
- **The harness's Try-again check after the race is a confirmation, not the promise**; it waits for
  a terminal row and reports what it found rather than sinking the run on a slow model.
- **Size: 163 source lines against 150** (+13), accepted by ruling. The priced design fit; the
  mount-continuity change is the overrun.
- The 022 follow-up's log entry is stamped 19:45; the commit is 19:30 — my estimate ran ahead of
  the clock. Noted, not rewritten (landed).

### Not verified

- **The bridge-side mechanism of the limitation** — why a session dialled while the host is
  re-registering its own identity answers /me once and then cannot dial. Measured four ways, not
  explained, not fixed here.
- A redial that fails **after** the old session was already closed by a Disconnect-then-Connect
  sequence inside the probe's 60 s dial (022's "smaller cousin"): the card's `openTransport` does
  not go through `dialAgain`. Unchanged, not driven.
- Firefox and Safari, as before.

### Candidates (not fixed here)

- `openTransport` under the same one-dial rule, keyed on the identity, for the card's own attempts.
- The probe's dial while the reader's own network is down: 60 s per attempt, reasoned only.

## Freeze

- **Base:** `fa45fe6` (public `main` at dispatch; rebased onto `origin/main` at the freeze).
  **Lane:** `t023-one-dial`, pushed, not merged.
- **Patch SHA-256:** of `git diff <base>...HEAD --binary` at the freeze commit, reported with the
  freeze message.

| Bucket | Budget | Measured (raw added / deleted) | Verdict |
|---|---|---|---|
| Source TS/TSX (`App.tsx`, `session.ts`, `ui/Chat.tsx`) | ≤150 lines of change | +87 / −76 = **163** | over by 13, accepted by ruling |
| Web tests (`session.test.ts`) | not budgeted | +72 / −4 | 232 tests (+3; two 022 tests rewritten to the adoption timing) |
| Dev harness (`dev/real-check.mjs`) | not budgeted | +38 / −1 | the `race` scenario; the wait prints the number |
| Screenshots | ≤500 KB each | 3 re-shot by the PM's WIP commit; none new | inside |
| Dependencies | none | 0 added, lockfile unchanged | — |

**Concepts: 0 budgeted, 0 used.** `connecting.redial` / `verifying.redial` are fields on states that
exist; `redialTarget`, `sameHost`, `dialFresh` are derivations of things that exist; the `reconnecting`
prop is a rendering of `redialTarget`. Rule against me on any of these.

## Ruling (PM, 2026-09-02 23:40)

**Landed** on main (`41d2565`); typecheck 0, 232 tests, lint 0, build OK, go build OK, printed. The
163/150 overrun accepted (evidence-forced mount continuity). Two causes fixed, including 022's own
Reconnect breakage. **Known limitation accepted for launch:** Reconnect pressed while the host is still
down, with the host restarting under the same identity, can fail to adopt a session (the host's relay
re-registration takes ~31 s); the escape is that the self-probe heals it, or a reload. Recorded in
`pm/LAUNCH.md` launch-day operations as a support-line answer.
