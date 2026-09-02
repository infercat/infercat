---
id: 020
title: Web truth, round 2 — per-turn state, stream idle end, reply-end classification, one health source, tab leader (second experience pass)
kind: normal
size: 3
status: landed
updated: 2026-09-02
release: demo-1
---

# 020 — Web truth, round 2

## Binding

**Why.** Second experience pass (both personas, real stack, after 014): "not launch-ready this week;
close". The happy path is "genuinely uncommon" and both friends verified the meters against the host.
The failure paths say four things that are not true, two of them self-refuting inside one screenshot,
and 014 introduced two regressions. Every item below is per-turn state or stream lifecycle; fix the
class through the machines (session reducer, message lifecycle), never per site.

**Promises.**
1. **Pending is per turn (blocker; 014 regression).** `Chat.tsx` repaints every user bubble as
   `pending` on each send and never clears it in the paused/failed branches, then persists it. Only the
   turn being sent carries `pending`; paused/failed/delivered all clear it; nothing persisted says
   "Not delivered" under an answered message. Test: three answered turns, host pauses, fourth send
   fails → exactly one bubble marked, and it clears on resume.
2. **A stream that stops ends (blocker).** A fourth silence joins 018's three: tokens were flowing and
   then none for 15 s while `/me` is unhealthy or unreachable → the reply ends `interrupted` with
   "<host> stopped answering mid-reply" and Try again; the composer returns; meters blank as on pause.
   Never "You stopped this reply" unless the user pressed Stop. Test over the stream event source with
   a fake clock.
3. **Reply-end classification is one function.** Given `finish_reason`, `usage`, `max_output_tokens`,
   and `model_context`: `out < max_output_tokens && in + out ≥ model_context − slack` → "This chat has
   filled <host>'s <N>k memory — start a new chat to keep going" with a New chat button and no
   Continue; the reply-cap copy only when `out ≥ max_output_tokens`. Add a context meter to the header
   (`in + out` of `model_context` for the current chat) so the one limit that ends conversations has a
   meter. Test the boundary rows.
4. **One health source.** Banners about the engine derive only from `/me.host.upstream.healthy`
   (poll every 30 s, immediately when a cooldown reaches zero, and after every failed request); a
   failed request never asserts "llama.cpp is not answering". When the thread offers Reconnect, no
   second cause is shown. Meters and path refresh on that same poll; a stale header is impossible by
   construction. Test: request fails while `/me` says healthy → no engine banner.
5. **Revoke stays in the thread.** Like pause: banner "This invite was revoked — ask <host> for a new
   code", composer disabled, the half-written answer readable, saved chats kept; the primary action is
   "Paste a new code" (a fresh connect card), never an enabled Connect for the revoked code, and never
   "Invite from your link is ready" above a revoked message. Only `invalid_key` on first connect uses
   the gate.
6. **Tab leader for the store (014 promise 4, incomplete).** The conversation store takes the same Web
   Lock election the tunnel identity uses: the leader tab writes; a follower tab reads live via the
   `storage` channel and never renders or writes a stream it did not start; a banner in the follower
   says "This chat is open in another tab" with "Use this tab instead". Usage/meters broadcast over the
   same channel so two tabs never disagree.
7. **Phone drawer parity.** Deleting from the drawer on touch uses the same undo toast as desktop; no
   instant delete anywhere. Thinking block on expand after completion: no height cap (or scroll to top
   with a visible bottom fade and forced scrollbar).
8. **Connect flow.** A stored invite or a fragment invite auto-connects, showing "Connecting to <host>…"
   with Cancel, falling back to the card only on failure (ruling: a link click or a return visit is the
   consent; the secret is never shown unmasked — mask by default with Show).
9. **Nits, all of them.** "Your 1 chat … are" plural; "<host>'s computer" reads as a template when the
   host name is already possessive — say "the computer named <host>" or use the name once; Copy on
   every assistant message; Stop mid-thinking still prints token counts (they were charged); model
   display name in message footers; footers do not wrap into the button row at 390 px.
10. **Evidence.** vitests for 1–6 over the reducers; Playwright screenshots on the real host of: the
    paused thread with exactly one marked turn, the mid-reply host death ending on its own, the context
    wall copy, the revoke banner in-thread, the follower-tab banner, the phone drawer undo. `pnpm
    typecheck && pnpm test && pnpm lint && pnpm build` printed; `web/dev/real-check.mjs` extended.

**Size 3** (≤900 TS/TSX source lines of change). Concept budget 1: store leader. **Normal**; the third
experience pass is the launch gate.

**Scope contract.** `web/**` except `web/wasm/**`. No Go changes; if a promise needs the gateway,
contest with the symbol.

**Keep (both personas, 23 items):** newline-tolerant paste; sub-second connect; meters that match the
footers; the pause round-trip that heals itself; all the edge copy; the thinking block while streaming.

## Background

- Reports: workflow `wf_52458c2e-1f5` journal (both personas, screenshots under
  `/Users/yuanpingsong/.claude/jobs/12b4a99c/tmp/ux2-{desktop,phone}-shots/`). Reviewer suggestions
  cite file:line; verify against the code.
- Ports for local runs: 6670–6679. Shared llama-server 127.0.0.1:18080, requests only.

## Log

All times 2026-09-02, EDT, laptop.

- 17:07 — ACK. Worktree `t020-web-truth-2` at `414a830` (public main); scope `web/**` except
  `web/wasm/**`, no Go. Read BELIEFS, DESIGN §2, 020, the 014/018/007 reports, all of `web/src`
  and `web/dev`, and the reviewer journal (`wf_52458c2e-1f5`). pnpm 11.13.0, Node 22.23.2, Go
  1.27.0; `make build`, `make wasm`, `pnpm install --frozen-lockfile` green. Every premise
  reproduces in the code: `Chat.tsx:237` repaints every user turn pending and the paused branch
  never clears it; the `/me` poll (`Chat.tsx:156-160`) runs only once something is already wrong,
  so a healthy-looking header is stale by construction; `session.ts:117-124` asserts engine health
  from a failed request; the phone's undo toast exists and sits under the drawer's backdrop
  (`07b-after-delete-tap.png`). No contest.

- 17:2x — Reducers first, in the order the ticket names. Three readings taken, each to keep one
  model rather than two, and each flagged for the ruling:
  (i) **Promise 1 is a per-turn rule in the store, not a render.** `send()` marks exactly the turn it
  sends; `settlePending()` (storage.ts) clears a mark whose next message is a delivered reply — run
  after every stream ends and on every load, so a transcript 014 marked repairs itself the moment it
  is read. The pause branch's rollback-to-composer (014) is gone: a paused send now stays in the
  thread as the one marked turn with the banner and a disabled composer, exactly the shape the
  ticket's test describes ("exactly one bubble marked, and it clears on resume") and the same shape
  as every other undelivered turn — one model, not two. Rule against me if you want the rollback back.
  (ii) **Promise 4 also moves "Reconnect" into the machine.** Chat's `broken` flag is deleted; a
  request that got no answer (`host_asleep`, a broken transport) makes the session's snapshot
  history (`meOk: false`) and the thread offers Reconnect exactly while `!live.meOk` — the next /me
  that gets through changes the word to Try again. `settle()` only calls the engine unhealthy on a
  /me that got through, so "llama.cpp is not answering" can never sit above a Reconnect.
  (iii) **Promise 5 generalises `Live.paused` to `Live.key`** (`active | paused | revoked | invalid`),
  so revoke is the same `degraded('key')` as pause with different copy and one different action.
  `FriendlyError` gains `code` so the reducer can tell the two dead codes apart; the never-dispatched
  `revoked` session event is deleted. On the connect screen a fatal failure disables Connect (the
  reviewer pressed it and reproduced the error) and hides "Invite from your link is ready".
- 17:2x — Promise 2: `chatEvents` takes a `probe` (Chat passes `refreshMe`, so the /me it asks is
  also dispatched into the session — one health source even mid-reply). The idle clock arms at the
  response head and re-arms on every event including `: queued`; on 15 s of silence the probe is
  asked, and only a host that is unreachable or unhealthy ends the reply (`host_stalled`); a healthy
  host is a slow one and the clock re-arms. `aborted` events carry a `why` when the app, not the
  reader, aborted (promise 6's take-over) — "You stopped this reply" is now reserved for Stop.
  Promise 3: `replyEnding()` in stream.ts; the cap wins when both walls are true (Continue still
  helps); slack is `max(8, ctx/64)`; the ending copy avoids the possessive ("the 4.1k memory on
  Max's laptop") for the same reason as promise 9's nit. Promise 6: `electStore()` in storage.ts
  mirrors `claimTunnelIdentity` with two more moves — a follower queues (promoted when the leader's
  tab closes) and "Use this tab instead" steals; a leader that loses the lock mid-reply aborts with
  the reason; `loadChats` no longer marks a streaming reply interrupted (another tab may be writing
  it) — `reopenChats` does, and only the leader calls it; /me is shared under `bn.me.<scope>`.
- 17:3x — Rebased onto `origin/main` `002e67a` (019 landed; keys default 4096) at the PM's request:
  Go only, no conflict. Founder ruling received: promise 3 is the classification only. Typecheck,
  lint, 218 vitests (190 → 218), build green; the two harnesses updated for auto-connect (promise 8:
  nothing in a harness clicks Connect for a code in the URL any more) and extended with the six
  real-host scenarios the ticket names; both running.

- 17:5x — Real host, run by `dev/real-check.mjs` (now mints its own keys so it can pause, resume and
  revoke): every scenario held on the second or third pass. What the first passes taught, all harness,
  none product: three "pong" turns finish in ~150 ms each, so a `keys pause` issued right after turn 3
  raced that turn's own /me and the app learned of the pause from the poll — composer off, nothing to
  mark — which is a true state but not the reviewer's; the pause now falls between polls. A reload
  while paused lands on the connect card by 014's design (no chat to keep at verify time), so the
  reload check runs after the resume. Two product findings from the same passes, both fixed: on an
  emulated phone `position: fixed; bottom: 0` sits in the 39 px band behind the collapsing URL bar
  (measured: innerHeight 883 vs a visible 844), so the undo toast is in flow and stacked above the
  drawer instead; and "Paste a new code" re-seeded the card from the module-level link/dev prefill —
  the card now takes what is remembered, and the link seeds only the first mount. The fake tunnel
  answered /me while "asleep", which the real host never does (014 measured it), so its sleep is now
  per session; with that the fake's host-asleep shot shows what the real one shows: Reconnect and
  blank meters.
- 18:0x — After the PM's landing (`447d1c9`, from this working tree): the final fake run came back
  clean (75 screenshots, no console or page errors) once the Connect prefill precedence was fixed
  (first mount: link, dev query, remembered; later cards: remembered only — the `offline` page had
  been seeded with the remembered demo code). That fix and the re-shot screenshots are the one
  follow-up commit on the lane; accounting above recomputed against `5bb43bc`.

## Report

### The core, shown working

Every promise is a rule in a reducer or one derivation, and the UI renders it. The real stack —
the production bundle over the real New York relay against a real `bunny-network serve` this
harness starts, kills and restarts, keys minted and paused/revoked through the host's own CLI —
printed, `dev/real-check.mjs` scenario by scenario:

```
paused   three answered turns · keys pause · fourth send
         banner "Max's laptop paused your invite. Your message is still here — try again once they resume."
         bubbles marked "Not delivered": 1 · composer disabled · no Try again while paused
         keys resume → banner cleared by itself 25 s later (the 30 s poll) · reload → still exactly 1 marked
         Try again → delivered · marks left: 0                                  20-real-paused-one-mark.png
stall    host SIGKILLed mid-reply → ended on its own at 25 s (15 s idle + the 10 s /me bound over the dead tunnel)
         "Max's laptop stopped answering mid-reply. What arrived is above. Try again — if it keeps happening,
          their machine may have gone to sleep." · composer back: Send · meters "— / 20 per minute · — / 200k
         tokens today · — / 4.1k context" · action Reconnect · engine banner: none · reconnected in 0.2 s
                                                                                20-real-stalled.png
wall     5000-word essay: "This chat has filled the 4.1k memory on Max's laptop — start a new chat to keep going."
         + New chat, no Continue · footer "Gemma 4 E2B · 51 tokens in · 4045 out" · 51 + 4045 = 4096
         header "4.1k/4.1k context"                                             20-real-context-wall.png
asleep   host killed before Send: "Still waiting…" at 5 s · failed at 15 s · pending kept · Reconnect
         engine banner: none (one health source) · path "not answering · last 72 ms 18 s ago" · meters "—"
         reconnected in 0.3 s and the thread works again                       14-real-asleep-failed.png
tabs     tab B on the same invite: "This chat is open in another tab. Use this tab instead" · B's composer
         disabled · A shows no banner · A sends, B sees it over the storage channel · meters A == B
         ("17 messages left this minute · 6.3k/200k tokens today · 45/4.1k context") · after Use this tab
         instead: A follows, B leads                              20-real-follower-tab.png · 20-real-tab-taken-over.png
phone    390×844 touch: delete from the open drawer → "Chat deleted · Undo" at y=725 (on screen, drawer still
         open) · Undo → chat back                                               20-real-phone-drawer-undo.png
revoke   keys revoke mid-chat: banner "This invite was revoked — ask Max's laptop for a new code." + Paste a new
         code · still in the thread (the 1 411-char answer readable) · composer disabled · Paste a new code →
         a fresh card, field empty, no "Invite from your link is ready", no Welcome back
                                                      20-real-revoked-in-thread.png · 20-real-revoked-new-code.png
cost     (014 promise 5, still) CONNECT → /me only · SEND rpm_used 0 → 1 → 2 · the third meter reads
         "273/4.1k context" after the first reply
```

How each is a class fix, in one line each:

1. **Per-turn pending** — `send()` marks the turn it sends; `settlePending()` (storage.ts) is the
   one rule that clears a mark: its next message is a delivered reply. Run after every stream and
   on every load, so a transcript 014 marked repairs itself when read. The pause branch's
   composer rollback is gone: an undelivered turn has one shape, whatever failed.
2. **The fourth silence** — `chatEvents` arms an idle clock at the response head, re-armed by
   every token and `: queued`; 15 s of nothing asks the `probe` (Chat passes `refreshMe`, so the
   /me it asks is also dispatched into the session) and only an unreachable or unhealthy host ends
   the reply, as `host_stalled`. `aborted` carries a `why` when the app aborted; "You stopped this
   reply" is the reader's sentence only.
3. **`replyEnding(capped, tokens, maxOut, ctx)`** — cap when `out ≥ maxOut`; context when the sum
   reaches `ctx − max(8, ctx/64)`; `length` when it cannot tell; the cap wins when both are true
   because Continue still helps. `contextMeter` reads the same numbers, so the line and the meter
   cannot disagree.
4. **One health source** — `settle()` calls the engine unhealthy only on a /me that got through
   (`meOk && !healthy`); a request that got no answer makes the snapshot history (`meOk: false`)
   and asserts nothing about the engine; one 30 s poll pings and asks /me together, plus at once
   when a cooldown reaches zero and after every request. "Reconnect" is derived: offered exactly
   while `!live.meOk`, gone the moment a /me gets through. Chat's `broken` flag is deleted.
5. **Revoke stays** — `Live.paused` became `Live.key: active | paused | revoked | invalid`; both
   dead codes are `degraded('key')` with their own line and one action; only the connect screen
   (nothing to keep) still shows a card, now with Connect disabled and no "ready" notice above it.
6. **Store leader** — `electStore()` (storage.ts): the same Web Lock election as the tunnel
   identity, plus a queue (a follower leads when the leader's tab closes) and a steal ("Use this
   tab instead"). Followers never write; `loadChats` no longer marks a streaming reply interrupted
   (another tab may be writing it) — `reopenChats` does, only on taking the store. A leader that
   loses the lock mid-reply aborts with the reason. /me is shared under `bn.me.<scope>`.
7. **Phone** — the toast in flow and stacked above the drawer; the thinking block uncapped once
   the answer has started (`.thinking.full`).
8. **Connect** — a remembered or link invite connects by itself, once per page load, behind
   "Connecting to <host>…" with Cancel; the card only on Cancel or failure; the code masked with
   Show. No harness clicks Connect for a code in the URL any more.
9. **Nits** — "1 chat is / N chats are"; `hostsComputer()` ("the computer named Max's laptop");
   Copy on every answer; a stopped reply's footer says "still counted against today's tokens";
   `modelLabel` in footers; footers wrap under the button row (`.meta` flex-wrap), never into it.

### Edge awareness, one line

Handled: a probe that comes back after tokens resumed (ignored); a stall while still thinking
(the note lands in the collapsed block); a queued line longer than the idle clock (keepalives
re-arm it); Stop during the silence (still a stop); both walls true at once; a host with no
`model_context` (no meter, no wall claim); `invalid_key` mid-session; a paused key at connect
time (card, as 014); a shared /me arriving in a tab whose own session is broken (ignored — another
tab's good news must not heal this one); the leader's tab closing mid-reply (the follower is
promoted and reopens the orphan); no Web Locks at all (one tab, it leads); a link invite in a
browser that cannot persist it (seeds the first mount only); a follower's Regenerate/Edit/
Continue/New chat (all hidden); a Cancel after the transport opened (a superseding attempt; the
machine closes it).

### Verified (printed, from base `5bb43bc`)

```
$ pnpm install --frozen-lockfile  → Already up to date                                 exit 0
$ pnpm typecheck                  → tsc --noEmit, no output                            exit 0
$ pnpm test                       → Test Files 10 passed (10) | Tests 218 passed (218) exit 0
                                    0 failed, 0 skipped   (018 left 190; +28 here)
$ pnpm lint                       → eslint ., no output                                exit 0
$ pnpm build                      → index 229 kB (gzip 74); Chat 358 kB (gzip 109); css 13.5 kB
$ node dev/real-check.mjs …       → every scenario above on the real relay + host       exit 0
$ node dev/screenshots.mjs        → 75 screenshots in dev/screenshots/ (fakes + real), no console
                                    or page errors, no horizontal overflow at 360/390 px   exit 0
```

`package.json` and `pnpm-lock.yaml` unchanged: **no new dependencies.** The host, vite and browser
the harnesses started were the only processes stopped; the shared llama-server was only sent
requests; max-ws.lab untouched; ports 6670–6674 only.

### Not verified

- **Two tabs on a real phone**, and any browser but Chromium (as 004/007/014/018). The follower
  banner and the takeover were driven in Chromium contexts, desktop and 390 px emulation.
- **A takeover while the old leader is mid-reply** — unit-tested (the abort carries its reason;
  the follower reopens the orphan) but not driven on the real host.
- **`dev/int-check.mjs`** was not re-run: its pause expectations have been stale since 014.
- The 30 s poll's cost on a `/me` that is expensive: unchanged from 007's note, now unconditional.

### Skipped from 7–9, and why

Nothing the ticket names. Not done, because the ticket does not name them and the source ceiling is
one line away: the reviewer's (c) rate-limit banner nits (the number the app knows; the em dash;
one of two Try agains), (d) a dark-mode disabled token, (g) LaTeX rendering and per-block "copy"
casing. Listed as candidates below.

### Judgment calls

- **Pause keeps the turn in the thread, marked, with the composer off** — 014 rolled it back into
  the composer. One model for every undelivered turn (the ticket's own test: "exactly one bubble
  marked, and it clears on resume"), and the same shape revoke now has. The round-trip still heals
  itself: the poll clears the banner (25 s on the real host), then Try again delivers. Rule for the
  rollback if you want it back; it is the deleted branch.
- **"Reconnect" is derived from `!live.meOk`**, not from an error code, so it also appears when a
  background /me times out and disappears the moment one gets through. The window between a
  failed request and the /me that follows it is closed by the reducer setting `meOk: false` on a
  request that got no answer.
- **The idle probe is `refreshMe` itself**, so the /me it asks feeds the session; without a probe
  (tests, other callers) silence is never an ending — 014/018's behaviour, unchanged.
- **The idle tests use short real timers**, like the file's other deadline tests, not a fake clock:
  the generator drives a real `ReadableStream` and faking the clock under both is fragile evidence.
- **Slack is `max(8, ctx/64)`** (64 tokens at 4 096): the two friends' sums were exact, engines
  that reserve a few tokens are not, and the cap is checked first so slack never steals a cap.
- **The context-wall copy says "the 4.1k memory on Max's laptop"**, not "<host>'s memory": the
  possessive is the nit promise 9 removes.
- **A dead invite at connect time still gets the card** — there is no thread to stay in; the card
  no longer offers an enabled Connect or the "ready" notice. `invalid_key` mid-session is treated
  like revoke with its own line ("no longer recognises this invite").
- **The follower shows the leader's checkpointed reply** with "Arriving in another tab…" rather
  than hiding it: it reads live, it just never owns it (no Stop, no actions, no Edit).
- **Auto-connect retires the dev `?autoconnect`** — it is the default for any code in the URL or
  in storage; the harnesses' `&autoconnect` is inert and left in place.

### Bought beyond the ticket (declare loudly)

- **`dev/fake-bunny-tunnel.ts`: sleep is per session.** It answered /me while "asleep"; the real
  host does not (014 measured 45 s of relay pings and nothing else). Necessary for the fake's
  host-asleep shot to show the derived Reconnect. Dev harness, ~12 lines.
- **The fakes learned `/wall` (a context-wall usage chunk) and a revoke-aware `/me`**; the runner
  takes the store over before starting a thread (`lead()`), targets the sidebar's New chat (the
  wall puts one in the thread), and skips its production section under `PROD=0` so it can run
  beside `real-check`, which serves `dist/`.
- **`07-revoked-return.png` is removed** — that state no longer exists; `20-revoked-in-thread.png`
  and `20-revoked-new-code.png` replace it. `20-link-connecting.png` is the new busy face.
- **The reload note is generalised** ("closed or reloaded"): the same orphan rule now covers a
  closed tab and a hand-over.

### Candidates (not fixed here)

- Regenerate is still offered at the context wall; it would hit the wall again.
- After a stall over a dead tunnel, /me is asked twice (probe, then the post-request refresh), 10 s
  each; one would do.
- `dev/int-check.mjs` expects the pre-014 pause ejection.
- The reviewer's (c), (d), (g) above.

## Freeze

- **Base:** `5bb43bc` (public `main`, after 019/021 and the 4096 default). **Lane:**
  `t020-web-truth-2`, pushed, not merged.
- **Patch SHA-256:** of `git diff origin/main...HEAD --binary` at the freeze commit, reported with
  the freeze message (recording it here would change it).

| Bucket | Budget | Measured (raw added / deleted) | Verdict |
|---|---|---|---|
| Source TS/TSX (`web/src/**`, tests and css excluded) | ≤900 lines of change | +641 / −258 = **899** | inside, by one |
| Web tests (`web/src/**/*.test.ts`) | not budgeted | +418 / −55 | 10 files, 218 tests (+28) |
| Stylesheet (`web/src/styles.css`) | not source (ruling on 004) | +12 / −2 | 5 surfaces |
| Dev harness (`web/dev/**`) | not budgeted | +391 / −80 | fakes, both runners, six real scenarios |
| Screenshots | ≤500 KB each | 56 files changed vs base (15 new `20-*`, 1 removed, the rest re-shot); largest 156 KB | inside |
| Ticket record | — | Log + Report | — |
| Dependencies | none | **0 added**, lockfile unchanged | — |

**Concepts: 1 budgeted (store leader), 1 used** — `electStore` / the `bn.store.<scope>` lock and
the tab's `leader` role. Not counted, as properties of things that already existed or named by a
promise: `KeyState` (replaces the `paused` boolean), the `host_stalled` copy entry (as `host_asleep`
was), `aborted.why`, `FriendlyError.code`, the `bn.me.<scope>` key (the scope layout's fourth key),
`replyEnding` / `contextMeter` / `settlePending` / `reopenChats` (derivations), the `probe`
parameter, `hostsComputer`. Rule against me on any of these.

## Ruling (PM, 2026-09-02 18:55)

**Landed** on main (ff). The engineer wrote the report with real-host evidence for every promise (paused:
exactly one mark; stall: ends on its own at 25 s; context wall: 51 + 4045 = 4096 named as memory, no
Continue; asleep: 5 s / 15 s / reconnect 0.3 s; tabs: leader/follower with shared meters) and yielded
twice while waiting on its own harness; the PM performed the mechanical freeze (commit, rebase, checks
printed: typecheck 0, 218 tests, lint 0, build OK; 641/258 TS lines within ≤900; scope clean) and pushed.
Third experience pass is the launch gate.
