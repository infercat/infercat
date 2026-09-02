---
id: 007
title: Web client hardening — truthful stream ends, session leaks, degraded states, host-scoped storage (from second-model review)
kind: normal
size: 5
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 007 — Web client hardening

## Binding

**Why.** A second-model review (gpt-5.6-sol, four lenses over main `5197966`) found the web client
lies in exactly the moments the launch audience will test: a stream that dies mid-answer looks
finished, a dead host keeps a healthy status pill, and a revoked key is swallowed. "Surfaces tell the
truth" is a taste rule in BELIEFS.md. Everything here is confirmed by reading the code; items marked
(×2) or (×3) were found independently by that many lenses.

**Promises (each with a vitest where the logic is testable; screenshots for visible states).**
1. **Truthful stream ends (×3).** `api.ts` stream parser: an SSE payload with an `error` member throws
   `GatewayError` with its code; EOF without `[DONE]` (and without a usage-only final chunk) rejects as
   a connection failure. The assistant message keeps its partial text and gets a terminal `status`:
   `complete | stopped | interrupted | no_answer`; the UI renders `interrupted` and `no_answer`
   visibly (short line under the message, not a banner) and excludes interrupted turns from the next
   request's context unless the user continues them.
2. **Reasoning-only end state.** When a reply ends with only `reasoning_content` (Gemma with a small
   cap does this) or Stop is pressed during thinking, the Thinking block collapses with a one-line
   explanation and the message gets `no_answer`/`stopped`; never an empty assistant row.
3. **No leaked sessions (×3).** `Connect.tsx`: the provisional transport is closed on `/me` failure,
   on unmount, and when a newer connect attempt supersedes it (attempt id guard). Test: `/me` rejects →
   `close()` called exactly once.
4. **Degraded states (×3).** Ping failure replaces the path pill with `path unknown · last 32 ms 2 min
   ago` (or similar) rather than the stale value; `/me` with `upstream.healthy=false` shows the engine
   offline state in the header; a periodic `/me` (every 60 s) while models are empty (LM Studio with
   nothing loaded) so a model loaded later appears without reconnecting.
5. **Revoked mid-session (×2).** `refreshMe` errors with `invalid_key|key_paused|key_revoked` return
   the user to Connect with the mapped reason; transient refresh errors keep the last snapshot.
6. **Abort during dial (×2).** `transport/tunnel/index.ts`: an `AbortSignal` already aborted or aborted
   while `dial()` is pending rejects immediately with `AbortError`; a connection that resolves after
   abort is closed.
7. **Host-scoped storage (×3).** Conversations and settings are namespaced by a non-secret identity
   `tunnelAddr + key.id`; connecting to a different host starts with that host's own (empty) list and
   its own settings; a persisted model not in the new host's list is reset.
8. **Retry countdown** uses an absolute deadline and `Date.now()`, re-derived on `visibilitychange`;
   values over 10 minutes render as a duration; missing `Retry-After` defaults to 5 s.
9. **IME safety.** Enter during composition (`isComposing` or keyCode 229) never sends, in both the
   composer and the invite field.
10. **Two tabs.** Use the Web Locks API: the tab holding the lock uses the persisted tunnel identity;
    other tabs connect with an ephemeral identity without overwriting the stored key. (Cheap variant;
    full cross-tab merge is backlog.)
11. **Small truths.** Per-code copy is shown (not replaced by the gateway's diagnostic; show both);
    add `invalid_request` and `not_found` entries; SSE framing handles CRLF split across reads;
    `Content-Encoding` compared case-insensitively in `wasm.ts`; wasm asset fetches have a 60 s timeout
    and a working Retry; the remembered-invite failure copy mentions a rotated invite and offers
    "Forget this invite".
12. **Privacy copy is conditional.** `/me.host.log_prompts` (added by ticket 006 promise 11) drives the
    Connect screen line: when true, an unmistakable disclosure replaces the "never what you wrote"
    sentence, before the first message.
13. **Evidence.** `pnpm typecheck && pnpm test && pnpm build` printed; screenshots of the interrupted,
    no-answer, degraded-path, engine-offline, and log-prompts-disclosure states via the existing
    Playwright runner + fake gateway (extend `web/dev/fake-gateway.ts` with an `error-mid-stream` and
    `eof-no-done` mode and a `log_prompts` flag).

14. **Invite from a link (PM, stranger UX pass).** Opening `<app>/#bn1.…` lands on the connect
    screen with the invite pre-filled and connects on one click — never auto-connecting — and the
    fragment is stripped from the address bar as it is read, so the secret is not left in history or
    in a shared screenshot. The host CLI will print links in that form.
15. **One vocabulary (PM, stranger UX pass).** The paused/revoked screens say it once, in the
    friend's words: paused → "Your invite is paused — ask your host to resume it."; revoked → "This
    invite was revoked — ask your host for a new one." And when `/me.host.name` is empty, the
    "You're on <name>." sentence is omitted rather than rendered as "You're on .".

**Size 5** (≤2000 TS/TSX source lines of change; expect ~600). Concept budget 2: message terminal
status, host-scoped storage namespace. **Normal**; experience review follows.

**Scope contract.** `web/**` except `web/wasm/**`. Ticket 005 concurrently makes one web change (10e:
do not send `max_tokens` unless set) — expect a trivial rebase conflict in the request builder.

**Backlog (declined for demo-1):** storage quota handling and cross-tab merge · 103 informational
responses · strict hex chunk-size and Content-Length grammar · refresh-during-generation checkpointing
(persist the user turn before I/O is in scope if it falls out of promise 1 cheaply; otherwise backlog).

## Background

- Review output: `/Users/yuanpingsong/.claude/jobs/12b4a99c/tmp/review-integrated.out` (JSON; four
  lenses, file:line anchors, suggested fixes). Reviewer notes are hypotheses; verify against the code.
- `web/dev/fake-bunny-tunnel.ts`, `fake-gateway.ts`, and the Playwright runner exist from 004.

## Ruling (design) — PM, 2026-09-02 11:2x (founder: fix classes, not instances)

Implement promises 1–6 and 10 through two explicit state machines, not per-site fixes:

(a) **Session reducer** (one file, e.g. `web/src/session.ts`): states `idle → loadingWasm →
connecting → verifying → connected → degraded(reason) → disconnected(reason)`. The reducer OWNS the
Transport instance and closes it on every exit transition — that is how "no leaked sessions" becomes
structural rather than a try/finally at one site. Events: `wasmLoaded, sessionUp, meOk(me),
meError(code), pingOk(rtt, via), pingFail, streamError(code), abort, revoked`. The header/status pill
renders the state; `degraded` carries the last good measurement and its age, so nothing cached can go
stale; `/me` errors route through the same reducer (promise 5).

(b) **Message lifecycle**: the SSE parser becomes an event source (`reasoningDelta, contentDelta,
usage, error(code), done, eof`) and a reducer assigns status `streaming → complete | stopped |
interrupted | no_answer`; the UI renders status (promises 1, 2). Abort-during-dial (6) is an event
into the same machines.

(c) **Storage namespacing** (7) is one key function used by the store.

Delete the ad hoc flags and effects these replace; keep the vitest coverage on the reducers (they are
pure and easy to test). The 13 promises remain the acceptance criteria.

## Log

All times 2026-09-02, EDT, laptop.

- 11:02 — ACK. Worktree `t007-web-hardening` at `8a3a3c4`; scope `web/**` except `web/wasm/**`.
  Read BELIEFS, ARCHITECTURE, 007, 004 (Log + Report + Contest + Ruling), then all of `web/src`
  and `web/dev`. pnpm 11.13.0, Node 22.23.2, `pnpm install --frozen-lockfile` green in 1.3 s
  (004's `pnpm-workspace.yaml` `allowBuilds: {esbuild: true}` note still holds; nothing to change).
- 11:06 — Verified every promise's premise against the code before editing. All thirteen reproduce.
  Two corrections to the ticket's file references (reviewer hypotheses, not defects):
  `transport/tunnel/index.ts` is `web/src/transport/index.ts`, and the `/me` handler the fakes
  share lives in `web/dev/fake-backend.ts` (`fake-gateway.ts` is the Node adapter over it), so the
  `log_prompts` flag and the new stream modes go in the shared backend and are switched from
  `fake-gateway.ts` (env) and the in-page fake (query param).
  Note on promise 12: 006 as dispatched has nine promises and none of them is `log_prompts`, but
  `docs/ARCHITECTURE.md` §Gateway HTTP API already carries `host.log_prompts` as contract. Coding
  against ARCHITECTURE per dispatch instruction: a missing field is `false`.

- 11:18 — Rebased onto `origin/main` `aa68524` (005 landed) at the PM's request. 005's only web
  change is a fixture in `web/src/api.test.ts` (no `max_tokens`/`max_completion_tokens` in the body);
  my edits do not touch the request-body line, so the rebase was clean with no conflict. It also adds
  `web/dev/int-check.mjs` and `int-*` screenshots — a second browser runner I leave alone.
- 11:24 — Design ruling received and recorded above. Restructuring: the per-site fixes written so far
  (api.ts stream ends, storage scope) survive as the leaves of the two machines; `Connect.tsx`,
  `Chat.tsx` and `App.tsx` are rewired to render from the reducers instead of from local flags.
  Two readings I am taking, both to keep the reducers pure and therefore testable, as the ruling asks:
  (i) the session reducer is a pure function whose states *carry* the Transport; a single effect in
  `App.tsx` closes any transport a transition dropped. Ownership is still structural and in one
  place — the reducer decides, one effect enacts — but `reduce()` itself performs no I/O.
  (ii) `degraded` carries the same live payload as `connected` (transport, secret, me, path, age) plus
  its reason, so the chat screen renders from either without a second code path; degradation is a
  state, not a flag on connected.

- 11:31 — Two machines written (`web/src/session.ts`, `web/src/stream.ts`) and everything rewired
  onto them. `ConnectStage` and `ChatDelta` deleted: the connect screen's four stage names are now
  the session states, and `streamChat(onDelta)` is now `chatEvents()`, an async generator whose last
  event is always `done`/`eof`/`aborted`/`error`. `Chat.tsx` lost its `me`, `path`, `error` and
  countdown flags; `App.tsx` gained `useSession`, the one place a Transport is closed.
- 11:44 — Rebased onto `origin/main` `5456e59` (006 landed). 006 touched no web file and the /me
  contract is unchanged; `internal/gateway/proxy.go:317` now really emits `log_prompts`, matching
  what the client already reads. Nothing to change.
- 11:52 — Screenshots: found a real layout defect while shooting the engine-offline state. `.main`
  was `grid-template-rows: auto 1fr auto auto`, so adding a header line pushed the `1fr` onto the
  wrong child and squashed the thread. Now flex with `min-height: 0` on the scroller — a header that
  can grow a truthful line no longer costs the thread its height.
- 12:03 — Promises 14 and 15 received and implemented. While shooting the revoked screen, found that
  the dev-only `?autoconnect` re-fired every time the connect screen remounted, so a revoked invite
  reconnected itself in a loop and its failure screen was unreachable. The flag is now once per page
  load (module scope, not a component ref). Dev-only, but it hid a real screen from testing.
- 12:10 — Rebased onto `origin/main` `fe9a139` (008's ruling and tickets 009–013 had landed).
  013 is the web follow-on for persistence/reload, which is this ticket's declined backlog — no
  overlap. Full check re-run from that base and printed in the Report.

## Report

### The core, shown working

Three things had to become true. Each is shown as the state a stranger would see, and each is a
transition in a reducer rather than a fix at a call site — the design ruling's requirement.

**1. A reply that stops is never drawn as a reply that finished.** `chatEvents()` ends every stream
with one of four events, and `reduceReply` turns them into a terminal status the bubble renders:

| what happened | status | what the reader sees |
|---|---|---|
| `[DONE]` or the usage-bearing final chunk | `complete` | the answer, nothing added |
| stream stopped without either | `interrupted` | partial text + "The connection dropped before the host finished this reply — what is above is only part of it." (`07-interrupted.png`) |
| `error` member inside a flushed 200 | `interrupted` | partial text + our copy **and** the host's sentence (`07-mid-stream-error.png`) |
| finished with only thinking in it | `no_answer` | the Thinking block collapsed with "The model used its whole reply thinking and never got to an answer." (`07-no-answer.png`) |
| Stop pressed | `stopped` | "You stopped this reply." / "…while it was still thinking." |

Interrupted and no-answer turns are also excluded from the next request's context, and say so in the
meta line ("· not sent as context").

**2. A session is never left open, because no call site closes one.** `reduce()` decides; `dropped()`
names the transports a transition orphaned; `useSession` in `App.tsx` is the only code that calls
`close()`. `session.test.ts` drives the sequences that used to leak — /me failing during verify, a
connect attempt landing after a newer one superseded it, a `verified` payload arriving for a session
already left, the reader disconnecting — and asserts `closes === 1` each time.

**3. Nothing on screen outlives its measurement.** A failed ping moves the session to
`degraded('path')`, and the pill stops presenting the old number as current: `path unknown · last
84 ms 30 s ago` (`07-degraded-path.png`, taken after a real 30 s measurement tick). An unhealthy
engine is `degraded('engine')` and says so under the header (`07-engine-offline.png`). A `/me`
refresh that returns `invalid_key|key_paused|key_revoked` ends the session with the mapped reason
(`07-revoked-return.png`); any other refresh error keeps the last snapshot.

Edge cases handled, one line: CRLF split across two reads (it used to make `[DONE]` arrive as
`[DONE]\r`, so every reply looked truncated), a usage-only final chunk with no `[DONE]`, an abort
before the dial and during it, a conn that lands after its abort, `Content-Encoding: GZIP` in the
wrong case, a wasm asset host that accepts the connection and then says nothing (60 s), a persisted
model the new host does not have, a host with no name, a fragment that is not an invite, a malformed
percent escape, Enter during IME composition in both text fields, and a second tab that must not
take over the stored tunnel identity.

### Verified (printed, from base `fe9a139`)

```
$ pnpm install --frozen-lockfile  → Already up to date. Done in 156ms                     exit 0
$ pnpm typecheck                  → tsc --noEmit, no output                               exit 0
$ pnpm test                       → Test Files 8 passed (8) | Tests 132 passed (132)      exit 0
                                    0 failed, 0 skipped   (004+005 left 78; +54 here)
$ pnpm lint                       → eslint ., no output                                   exit 0
$ pnpm build                      → 504 modules; index 218.81 kB (gzip 70.19)
                                    Chat 351.20 kB (gzip 107.02); css 11.29 kB            exit 0
$ pnpm screenshots                → 41 screenshots; no console errors, no page errors,
                                    no horizontal overflow at 360 px, no off-origin
                                    request from the production bundle                    exit 0
```

`pnpm-lock.yaml` unchanged: **no new dependencies**.

### Screenshots — `web/dev/screenshots/07-*.png` (the 004 set is unchanged and re-shot green)

| | |
|---|---|
| `07-interrupted.png` | a stream that died mid-answer: partial text kept, cut-off line, not sent as context |
| `07-no-answer.png` | thinking only, collapsed with its explanation and its token counts |
| `07-mid-stream-error.png` | our copy and the host's diagnostic, both |
| `07-engine-offline.png` | `upstream.healthy=false` in the header, and an empty state that no longer says the model "is listening" |
| `07-degraded-path.png` | `path unknown · last 84 ms 30 s ago`, after a real ping tick failed |
| `07-log-prompts-disclosure.png` | the disclosure a `--log-prompts` host forces, before the first message |
| `07-log-prompts-chat.png` | and the line that stays up for the rest of the session |
| `07-revoked-return.png` | revoked mid-chat: back to Connect, one vocabulary, with the rotated-invite hint |
| `07-invite-link.png` | `<app>/#bn1.…`: field filled, address bar already wiped, one click to connect |

### Judgment calls

- **The reducer is pure; one effect enacts it.** The ruling says the reducer owns the Transport and
  closes it on every exit. `reduce()` performs no I/O — it returns state, and `dropped(prev, event,
  next)` returns what to close, which `useSession` does in one place. Ownership is still structural
  and singular; purity is what makes `session.test.ts` possible, which the ruling also asks for.
- **`degraded` carries the same payload as `connected`.** The chat screen renders from either, so a
  degraded host is still a usable one and there is no second code path to keep in step.
- **"Unless the user continues them" (promise 1) is Regenerate.** Interrupted and no-answer turns are
  dropped from context automatically and say so; the reader continues one by regenerating it. I did
  not add a "use it anyway" toggle — it is a second way to say the same thing, and the launch is days
  away. Flag it if you want the toggle.
- **`stopped` turns stay in context.** The reader chose to stop; the text is real and every other
  chat app keeps it. Only turns the *host* cut short are dropped.
- **The log-prompts disclosure is a gate, not a line.** BELIEFS says `--log-prompts` "says so
  loudly", and the connect screen has just promised the opposite in its own copy, so the correction
  interrupts once, before the first message, and then persists as a header line. It is one extra
  click on hosts that log — never on a normal host.
- **Direct mode shares the storage namespace** with tunnel mode for the same invite: the scope is
  `tunnelAddr + key.id`, and both modes have both.
- **Pre-scope history is dropped, not migrated.** Handing one host's old conversations to whichever
  host you connect to next is the exact bug promise 7 fixes; compatibility is off until first
  release (BELIEFS).

### Bought beyond the ticket (declare loudly)

- **A layout fix in `styles.css`** (`.main` grid → flex): the header could not grow a line without
  squashing the thread. Found while shooting `07-engine-offline`; without it, promises 4 and 12 are
  unshippable.
- **`?autoconnect` is now once per page load.** Dev-only, ~2 lines; it was hiding every
  fatal-disconnect screen from browser testing.
- **`07-revoked-return.png` and `07-invite-link.png`** — two screenshots beyond the five the ticket
  names, for the two promises that arrived mid-flight.
- **One eslint global** (`location`, for browser code inside `page.evaluate`).

### Not verified

- **Against the real gateway.** 006 landed `log_prompts` (`internal/gateway/proxy.go:317`) but this
  slice was exercised against the fakes only; the integration runner (`web/dev/int-check.mjs`, 005)
  has not been re-run against a live `serve`. The disclosure gate in particular has never seen a real
  `--log-prompts` host.
- **Web Locks with two real tabs.** Tested against a stubbed `navigator.locks` (both branches) and
  the no-Web-Locks fallback; not two live tabs in Chrome, and not Safari.
- **Reload during generation.** Explicitly 013's, per that ticket.
- **iOS Safari and Firefox.** Chromium only, as in 004.

### For the PM to re-price

1. **The design ruling moved the size.** The ticket estimated ~600 TS/TSX lines; the measured change
   is 1239 raw (951 added, 288 deleted) — inside the 2000 ceiling, but double the estimate. The two
   machines are ~230 lines of the total; the rest is the rewiring they forced through `Chat.tsx`,
   `Connect.tsx` and `App.tsx`. Worth knowing before the next "fix the class" ruling is priced.
2. **Concepts: 2 budgeted, 2 more introduced by the ruling, each replacing one it deleted.** Message
   terminal status and the host-scoped namespace were budgeted. `SessionState`/`SessionEvent`
   replaced `ConnectStage`; `StreamEvent`/`reduceReply` replaced `ChatDelta`. Net count is flat, but
   they are new named concepts and I am not going to call that free.
3. **`/me` after every request, still.** Unchanged from 004's note, and now also every 60 s while the
   host has no model loaded. If the real `/me` is expensive, this is where it shows.
4. **The 30 s ping and the 60 s `/me` are hard-coded.** If the launch wants them tuned, they are two
   constants in `Chat.tsx`, not a config concept.

