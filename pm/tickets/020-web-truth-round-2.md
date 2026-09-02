---
id: 020
title: Web truth, round 2 — per-turn state, stream idle end, reply-end classification, one health source, tab leader (second experience pass)
kind: normal
size: 3
status: dispatched
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

## Report
