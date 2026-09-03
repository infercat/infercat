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

## Report

### The core, shown working

<!-- TRANSCRIPT -->

Vitests (stream.test.ts, +2 for 024; the `contextUsed` test replaced): the meter counts the system
prompt and every answer — never thinking, never a cut-off or empty reply — and never decreases
across six growing turns; a 20 000-character paste is carried by its own request (the host is the
one to refuse it), left out of the next question with the turn named, absent from the meter the
moment it is refused, and never left out by a host that has not said how big its memory is. 233.

### Edge awareness, one line

Handled: a host with no `model_context` (nothing left out, meter absent as before); an empty
system prompt (not counted); a reply that only thought (`no_answer`, not carried, not counted); a
turn edited shorter (the meter goes down — honestly, the chat shrank); two oversized turns (both
left out, the note counts them); the wall reply itself (its content is carried, its thinking is
not); a follower tab (derives the same number from the same store).

### Verified (printed)

<!-- VERIFIED -->

### Skipped by ruling (backlog, not started)

Composer focus on mount; Escape closing the sheets; the returning card immediately after
Disconnect; the per-minute meter refilling between polls; the probe cap at 10 s and a probe on
`visibilitychange`; the streaming estimate. None touched.

### Judgment calls

- **The meter at the wall reads ~3.3k, not ≥ 4.1k.** The ruling's parenthetical ("agrees with the
  wall — ≥ 4.1k when the wall shows") would require counting the model's thinking, which is never
  sent; the wall probe measured a 4 096-token wall (51 in + 4 045 out) followed by a next question
  that carried 3 458 tokens. The meter now shows that second number — what the next question will
  carry — and the wall row above it says the reply stopped short and replies will keep getting
  shorter. The two say different, both-true things, and the sheet's sentence says which one the
  meter is. Counting thinking would put back the quantity the fourth pass rejected.
- **The oversized turn's own request still goes to the host.** The ruling says "a *refused*
  oversized turn": the host refuses it once, in its own words ("This conversation no longer fits
  the model"), and only later questions leave it out. Dropping it before sending would answer an
  empty question with a note about a paste the host never saw.
- **The estimate is chars ÷ 4 plus 4 per message**, the same for the meter and the rule (one
  function), measured against the gateway's count at 3 339 tokens for a 13k-character essay. The
  sheet calls it "what the next message will carry"; the label is unchanged.
- **Copy: "One earlier message was too long for the 4.1k memory on Max's laptop and was left out
  of this question."** The ruling's sentence said "the essay" and "<host>'s memory"; the turn is
  not always an essay, and the possessive is 020's own nit, so the sentence keeps its shape and
  loses both.
- **The note rides on the reply** (a `note` a complete reply may now carry, shown the way an ending
  is shown). No new field, no new state.

### Not verified

- Chars ÷ 4 on code, CJK or heavily tokenised text: the gateway's count is the truth, the meter is
  an estimate, and the sheet says so.
- A paste between 4.1k and the memory minus the reply floor: it is carried (it fits alone) and the
  host shrinks the reply to fit, as before; not driven here.


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
