---
id: 024
title: Final launch items — host-keyed chats, honest oversized-turn wall, delivered-turn rows, empty-answer death note, context meter (fourth pass, last)
kind: normal
size: 1
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 024 — Context meter truth

**Why.** Fourth-pass desktop spot-check: the header meter labelled "context", explained as "everything
said so far, both sides", shows the LAST exchange's prompt+reply including thinking tokens that are
never resent — it read 125 after turn 1 and 51 after turn 2, froze at 419 during a 2,500-token essay,
and read 3.5k/4.1k one message after the wall said the memory was full. The wall copy is honest; the
meter under it argues with it.

**Promises.**
1. The meter shows what the next request will carry: the history the client will actually send
   (after its own trimming rule), estimated locally (known prompt tokens from the last usage object +
   chars ÷ 4 for anything newer), updated after every send and while streaming (022 promise 7 estimate).
   It never goes down while a chat grows, and it agrees with the wall (≥ 4.1k when the wall shows).
2. The explainer sentence matches: "What the next message will carry — the chat so far, minus
   thinking, minus anything trimmed to fit. When it fills, older turns are dropped."
3. Composer gets focus when the chat view mounts (fourth-pass item 3, one line). Escape closes the
   Settings and Limits sheets (item 4). The card shown immediately after Disconnect is the returning
   card, same as after a reload (item 5). Per-minute meter refills client-side between polls (item 6).
   Self-probe backoff cap 10 s and a probe on `visibilitychange` (item 2).
4. vitests for 1 and the refill; screenshots of the meter across three turns and after the wall.

**Size 1** (≤150 lines). Concept budget 0. **Scope:** `web/src/**` (meter derivation, sheets, session
backoff), tests. Ports 6720–6729.

## Ruling (PM, 2026-09-02 22:58 — founder: fourth pass is the last)

Cut to two promises: (a) the context meter shows what the next request will carry, simplest truthful
derivation, no streaming estimate; (b) a refused oversized turn is dropped from the history the client
sends, with the ended-line copy saying so. Promise 3's five items and the streaming estimate → backlog
(ticket 012 list). Sent to the engineer with 023.

## Final ruling (PM, 2026-09-03 00:10 — fourth pass complete, both personas; this ticket is the last)

Synthesis: not launch-ready — one blocker, three screenshot-worthy frames; everything else ordinary
polish. Ranked once. **This ticket's promises are now exactly:**
1. **Chats are keyed to the host, not the invite (blocker).** A new code from the same host (revoke +
   re-issue, or `keys rotate`) opens the same drawer. Scope by the tunnel address (host identity), and
   migrate any existing invite-id-scoped chats into the host scope once on load. The revoked card gets the
   sibling card's sentence: "Your N chats with <host> are still on this device."
2. **An oversized turn never kills the thread.** When the previous turn alone exceeds the context, drop
   that turn from the history the client sends and say so in the ended line ("The essay was too long for
   <host>'s memory and was left out of this question."); a nine-word question after a refused paste is
   answered.
3. **Delivered turns lose their failure line.** The same derivation that clears the bubble's mark clears
   the assistant row's "Not sent — your invite is paused." (or softens it to grey "Carried into the next
   question."). Both actions for a rate-limited turn share one countdown state.
4. **Empty-answer death note.** If the host died before any answer text, the note reads "…stopped
   answering mid-reply — nothing of the answer had arrived yet, only its thinking." and renders outside
   the Thinking rule.
5. **Context meter** = what the next request will carry (never decreasing; agrees with the wall).
6. Tests for 1–3 and 5; one real-host screenshot each for 1, 2, 4. Checks printed.

**Backlog (ruled non-blocking, appended to 012):** the "host is back" line and the 32 s heal latency;
composer keeps its height after send; "0 messages left" while sends still work; follower composer
placeholder; the card right after Disconnect; Escape/focus/refill items already listed.

Size re-priced to **2** (≤400 lines); concept budget 0 (host scope replaces invite scope).

## Log

All times 2026-09-02, EDT, laptop.

- 23:05 — ACK. Fresh worktree `t024-context-meter` at `36966ec` (main, with 023 landed). Ruling
  cuts the ticket to two promises: (a) the meter shows what the next request will carry, (b) a
  turn that alone would not fit the memory is left out of the history sent and the reply says so.
  Everything else in the ticket (composer focus, Escape, the card after Disconnect, the per-minute
  refill, the probe cap and `visibilitychange`, the streaming estimate) is backlog by ruling.
- 23:08 — Design: one function, two readers. `carried(history, settings, modelContext)` in
  `stream.ts` is what the request sends — the system prompt, the answers (not cut-off or empty
  replies), never thinking — and, new, leaves out any single turn whose own estimate (chars ÷ 4,
  plus 4 per message for the template) would not fit the memory; it returns the messages and the
  turns left out. `contextCarried()` sums the same messages with the same estimate: the meter's
  number. `toChatMessages` in Chat.tsx is deleted in its favour; `contextUsed` (the last exchange's
  prompt + reply, thinking included — the quantity the fourth pass caught arguing with the wall) is
  deleted. The reply to a question whose history left something out carries a note; Message.tsx
  shows a note whether or not the reply ended badly. The sheet's sentence says what the meter is.
  Chars ÷ 4 is the estimate the wall probe measured against the gateway's count (13k characters →
  3339 tokens); the number is an estimate and the sheet says "what the next message will carry".
- 23:40 — Re-ruled to five promises, size 2. Meter (5) and the oversized turn (2) already on the
  real host: meter 0 → 18 → 36 → 79 across three turns, a ~7.9k-token paste refused by the host
  ("This conversation no longer fits the model…"), the next question answered "Paris" with "One
  earlier message was too long for the 4.1k memory on Max's laptop and was left out of this
  question.", meter 99 (the paste never counted). Shots `24-real-meter-three-turns`, `24-real-meter-left-out`.
- 23:45 — (1) `hostScope(addr)`: FNV over the address alone; `adoptInviteScope(addr, keyId)` moves
  an earlier build's chats (merged by id, nothing duplicated), settings and shared /me from the
  invite scope into the host scope and forgets the invite keys, so it runs once; called in Chat's
  first render before `loadChats`. `Chat` is keyed by the address alone. The card after a revoke —
  and the empty one for the next code — says "Your N chats with <host> are still on this device."
  whenever the last host is known and has chats. W6's "one host, two keys, two drawers" is
  overturned by the ruling; its tests now say the opposite. A `LastHost.scope` written by an older
  build points at the invite scope: the count there is right until the next verify rewrites it,
  and right after (the chats are wherever it points). (3) `carriedAfter(messages)` — the same
  derivation as the bubble's mark, read for the row: an `interrupted` reply whose turn a later
  request carried renders "Your message was carried into the next question." in grey instead of
  its red note; the thread's Try again reads the banner's countdown (`disabled`, "Try again in Ns")
  — one `retryUntil`, two readers. (4) `host_stalled` with no answer text: "<host> stopped answering
  mid-reply — nothing of the answer had arrived yet, only its thinking. Try again — …"; the note
  lives inside the Thinking block only for `no_answer` now (the model's own choice), never for a
  cut-off.

## Report

### The core, shown working

Production bundle over the real New York relay against a real `bunny-network serve` on 6720, keys
minted and revoked with the host's own CLI (`dev/real-check.mjs`, 23:30–00:05):

```
rekeyed  (promise 1) keys revoke mid-chat → banner, still in the thread · Paste a new code → the card:
         "Your 1 chat with Max's laptop is still on this device." · a new code minted for the same host,
         pasted, Connect → the drawer: ["Write three short paragraphs about light…"] — the same chat
                                                                            24-real-rekeyed-same-chats.png
left-out (promise 2) a ~7.9k-token paste → "This conversation no longer fits the model. Start a new chat,
         or shorten what you just sent." · the next question "What is the capital of France?" → "Paris" ·
         under it: "One earlier message was too long for the 4.1k memory on Max's laptop and was left out
         of this question." · meter 99/4.1k (the paste never counted)         24-real-meter-left-out.png
carried  (promise 3) three answered turns · keys pause · "Please remember the secret word ZEBRA." → 1 mark, the
         row under it "Not sent — your invite is paused." · resume → reload → the next question → the
         model recalls ZEBRA · marks 0 · the row under ZEBRA now: "Your message was carried into the
         next question."                                                (22-real-zebra-recalled.png, re-shot)
death    (promise 4) host SIGKILLed while the model was still thinking, no answer text on screen →
         "Max's laptop stopped answering mid-reply — nothing of the answer had arrived yet, only its
         thinking. Try again — if it keeps happening, their machine may have gone to sleep." · inside the
         Thinking block: 0                                                        24-real-empty-death.png
meter    (promise 5) before anything 0/4.1k · after turn 1: 18 · turn 2: 36 · turn 3: 79 — never down ·
         after the refused paste still 79 · after the next question 99         24-real-meter-three-turns.png
```

Vitests: (1) one host one drawer whatever the code, two hosts two drawers; an earlier build's
invite-scoped chats and settings adopted once — merged by id, nothing duplicated, the old keys
gone, a second call a no-op, a host this build always knew untouched. (3) the row under a turn a
later question carried is softened, and not before; a reply that answered, or a failure with
nothing delivered after it, untouched. (4) the death note names the thinking that arrived, says
nothing arrived when nothing did, and keeps the ordinary line once the answer had started. (5) the
meter counts the system prompt and every answer, never thinking, never a cut-off or empty reply,
never decreases across six growing turns; a 20 000-character paste is carried by its own request,
left out of the next question with the turn named, absent from the meter the moment it is refused,
never left out by a host that has not said how big its memory is. 229 → 239.

### Edge awareness, one line

Handled: a `LastHost` record from an older build (its scope is wherever the chats are, before and
after the move); two oversized turns (both left out, the note counts them); a turn edited shorter
(the meter goes down, honestly); an empty system prompt; a reply that only thought (`no_answer`:
not carried, note inside Thinking as before); a stall after the answer began (the ordinary line);
a rate-limited turn's Try again in the thread while the banner counts (same number, both
disabled); a follower tab (same derivations over the same store); a host with no `model_context`
(nothing left out, no meter, as before).

### Verified (printed)

```
$ pnpm install --frozen-lockfile  → Done in 158ms using pnpm v11.13.0                    exit 0
$ pnpm typecheck                  → tsc --noEmit, no output                             exit 0
$ pnpm test                       → Test Files 10 passed (10) | Tests 239 passed (239)  exit 0
                                    0 failed, 0 skipped   (023 left 232; +7 here, 2 rewritten)
$ pnpm lint                       → eslint ., no output                                 exit 0
$ pnpm build                      → index 231 kB (gzip 74.5); Chat 359 kB (gzip 110); css 13.8 kB  exit 0
$ node dev/real-check.mjs meter   → 0 → 18 → 36 → 79 · paste refused · "Paris" + the left-out note · 99   exit 0
$ node dev/real-check.mjs revoke  → the card's sentence · the re-issued code opens the same drawer     exit 0
$ node dev/real-check.mjs emptydeath → the death note, outside the Thinking block                     exit 0
$ node dev/real-check.mjs paused  → ZEBRA recalled · marks 0 · the refused row softened                exit 0
$ node dev/screenshots.mjs (PROD=0) → 94 screenshots; no console or page errors, no horizontal overflow at 360/390 px;
                                    the fake wall's meter "50/8.2k context" (the next question's load)  exit 0
```

`package.json` and `pnpm-lock.yaml` unchanged: **no new dependencies.** Processes: only the host,
previews, dev servers, fake gateways and browsers the harnesses started (6720–6726), all gone; the
shared llama-server only received requests; max-ws.lab untouched.

### Backlog by ruling (not started)

Composer focus on mount; Escape closing the sheets; the returning card right after Disconnect; the
per-minute meter refilling between polls; the probe cap at 10 s and a probe on `visibilitychange`;
the streaming estimate; the "host is back" line and the heal latency; composer height after send;
"0 messages left"; the follower placeholder. None touched.

### Judgment calls

- **The meter at the wall reads ~3.3k, not ≥ 4.1k.** "Agrees with the wall" as "≥ 4.1k when the wall
  shows" would require counting the model's thinking, which is never sent: the wall probe measured a
  4 096-token wall (51 in + 4 045 out) followed by a next question that carried 3 458. The meter shows
  that second number — what the next question will carry — under a wall row that says the reply
  stopped short and replies will keep getting shorter; the sheet's sentence says which the meter is.
  Counting thinking would put back the quantity the fourth pass rejected.
- **The oversized turn's own request still goes to the host.** "A *refused* oversized turn": the host
  refuses it once, in its own words, and only later questions leave it out. Dropping it before sending
  would answer an empty question with a note about a paste the host never saw.
- **The estimate is chars ÷ 4 plus 4 per message**, one function for the meter and the rule, measured
  against the gateway at 3 339 tokens for 13k characters.
- **Copy:** "One earlier message was too long for the 4.1k memory on Max's laptop and was left out of
  this question." — the ruling's "the essay" and "<host>'s memory" become "one earlier message" (it is
  not always an essay) and 020's own phrasing (no possessive). The softened row says "Your message was
  carried into the next question." rather than "Carried into the next question." — under a partial
  reply the shorter one read as if the reply were carried; it was the turn.
- **W6 is overturned, not amended:** one host, two codes, one drawer. Two people sharing one browser
  with two invites to the same host now share a drawer; the ruling names the trade (a re-issued code
  must open the same chats) and the concept budget note says the host scope replaces the invite scope.
- **The migration is one-way and runs in Chat's first render**, before `loadChats`, because it needs
  the key id the card does not have; a browser that never opens the chat never migrates, and its
  card still counts the right chats (see edge awareness).

### Not verified

- Chars ÷ 4 on code, CJK or heavily tokenised text — the gateway's count is the truth, the meter an
  estimate, and the sheet says so.
- Two tabs of one browser on two codes to the same host (they now share a drawer; the leader
  election is per scope, so one of them follows — as for one code).
- Firefox and Safari, as before.

## Freeze

- **Base:** `36966ec` (public `main` at dispatch, with 023 landed; rebased onto `origin/main` at the
  freeze). **Lane:** `t024-context-meter`, pushed, not merged.
- **Patch SHA-256:** of `git diff <base>...HEAD --binary` at the freeze commit, reported with the
  freeze message.

| Bucket | Budget | Measured (raw added / deleted) | Verdict |
|---|---|---|---|
| Source TS/TSX (`stream.ts`, `storage.ts`, `App.tsx`, `ui/Chat.tsx`, `ui/Connect.tsx`, `ui/Message.tsx`) | ≤400 lines of change | +176 / −47 = **223** | inside |
| Web tests (`stream.test.ts`, `storage.test.ts`) | not budgeted | +125 / −23 | 239 tests (+10 vs 023; the two W6 tests rewritten to the ruling) |
| Dev harness (`dev/real-check.mjs`) | not budgeted | +78 / −2 | `meter`, `emptydeath`; `revoke` and `paused` extended |
| Screenshots | ≤500 KB each | 4 new `24-real-*` | inside |
| Dependencies | none | 0 added, lockfile unchanged | — |

**Concepts: 0 budgeted, 0 used.** The host scope replaces the invite scope (the ruling's own note);
`adoptInviteScope`, `carried`, `contextCarried`, `carriedAfter`, `estimateTokens` are derivations;
`ThreadAction.disabled` and the `carried` prop are renderings of state that exists; `Message.note` on
a complete reply is a field that existed.
