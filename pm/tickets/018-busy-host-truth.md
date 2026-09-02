---
id: 018
title: Busy host is not an asleep host — early headers and queued keepalives on streaming requests
kind: sensitive
size: 2
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 018 — Busy host truth

## Binding

**Why.** After 014, a friend whose request gets no bytes for 15 s is told the host "didn't answer — it's
probably asleep or offline". A host that is merely busy (all slots taken; the request is waiting in the
FIFO queue for up to 30 s) produces exactly the same silence, so the app now states something false in
the most common launch-day situation: several strangers at once. "Surfaces tell the truth" (BELIEFS).

**Promises.**
1. **Gateway.** For a streaming chat request (`stream: true`) that is admitted and must wait for a slot,
   the gateway writes the response head at once — `200`, `Content-Type: text/event-stream`, the usual
   no-buffering headers — and then an SSE comment line `: queued` immediately and every 5 s while
   waiting (comments are legal SSE and invisible to compliant parsers). When the slot arrives, the
   normal relay follows on the same response. If the queue times out, the request ends with a normal
   SSE error event `data: {"error":{"code":"queue_timeout","message":…}}` followed by `[DONE]`
   (the client already treats error events as terminal, 007). Non-streaming requests keep today's
   503. The settle table is unchanged (queue timeout stays a counted, uncharged row); `Retry-After`
   moves into the error event as `retry_after` seconds since headers are already sent.
2. **Client.** The message lifecycle distinguishes three silences: no response head by 15 s →
   "<host> didn't answer…" (014's copy, `degraded(engine)`); response head received but no token yet →
   the pending line reads "Waiting for a free slot on <host>…" and the session stays `connected`;
   a `queue_timeout` error event → "<host> is busy — every slot was taken for 30 s. Try again in
   N s." with the countdown, message kept. The `: queued` comments reset the client's first-token
   wait so a queued request is never called asleep.
3. **Evidence.** Gateway tests: head + first `: queued` within 100 ms of admission with Slots=1 and one
   request already running; a comment every 5 s (fake clock or 6 s wait); queue timeout as an SSE error
   with `[DONE]`; non-stream path unchanged. Client vitests for the three silences over the stream event
   source; a Playwright screenshot of "Waiting for a free slot…" against the real host with
   `--slots 1` and a second key holding the slot. `go test -race ./internal/gateway/` and the web checks
   printed.

**Size 2** (≤400 source lines). Concept budget 0 (a comment line and one more message-pending reason,
which is a rendering of existing state). **Sensitive** (streaming relay path).

**Scope contract.** `internal/gateway/request.go`, `proxy.go`, `queue.go` (relay/queue only), their
tests; `web/src/stream.ts`, `web/src/session.ts` or the message reducer, the UI line that renders the
pending state, tests, one screenshot. `docs/ARCHITECTURE.md` is the PM's — list the contract lines
changed.

## Background

- The pipeline (`request.go`): `acquireSlot` waits before `callUpstream`; the head is written by
  `relay` today. Promise 1 moves the head (for streaming requests only) to the moment `acquireSlot`
  starts waiting; a request that gets a slot immediately behaves exactly as today.
- Client: `web/src/stream.ts` (event source), 014's pending turn and 15 s bound (`api.ts`/`Chat.tsx`).
- Ports for any local run: 6620–6629 (valid TCP range). Shared llama-server 127.0.0.1:18080, requests only.

## Log

- 2026-09-02 (ACK) base `64fb2f1` (public main), lane `t018-busy-host`. Read BELIEFS, ARCHITECTURE, DESIGN §1.4–1.6/§2.3,
  014's report, `request.go`/`proxy.go`/`queue.go`, `stream.ts`/`api.ts`/`Message.tsx`. Premise holds: the head is
  written in `pipeStream` (proxy.go:524-528) after `callUpstream`; `acquireSlot` (request.go:295) blocks inside
  `slotQueue.acquire` with nothing written. No contest. Plan: `acquire` takes a `queued func() error` called on joining
  and every 5 s (a `slotQueue` field-free change; the interval is a gateway constant like the deadlines); the request's
  `queued` writes the head + `: queued` under `armWrite`; `pipeStream` skips a head that is out; `writeStreamError`
  carries `retry_after` and ends with `[DONE]`. Client: `sseData` yields comment-only blocks, a `queued` event, a
  `queued` pending reason, the notice timer re-armed by each keepalive.
- 2026-09-02 16:25 gateway done: `slotQueue.acquire(ctx, wait, every, queued)` — `queued` runs once on joining and on a
  ticker while waiting; `request.queued` writes the head (`streamHead`, shared with `pipeStream`) and `: queued\n\n`
  under `armWrite`; a failed keepalive write leaves the queue as `client_closed` (not counted), and a slot handed over in
  that instant is handed on (`TestQueuedClientGoneFreesThePlace/at the queue`). `writeStreamError` now carries
  `retry_after` and ends with `[DONE]` (one shape for "the stream is over with an error"; the two engine-cut paths get
  it too). Five named tests in `busy_test.go`; the first cut failed only on my own assertion — on loopback a queued
  request can be handed its slot in under a millisecond, so `queued_ms` was legitimately 0; the test now waits 30 ms.
  Full suite with `-race`: ok.
- 2026-09-02 16:28 client done: `sseData` yields `{data}|{comment}` blocks (comment-only blocks were invisible; the
  keepalive is one); `queued` StreamEvent; reducer `queued`/`waiting` — the newer wins; each keepalive re-arms the
  5 s notice to 2× so it never races the next keepalive; `retry_after` read from an in-stream error. Found while
  testing: in-stream and head errors were described without the host's name (`rawChatEvents` never received it), so
  "{host} allows…" read "your host allows…" — threaded `hostName` through (3 lines, declared below). 190 vitests.
- 2026-09-02 16:31 real stack, run 1 (`web/dev/busy-check.mjs`, host `--slots 1` on 127.0.0.1:6620, vite 6621,
  bob holding the slot with a 2 500-token generation): "Waiting for a free slot on Max's laptop…" at 0.1 s after
  Send, unchanged at 6.1 s, header not degraded, path "direct · 4 ms", served on the same response at 18.4 s
  (queued_ms 15192, ttft_ms 15238). The timeout scenario did not reach 30 s: the holder's 7 000-token request stopped
  at 408 tokens (temperature 1, the model gave up counting). Re-running with a deterministic holder.

- 2026-09-02 16:36 real stack, runs 2 and 3 (deterministic holders: temperature 0; the engine's 4 096 context caps one
  reply near 24 s, so the timeout scenario holds with bob running and carol in line ahead of alice): both scenarios
  held, printed in the Report. Run 2's only "problem" was the harness reading `code` as `''` when the event omits
  it; fixed, run 3 clean. Lint caught `AbortController` undeclared in the harness (`globalThis.`), fixed. Freeze
  checks printed below; base unchanged at `64fb2f1` after `git fetch` (rebase a no-op).

## Report

### The core, shown working

**Promise 1 — the gateway says "in line" the moment it is.** `slotQueue.acquire` now takes the request's
`queued` hook: on joining the queue and every 5 s after, a streaming request writes its head and a
`: queued` comment under the write deadline; a slot free at once writes nothing new. The engine's bytes
follow on the same response. The queue timeout ends the stream as an error event with `retry_after`,
then `[DONE]`. Five named tests (`internal/gateway/busy_test.go`): head + first comment within 100 ms
with Slots=1 and bob running; a comment every interval; the timeout as an SSE error with `[DONE]`,
counted and charged 0 (the settle row); the non-stream 503 and the at-the-door 503 unchanged; a
friend who leaves while queued (through the pipeline and, at the queue, a keepalive whose write
fails in the same instant the slot is handed over — handed on, never leaked).

**Promise 2 — the three silences, on the real host** (`web/dev/busy-check.mjs`: the real web client in
Direct mode through the dev listener on 127.0.0.1:6620, a real `bunny-network serve --slots 1` on the
shared llama-server, bob and carol holding the slot with deterministic 4 000-token generations):

```
"Waiting for a free slot on Max's laptop…"   at 0.1 s after Send · unchanged at 6.1 s (past the
                                             first keepalive and 014's 5 s notice)
  header degraded: false · path "direct · 5 ms" · session connected throughout
  served on the same response at 18.4 s (bob held the slot 14.7 s) · reply "Hi there, hello."
  usage.jsonl: status 200 code "" queued_ms 14667 ttft_ms 14711        (ttft is still the engine's byte)

queue timeout at 30.4 s: banner "Max's laptop is busy  Every slot was taken — your message is
                                 still here.  [Try again in 4s] [Dismiss]"
  under the message: "Not sent — every slot was taken." · pending turn kept: true · action "Try again"
  header degraded: false · path "direct · 1 ms"
  usage.jsonl: status 200 code "queue_timeout" queued_ms 30000          (counted: meter 18 left; charged 0)
```

Screenshots: `web/dev/screenshots/18-real-queued.png`, `18-real-served.png`, `18-real-busy.png`.
The first silence (no head by 15 s → "didn't answer") is 014's and its tests still pass; the
keepalives cannot reach it because the head arrives first. Client vitests for the three silences
over `chatEvents` with a `busy()` transport: queued and never asleep however long the line; "still
waiting" only once the keepalives have stopped for two notice intervals; the timeout as
`queue_timeout` with its countdown and no reconnect. `sseData` now yields comment-only blocks; the
reducer's `queued`/`waiting` — the newer wins; `Message.tsx` renders the three sentences.

### Edge awareness, one line

Handled: a keepalive racing the hand-over (the slot is handed on); the very first keepalive write
failing (place dropped, not counted); a queued stream whose engine then fails (error event inside the
200, `retry_after` for `upstream_down`); the at-the-door refusal of a stream still a 503 (no head);
TTFT unchanged as the engine's first byte; a comment sharing a block with data (data wins) and CRLF
comment blocks; reload mid-queue (`queued` cleared with `waiting`); a nameless host in every new sentence.

### Verified (printed, from base `64fb2f1`)

```
$ go build ./... ; go vet ./...                      exit 0 ; exit 0
$ go test ./...                                      11 packages ok (gateway 16.3 s), 0 failures
$ go test -race -count=1 ./internal/gateway/         ok 17.3 s; 54 top-level tests PASS, 5 of them new
$ gofmt -l cmd internal                              no output
$ pnpm typecheck                                     tsc --noEmit, no output                     exit 0
$ pnpm test                                          9 files; 190 passed (190) | 0 failed | 0 skipped   (014: 181; +9)
$ pnpm lint                                          eslint ., no output                          exit 0
$ pnpm build                                         index 226.94 kB (gzip 73.02); Chat 355.66 kB (gzip 108.53)
$ node dev/busy-check.mjs all                        both scenarios above; run 3 (queued) ends "no console or page errors"
```

`package.json` and `pnpm-lock.yaml` unchanged: **no new dependencies.** The host, vite and browser the
harness started were the only processes it stopped; the shared llama-server was only sent requests.

### Not verified

- **The tunnel transport end to end.** The real-stack run is Direct mode through the dev listener.
  `http1.ts` resolves `fetch` on the response head exactly as the browser's does, and 014's
  `raceAbort` covers the abort path, but the wasm bridge over the relay was not driven with a queued
  request.
- **A friend who vanishes without closing** (laptop lid, tunnel silently gone) while queued. The
  keepalive's 11-byte writes never fill a socket buffer, so the write deadline cannot detect a silent
  peer; the 30 s queue timeout bounds the wait, and if the slot arrives first the dead request runs
  until the 60 s write deadline fires on a full buffer — the same bound a dead client *holding* a
  slot has today. A friend whose client closes or aborts (the browser's Stop, a closed tab, 014's
  reconnect) sends a FIN: net/http's background read cancels the context and the place is dropped —
  that path is tested through the pipeline. The dispatch's reasoning ("the write is what detects
  it") holds for a peer that resets the connection; it does not hold for one that goes silent.
- iOS Safari and Firefox: Chromium only, as in 004/007/014.

### Contract lines changed (`docs/ARCHITECTURE.md` is the PM's)

1. §Gateway HTTP API, `POST /v1/chat/completions`: a streaming request that must wait for a slot gets
   its head (`200`, `text/event-stream`, no-buffering headers) and a `: queued` SSE comment at once
   and every 5 s while it waits; the engine's stream follows on the same response; a queue timeout
   is then an SSE error event carrying `retry_after` (seconds), followed by `[DONE]`.
2. §Error format: inside a stream the error event carries `retry_after` in place of the header, and
   every stream error event is followed by `[DONE]`.
3. §Statuses: `503 queue_timeout` (+ `Retry-After`) for non-stream requests and for any request
   refused before the queue (waiting set full); a queued stream that times out ends inside its 200.
4. §Deadlines: client write 60 s "re-armed per event" → "re-armed per event and per `: queued`
   keepalive; for a queued stream it starts when the head is written". No row added or removed.
5. §Usage events: a queued stream that fails records `status 200` with its `code`, as mid-stream
   errors already do; `queued_ms` is the wait, `ttft_ms` remains the engine's first byte.

### Judgment calls

- **`[DONE]` after every stream error event**, not only the queue timeout's: one shape for "the
  stream is over with an error", in `writeStreamError`; the two engine-cut tails gain it too. Zero
  extra lines; the client stops at the error event either way.
- **The banner copy is "{host} is busy — Every slot was taken — your message is still here."**, not
  the ticket's "every slot was taken for 30 s". `queue_timeout` is one code for two facts — a wait
  that ran out (in-stream) and a line already full at the door (503 at once) — and "for 30 s" is
  false for the second; the host's own sentence ("waited 30s for a free slot" / "N requests already
  waiting") is in Details and the countdown is the banner button's. Rule for the ticket's wording if
  you prefer it; it is one string.
- **Each keepalive re-arms the 5 s notice to 2×.** At 1× the notice and the next keepalive land in
  the same instant and "Still waiting…" would flicker every 5 s; at 2× it speaks only once the
  keepalives have stopped for a full interval — the slot has come and the model is at work.
- **The comment is written as its own block (`: queued\n\n`)** so the client sees it as it arrives;
  a comment line alone would sit in the parser's buffer until the first data event.
- **The keepalive interval is asserted by value and shortened through a gateway field** (as every
  deadline is in this suite) rather than a 6 s wait per test.

### Bought beyond the ticket (declare loudly)

- **The host's name in in-stream and head error copy** (`api.ts`, 3 lines): `rawChatEvents` never
  received `hostName`, so every `{host}` sentence from a request error read "your host" — 014's
  "Max's laptop allows a set number of messages…" was in fact "your host allows…". Found because
  "{host} is busy" needed the name; fixed on the way through since the copy is the promise.
- **`web/dev/busy-check.mjs`** (189 lines of dev harness) and three real-stack screenshots.

### Candidates (not fixed here)

- `Retry-After` is not exposed to the app for the *in-stream* error's sibling, the non-stream 503,
  only in dev CORS — unchanged, noted for completeness.
- The harness's holder needs two keys because this engine's 4 096 context caps one reply at ~24 s;
  a `--queue-timeout` would make the timeout scenario cheap, but that flag was retired by design.

## Freeze

- **Base:** `64fb2f1` (public `main`; `git fetch origin` at freeze time showed it unchanged, rebase a
  no-op). **Lane:** `t018-busy-host`, pushed, not merged.
- **Patch SHA-256:** of `git diff origin/main...HEAD --binary` at the freeze commit, reported with the
  freeze message (recording it here would change it).

| Bucket | Budget | Measured (raw added / deleted) | Verdict |
|---|---|---|---|
| Go source (`internal/gateway/{errors,gateway,proxy,queue,request}.go`) | part of ≤400 | +94 / −26 = **120** | inside |
| TS source (`web/src/{api,stream,storage}.ts`, `ui/Message.tsx`) | part of ≤400 | +76 / −27 = **103** | inside |
| **Source total** | **≤400** | **223** | **inside** |
| Go tests (`busy_test.go`, new) | — | +269 | 5 tests, 6 subtests |
| Web tests (`api`, `stream`, `session` `.test.ts`) | — | +156 / −16 | 190 total (+9) |
| Dev harness (`web/dev/busy-check.mjs`, new) | not budgeted | +189 | real-stack runner |
| Screenshots | ≤500 KB each | 3 files, 40 / 44 / 52 KB | inside |
| Ticket record | — | Log + Report | — |
| Dependencies | none | **0 added**, lockfile unchanged | — |

**Concepts: 0 budgeted, 0 used.** No new code, flag, config key, route, error code or state file.
Named by the ticket and not counted: the `: queued` comment, the `queued` pending reason
(`StreamEvent`/`Message.queued`), `retry_after` inside a stream error event. Also not counted:
`defaultQueuedEvery` (a constant beside the deadlines), `SSEBlock` (the parser's element type),
`streamHead` (a helper shared by two callers). Rule against me on any of these.
