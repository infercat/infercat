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

- 03:20 ACK. Lane `t033-handshake-bound` fast-forwarded to origin/main `ebc1146` (the ticket commit). Read BELIEFS,
  DESIGN §2.2, reports 007/014/022/023, session.ts, App.tsx, Connect.tsx, transport/*, wasm/main_js.go. The connect
  effect is `Connect.tsx: connect()` (the card's attempts); redials live in `App.tsx: useRedial` (023) and are out of scope.
- 03:24 Premise check for promise 2 before editing. `main_js.go: pingUntil` logs `handshake attempt N: <err>` where err
  is tailcat `Client.Ping`'s: `ensureStarted` (DERP-map fetch) or `magicsock.SendDERPPacketTo` (queues; a relay the
  browser cannot reach does not surface there) or the 5 s `context deadline exceeded`. Reading says a dead host and an
  unreachable relay may log the same line; running the real bridge under both to know (probe in job tmp, not the repo).
- 03:25 Own host built from this lane, started on `--dev-listen 127.0.0.1:6850` in `…/tmp/bn033-data`, key `t033`
  minted, host stopped (pid 90833 only; founder's host on 9091 untouched). The invite now points at a host that is not there.
- 03:31 Evidence (real bridge, headless chromium, `probe.mjs` in job tmp; 26 s per condition, `onLog` captured):
  dead host → `handshake attempt N: context deadline exceeded` every 5.2 s; relay host blocked (`*ipn.dev*`, CDP) →
  the same line, verbatim — only tailcat's verbose console shows `netcheck: probing tc301a.ipn.dev … Failed to fetch`,
  and that channel is `logf` → console, not `onLog` (`main_js.go:122-125`, read-only here); relay map blocked or
  offline → `handshake attempt N: fetching DERPMap for region 301: Get "https://tailcat.dev/derpmap.json": … Failed to
  fetch`, five a second. So the log lines tell "this network cannot reach the relay directory" from "no answer"; a relay
  whose own host is blocked while tailcat.dev answers is NOT distinguishable from a dead host with what the app can see.
  Promise 2 is built on the line that exists; the gap is declared in the report with the fix candidate.
- 03:36 Edits. `session.ts`: `connecting.slow`, event `slow`, `boundHandshake` (8 s mark, 20 s fail), `handshakeFailure`
  (the copy, from the last log line). `Connect.tsx`: the effect on `connecting` (shared by auto-connect and Connect),
  `onLog` kept (last 4 lines), a stale attempt closes what it opens instead of dispatching `sessionUp` into a newer
  attempt's `connecting`, pitch "Still connecting…", the old hedged 'connecting' copy replaced. `session.test.ts` +5.
- 03:37 First real-host run: "Still connecting to Dead host…" at 8.6 s, the asleep copy with Try again — but the failure
  showed at 50.2 s while Details held attempt 3 (15.5 s). Suspected a stalled main thread; a 250 ms heartbeat and a tee on
  the bridge's `onLog` (`diag.mjs`, founder sequence: real connect, host killed, link reopened) showed no gap and the
  failure at 20.4 s. The 50 s was the harness: Playwright's `textContent()` auto-waits 30 s for the steps node that
  disappears when the card fails. Harness fixed (non-waiting reads); screenshots re-taken from the trimmed source.
- 03:40 Accounting: 171 TS/TSX lines added was over the 150 budget — doc comments and test assertions trimmed, no
  behaviour changed; a second pass got the whole TS/TSX diff to 150 added (source +81 −13, tests +69 −1).
- 03:43 Freeze: origin/main still `ebc1146`; pinned command exit 0; screenshots re-taken from the final source at 03:41.

## Report

### The core, shown working

Production bundle over the real New York relay (`tc301a.ipn.dev`), a `bunny-network serve` built from this lane on
6850, connected once so the card knows the host by name (the founder's situation), then killed, and the invite link
opened again (`shot.mjs`, job tmp):

```
 0.5 s  Connecting to Dead host…            [Connecting to the relay]
 8.1 s  Still connecting to Dead host…      [Connecting to the relay]            33-real-still-connecting.png
20.3 s  Dead host didn’t answer             (19.7 s after "Connecting to the relay")
        It’s probably asleep or offline. Ask them to check that the host is running, then try again.
        Details: handshake attempt 3: context deadline exceeded · buttons: Try again · Forget this invite
                                                                                 33-real-host-stopped.png
66.0 s  the same failure — the bridge's own 60 s rejection of the superseded attempt changed nothing
        console errors 0 · page errors 0
```

A fresh browser with no earlier session says "The host didn’t answer" at 20.5 s; a 250 ms page heartbeat had no gap
over 1 s across the wait (`diag.mjs`) — the bound is the app's clock, not the bridge's. Reducer on a fake clock
(`session.test.ts`, +5): no `sessionUp` for 20 s → `slow` at 8 s, then `disconnected` with the asleep copy and the last
bridge line in Details, no `fatal`; `sessionUp` at 19 s → `verifying`, nothing fails at 20, `connected` on `verified`,
zero closes; Cancel at 5 s → nothing lands at 20; the log classifier both ways; `slow` only in `connecting`, once.

### Promise 2, as far as the evidence goes — read this

Measured with the real bridge in headless chromium (`probe.mjs`, `onLog` captured, 26 s per condition):

```
dead host                     handshake attempt N: context deadline exceeded                      every 5.2 s
relay host blocked (*ipn.dev) handshake attempt N: context deadline exceeded          — identical; only tailcat's
                              verbose console says "netcheck: probing tc301a.ipn.dev … Failed to fetch"
relay map blocked / offline   handshake attempt N: fetching DERPMap for region 301: Get
                              "https://tailcat.dev/derpmap.json": net/http: fetch() failed: …   five a second
```

The map line is what the app can see, and it now reads **"Can’t reach the relay from this network"**, naming what the
line carries (`tailcat.dev`, region 301), the raw line under Details. **A relay whose own host is blocked while the map
answers is not distinguishable from a dead host with what the app can see** and reads as asleep: `magicsock.
SendDERPPacketTo` queues the meow and reports it sent (`derp.go:781`), the WebSocket failure lives in tailcat's `logf`,
which `main_js.go:122-125` sends to the console under `verbose` and never to `onLog`. "With the relay name": the city
is in the DERP map, which is the thing that could not be fetched. Fix candidate, Go side (read-only here): route
tailcat's `Logf` lines matching `netcheck: probing` / `derphttp` into `onLog`, or have `connect` report the relay
hostname before the handshake — one more regex here after that. The relay's `/derp/probe` answers with
`access-control-allow-origin: *` (checked), so a JS-side reachability check is also open once the app knows the name.

### Edge awareness, one line

Handled: Cancel or the bound superseding an attempt whose bridge `connect` resolves later (closed in `connect()`,
never dispatched into a newer attempt's `connecting` — a latent race before: Cancel → Connect inside the bridge's 60 s
adopted the stale transport); the bridge rejecting before 20 s (its message joins the log, same classifier); direct
mode (the bound arms on `connecting` too); the wasm download outside the 20 s (armed after `wasmLoaded`); StrictMode's
double arm (cleanup clears); the offline log at five lines a second (last four kept); a nameless host ("The host didn’t
answer"); `slow` idempotent, and never on a redial's `connecting` (Chat renders that; the effect lives in Connect.tsx).

### Verified (printed, from base `ebc1146`)

```
$ pnpm install --frozen-lockfile  → Done in 1.2s using pnpm v11.13.0                     exit 0
$ pnpm typecheck                  → tsc --noEmit, no output                               exit 0
$ pnpm test                       → Test Files 10 passed (10) | Tests 244 passed (244)   exit 0
                                    0 failed, 0 skipped   (base 239; +5 here)
$ pnpm lint                       → eslint ., no output                                   exit 0
$ pnpm build                      → 505 modules; built in 763 ms                          exit 0
$ node shot.mjs        (job tmp)  → the run above; exit 0 = failure state reached, zero page problems
$ node diag.mjs founder (job tmp) → failure at 20.4 s, bridge lines at 5.1/10.3/15.5 s, no heartbeat gap
$ node probe.mjs       (job tmp)  → the four network conditions above
```

`pnpm-lock.yaml` unchanged: **no new dependencies.** Go and wasm untouched.

### Declared loudly

- **Two screenshots, not one.** `33-real-host-stopped.png` is the ticket's; `33-real-still-connecting.png` is the 8 s
  mark (35 KB). Drop it if one is the rule.
- **A behaviour change beyond the literal ticket:** a superseded attempt now closes what it opens in `connect()`
  instead of dispatching `sessionUp`. The bound needs it — Try again inside the bridge's 60 s would otherwise adopt the
  dead attempt's session — and it closes the same race under Cancel.
- The old `connecting` failure copy, which hedged "asleep or offline, or the relay could not be reached", is gone; each
  cause has its own copy now.
- The first real-host run showed the failure at 50 s. That was the harness (Playwright's `textContent()` auto-waits
  30 s for the steps node that disappears when the card fails), proven by the heartbeat run; fixed, re-taken. Logged.
- Production-touching: none. Own host only (`…/tmp/bn033-data`, 6850, started and stopped three times, my pid each
  time); the founder's host on 9091 was never touched.

### Judgment calls

- The bound is a `session.ts` helper (`boundHandshake`) enacted by one effect in `Connect.tsx` — 022's `scheduleProbe`
  shape — so the reducer stays pure and the fake-clock tests drive the real helper.
- "Still connecting…" is the pitch line, keeping the host's name ("Still connecting to Dead host…"); the step list is
  unchanged.
- `slow` is machine state, not local UI state: the surface renders the machine (BELIEFS).
- Timeout copy is not `host_asleep`'s ("your message is saved"): there is no message on the connect screen.

### Candidates, not fixed here

- The card's own `/me` at verify time has no bound (`Connect.tsx: getMe(transport, secret)`); the redial's has
  `ME_TIMEOUT_MS` (023). One line.
- A redial's dial (023) still rides the bridge's 60 s: "Opening a fresh connection…" can last that long on a dead host
  before it falls back to `degraded`.
- `main_js.go`: surface tailcat's relay-connect failures on `onLog` (above).

## Freeze

- **Base:** `ebc1146` (origin/main at dispatch and at the freeze — the ticket commit; nothing landed since).
  **Lane:** `t033-handshake-bound`, pushed, not merged.
- **Patch SHA-256:** of `git diff ebc1146...HEAD --binary` at the freeze commit, reported with the freeze message.

| Bucket | Budget | Measured (raw added / deleted) | Verdict |
|---|---|---|---|
| Source TS/TSX (`session.ts`, `ui/Connect.tsx`) | ≤150 lines of change | +81 / −13 = **94** | inside |
| Web tests (`session.test.ts`) | not budgeted (all TS/TSX added, incl. tests: 150) | +69 / −1 | 244 tests (+5) |
| Screenshots | — | 2 new: 59 KB, 35 KB | — |
| Dependencies | none | 0 added, lockfile unchanged | — |
| Go / wasm (`main_js.go`, read-only) | — | untouched | — |

**Concepts: 0 budgeted, 0 used.** `connecting.slow` is a field on a state that exists; `slow` is an internal event;
`boundHandshake` / `handshakeFailure` are helpers; no new CLI verb, flag, error code, config key or file. The two copy
strings replace one.
