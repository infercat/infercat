---
id: 014
title: Web polish — the two failures a friend will hit, honest meters, touch, copy (from the friend-experience pass; folds in 013)
kind: normal
size: 5
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 014 — Web polish

## Binding

**Why.** A fresh-context reviewer used the app as an invited friend on a phone against the real host
and relay: "the happy path is genuinely lovely; the error writing is better than most products I pay
for; what breaks the spell is that the app is honest about everything except the two things a friend
will actually hit." Those two, and the rest, ranked. Design law: each fix goes through the session and
message machines from 007, not around them.

**Promises.**
1. **Host asleep or offline (blocker).** Sending while the host is gone shows "Still waiting for
   <host name>…" under the spinner at ~5 s and fails at ~15 s (not the bridge's 30 s) with
   "<host name> didn't answer. It's probably asleep or offline — your message is saved, try again in a
   minute." and a Try again that resends. The user's text stays in the thread as a pending turn. Raw
   transport strings (`dial port 80: context deadline exceeded`, `the connection closed inside the
   response`) never appear in primary copy; a "Details" disclosure may hold them. The session enters
   `degraded(engine)` on this failure so the header tells the truth (promise 3).
2. **Paused is recoverable (blocker).** `key_paused` keeps the chat on screen with an inline banner
   above the composer: "<host name> paused your invite. Your message is still here — send it again once
   they resume." The composer keeps its text. Model this as a `degraded` reason (`key`), not a new
   state. Only `key_revoked` and `invalid_key` eject to the connect screen, and that screen's primary
   action is "Paste a new code" (Try again cannot work for a revoked code). Drop the rotation sentence
   from the paused/revoked screens.
3. **Unknown is not zero.** When the last `/me` or ping failed, the header dims, the path reads
   "<host name> — not answering", and the meters render "— / 20 per minute", "— / 200k tokens today".
   Never render fabricated zeros or a stale live latency.
4. **Two tabs (blocker).** Conversation storage is multi-tab safe: per-conversation keys with a
   `storage` event listener so a tab re-reads before it writes; the last exchange is never silently
   lost. (Cheap alternative if this exceeds budget: on load, if another tab holds the session lock,
   show "This chat is open in another tab" and make this tab read-only until "Use this tab instead".)
   Folds in 013: the user turn is saved before I/O; a streaming reply is checkpointed every 2 s;
   `streaming → interrupted` on reload with the partial text kept.
5. **Messages, not requests.** The friend's meter counts messages: "8 messages left this minute" and
   in Settings "Limits: about 10 messages a minute, 200k tokens a day". Find and fix why one chat turn
   costs two requests (the client must not call `/v1/models` or anything RPM-counted per send; models
   are fetched on connect and by the 60 s poll). **Ruling for the gateway:** RPM counts model calls
   (`/v1/chat/completions`, `/v1/embeddings`) only; `/v1/models` is not counted (settle-table row) —
   this is the one allowed edit under `internal/gateway` and it gets a test.
6. **Touch keyboard.** On touch devices Return inserts a newline and the Send button is the only way
   to send; the "Enter sends · Shift+Enter" hint is shown only on pointer devices.
7. **Privacy sentence, precise.** Connect screen, empty state, and Settings say one true thing:
   "Encrypted end-to-end from your device to <host name>'s computer — the relay in between can't read
   it. Bunny Network records counts, never text. The model runs on their machine." (Keep the
   `log_prompts` disclosure variant from 007.)
8. **Delete with undo.** Deleting a conversation shows "Chat deleted · Undo" for six seconds instead of
   a confirm dialog; the × has a 44 px target set apart from the open target.
9. **Returning state.** A remembered invite with saved chats gets its own connect face: "Welcome back",
   "Your 3 chats with <host name> are still on this device.", primary "Reconnect"; the code is masked
   (`bn1.tco2…N96as`) behind a Show toggle.
10. **Say it once.** Rate-limit: inline per failed message "Too fast — not sent."; the single toast
    carries the explanation and countdown (keep the countdown). Replace "not sent as context" with
    plain words. Connect screen: clear the previous connection error when the field changes; disable
    Connect whenever the inline format check fails. Fragment invite: persist it as soon as it is read
    and label "Invite from your link is ready." (still one click to connect).
11. **Names and units.** Model display name derived from the id ("Gemma 4 E2B"; full id in Settings
    only); "relayed via New York" (region code → city, small table); meters tappable on touch, opening
    one sheet that explains both limits in a sentence each; footer counts labelled ("25 tokens in ·
    190 out"). Settings: slider in the accent colour with "Lower is more predictable, higher is more
    surprising." and Cancel beside Done.
12. **Evidence.** vitest for every reducer change (degraded reasons, pending turn, unknown meters,
    multi-tab storage, messages meter, touch branch) and Playwright screenshots via the 004/007 runner
    of: host-asleep at 5 s and at fail, paused banner with text kept, unknown meters, welcome-back,
    delete-undo, touch composer at 390 px, rate-limit said once. `pnpm typecheck && pnpm test && pnpm
    lint && pnpm build` printed.

**Size 5** (≤2000 TS/TSX source lines of change). Concept budget 3: `degraded(key)` reason, pending
turn, undo toast. **Normal**; a follow-up experiential pass runs after landing.

**Scope contract.** `web/**` except `web/wasm/**`; plus `internal/gateway/**` for promise 5's one
settle-table row and its test only (ticket 011 is concurrently changing the gateway's upstream import —
keep the edit tiny). Not `cmd/**`.

**Keep (from the reviewer; do not touch):** the three-step connect checklist; the invite format errors;
"You stopped this while it was still thinking."; keeping the partial answer with Copy/Regenerate; the
thinking block's collapse; the rate-limit countdown toast; "One reply at a time"; the empty state and
suggestion chips; stripping the fragment; edit-in-place; settings persistence; the visual restraint.

## Background

- Reviewer report: workflow `wf_9e6581b7-630` journal (phone persona); screenshots under
  `/Users/yuanpingsong/.claude/jobs/12b4a99c/tmp/ux-web-phone-shots/`. A desktop persona is re-running;
  the PM appends its items to this ticket if they add anything.
- Session and message machines: `web/src/session.ts`, `web/src/stream.ts` (007), `docs/DESIGN.md` §2.
- Host-side items from the same pass go to the host lane: `keys add` prints the invite line again
  after the QR; `status` may keep printing the address (it is the host's own).

## Log

## Report

## Additions (PM, 2026-09-02 15:45 — desktop persona of the experience pass; sent to the engineer)

13. Reconnect after the host comes back (blocker): tear down and re-establish the session on retry after
    a broken connection; banner action "Reconnect"; honest fallback copy.
14. Reply cap ending (blocker): `finish_reason: length` → "This stopped at your invite's N-token reply
    limit." + [Continue]; Settings' limits sentence includes the reply cap.
15. First suggestion chip answerable (seed a default system prompt or change the chip).
16. Edit/regenerate keep the old answer ("Replace answer", retitle, previous answer kept).
17. Rendering: single newlines as line breaks; reasoning as markdown; "Untitled chat".

Synthesis verdict (both personas): not launch-ready yet; "everything wrong lives in the second minute
and nothing wrong is architectural"; the first minute is "genuinely excellent". Host-side item from
the same pass → ticket 016 (usage meter resets on host restart).
