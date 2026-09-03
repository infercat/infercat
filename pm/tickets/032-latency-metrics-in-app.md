---
id: 032
title: Web app — show TTFT and time-per-output-token on each reply, honestly attributed
kind: normal
size: 1
status: dispatched
updated: 2026-09-03
release: demo-1
---

# 032 — TTFT / TPOT in the reply footer

**Why.** Founder (2026-09-03): show latency metrics in the web app. The technical launch audience
reads these numbers; the desktop reviewer already verified the token counts against the host, and
latency is the next thing they will check. The client has the data: first-delta timestamp, last-delta
timestamp, completion tokens from the usage chunk, and the `queued` keepalives (018) that mark time
spent waiting for a slot.

**Promises.**
1. Reply footer gains `ttft 61 ms · 42 tok/s` (tokens per second is what this audience says; also
   expose TPOT = 1000/tok/s in the hover/sheet). Measured **from the device**: TTFT = send → first
   content-or-reasoning delta; tok/s = completion tokens ÷ (last delta − first delta). Stated as such.
2. **Attribution is honest:** if the request was queued (keepalives seen), the footer shows
   `waited 4.2 s for a slot · ttft 61 ms` so the wait is not blamed on the model; if the path is
   relayed, the sheet says the relay hop is included (path RTT from the pill). Thinking tokens count in
   tok/s only if they streamed (they do); the sheet says whether the number includes thinking.
3. A per-chat average in the Limits/Settings sheet (median TTFT and tok/s over this chat), plus the
   same two numbers in `/me`-independent local state so they survive reload with the chat.
4. Evidence: vitest on the derivation (with queued keepalives, with thinking-only replies, with
   abort); a real-host screenshot; cross-check one reply's numbers against the host's `usage` line for
   the same request (they will differ by the relay hop — say by how much in the report).

**Size 1** (≤150 lines). Concept budget 0. **Scope:** `web/src/stream.ts` (timestamps), `Message.tsx`,
the sheet; tests. Same lane as 031 (same footer).

## Log

- **2026-09-03 03:22 ACK.** Base `3eb9033`, lane `t031-web-footer` (shared with 031; 031 lands first,
  one commit each).
- **03:26 Read before edit.** The gateway writes `: queued` on joining the slot queue and every 5 s
  (`internal/gateway/request.go:317`, `defaultQueuedEvery`) and **nothing when the slot is granted**, so
  from the device the wait for a slot is a lower bound (send → last keepalive) at 5 s granularity, while
  send → first delta is exact. The footer will say the exact number and the bound, never a split it
  cannot see. Timestamps go on the reply as it is reduced (`reduceReply` takes a clock, default
  `Date.now()`), persist with the message, and the sheet's medians are derived from the chat's messages
  — no second store, nothing to keep in step.
- **03:30 Built.** `startReply()`, the clock on `reduceReply`, `speed()` / `speedLine()` / `chatSpeed()`
  in `stream.ts`, `Timing` on the message, the footer span with its hover sentence, the sheet paragraph.
  8 vitests. Committed as `a23fbfb`, 140 source lines.
- **03:33 / 03:36 Real-relay runs** (`dev/footer-check.mjs`; see 031's Log): the first run's engine was
  idle, the second run's shared llama-server was carrying ticket 028's concurrency load test (both
  engine slots busy, five hosts and a `load` process on it — checked with `/slots` and `lsof` mid-run),
  which turned out to be the better cross-check: it shows where the time went. Evidence below.

## Report

### The core, shown working

Production bundle over the real New York relay against a real `bunny-network serve --slots 1` on the
shared llama-server (Gemma 4 E2B), `web/dev/footer-check.mjs`, run 2 (03:36), printed beside the
host's own `usage.jsonl` line for the same request:

```
REPLY 1   footer  "ttft 3.7 s · 138 tok/s"                                                  32-real-footer.png
          hover   "Measured on this device. Time to first token: from Send to the first token, thinking
                   or answer, with the relay hop and this app inside it. 138 tok/s is 7 ms per token:
                   completion tokens over first-to-last token, thinking included."
          host    ttft_ms 3646 · queued_ms 0 · total_ms 4672 · 137 tokens → 134 tok/s host-side
REPLY 2   footer  "ttft 6.4 s · 163 tok/s"      host ttft_ms 6370 · 8 tokens → 143 tok/s host-side
QUEUED    "Waiting for a free slot on Max's laptop…" at 0.1 s after Send, then
          footer  "ttft 29.8 s (≥20.1 s of it in line for a slot) · 167 tok/s"                32-real-queued.png
          hover   "… The host said this request was in line for a slot for at least 20.1 s of that; it
                   says so every 5 s and nothing when the slot comes, so the rest is not all the model's. …"
          host    queued_ms 20543 · ttft_ms 29700
SHEET     "5.1 s to the first token · 163 tok/s, 6 ms per token. The median over the 3 replies in this
          chat, measured on this device: Send to the first token, thinking or answer; completion tokens
          over first-to-last token, thinking included. The relay hop is inside both — the 29 ms round
          trip in the header right now. A reply that waited for a free slot says so on the reply and is
          left out of the first-token median."                                                32-sheet-medians.png
RELOAD    the three footers identical before and after                                        32-real-after-reload.png
```

**Cross-check against the host (promise 4).** Run 2's engine was busy with 028's load test, so the
device and the host agree that the time went to the engine: device ttft 3.7 s vs host `ttft_ms` 3646
(+54 ms), 6.4 s vs 6370 (+30 ms); tok/s 138 vs 134 over 137 tokens. Run 1 (03:33, engine idle; its
log was overwritten by run 2, numbers from the run's console): device **143 ms vs host 63 ms (+80 ms,
with the pill reading 71 ms round trip)**, 165 vs 158 tok/s over 108 tokens; the thinking-off reply 153
vs 90 ms (+63 ms), 211 vs 174 tok/s over 8 tokens. So the relay hop shows on ttft as roughly one round
trip, and tok/s agrees within about 5 % on a hundred-token reply; eight-token replies are too short to
compare rates (the ticket's formula has no first interval to count, see judgment calls).

**The wait is a floor, said as one.** The host had the request in line 20.5 s; the device's last
keepalive came at 20.1 s and the next one would have been due at 25.1 s, so "≥20.1 s" is exactly what
the device knows. The 9.2 s after the slot were the contended engine's — and the hover says the rest is
not all the model's rather than blaming it.

### Edge awareness, one line

Handled: a single-token reply (no rate); a stopped reply (ttft kept, no rate without a count); an
abort before the first token (nothing); a reply that only thought (timed like any other); a message an
older build stored without a clock (nothing shown, the medians skip it); a keepalive after a terminal
status (ignored); a chat whose every reply waited ("Every reply so far waited for a slot first"); direct
mode (no relay sentence); the sheet before any reply (no paragraph).

### Judgment calls

- **ttft is the exact Send-to-first-token number, always; the queue floor sits beside it in
  parentheses.** The ticket's `waited 4.2 s for a slot · ttft 61 ms` needs a split the gateway does
  not send (Log 03:26); inventing one would be the lie the ticket exists to prevent.
- **The medians leave queued replies out of first-token time** (the wait was the host's) and keep them
  for tok/s (measured the same as any other).
- **"Survives reload" is the persisted `timing` on each message plus a derivation** — no second store,
  no key, nothing that can disagree with the footers.
- **The fuller sentence is a `title` on the footer span** (a hover, as the ticket says); the sheet
  carries the same explanation for readers who cannot hover.
- **tok/s is the ticket's formula**, completion tokens ÷ (last − first): it counts one interval fewer
  than it has tokens, so short replies read high (8 tokens: 163 vs the host's 143). On the replies the
  audience will measure it is within a few percent.

### Bought beyond the ticket (declare loudly)

- **`web/dev/footer-check.mjs`** (dev harness, ~190 lines): the real-relay runner for 031 and 032 —
  starts and stops its own host and preview on 6840–6841, mints its keys, holds the one slot with a
  second key for the queued scenario, prints every footer beside the host's usage line.
- **The fake-driven screenshot set re-shot** (`dev/screenshots.mjs`, PROD=0 on 6842–6843): the footer
  grew, and that harness is what checks 360 px and 390 px for overflow.

### Not verified

The hover on a touch device (there is none; the sheet is the touch path); Firefox and Safari, as in
every earlier round; a queued reply whose wait ends within 5 s of Send (the floor is then the join
keepalive, near 0 — truthful, unhelpful).

### Candidates (not fixed here)

- **One SSE comment at slot grant** (`: served`, one line in `internal/gateway/request.go` next to
  `queued()`) would make the wait exact and the split honest on both sides; gateway scope, not this
  ticket's.
- `footer-check.mjs` reads the sheet's paragraph with a regex that stops at the first `.`; cosmetic.
