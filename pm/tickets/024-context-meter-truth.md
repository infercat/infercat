---
id: 024
title: Context meter measures the last exchange, not the chat — make it what the next question will carry
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

## Report

## Ruling (PM, 2026-09-02 22:58 — founder: fourth pass is the last)

Cut to two promises: (a) the context meter shows what the next request will carry, simplest truthful
derivation, no streaming estimate; (b) a refused oversized turn is dropped from the history the client
sends, with the ended-line copy saying so. Promise 3's five items and the streaming estimate → backlog
(ticket 012 list). Sent to the engineer with 023.
