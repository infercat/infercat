# DESIGN — architecture review and debt inventory after demo-1 (ticket 008)

Base: `main` at `5456e59` (= `ed3033f`, where 006 landed, plus a HANDOFF-only commit). 001–006 are
on main; every `file:line` below is `main` at that commit. Ticket 007 is cited from its uncommitted
working tree as `007-wt:`. Every design claim names the code it replaces and the review finding it
prevents; the review sources are the adversarial summaries appended to tickets 001–003, ticket 006's
binding and report, and the second-model JSON (four lenses, 39 findings) — cited as `[001-adv]`,
`[002-adv]`, `[003-adv]`, `[2nd:lens#n]`.

This document is a design, not code. Nothing here is scheduled; §6 proposes tickets for the PM to
price. Where the ticket's Background opinion is wrong, §0.3 says so.

---

## 0. Summary

### 0.1 Four causes, ~30 defects

The founder's ruling holds up under the code. The confirmed defects cluster by missing structure, not
by missing check:

| Cause | Defects it explains | Missing structure | Section |
|---|---|---|---|
| Gateway admitted work in the wrong order and released resources at scattered sites | body + `/tokenize` before admission, unmetered `/v1/models`, unbounded waiting set, no read/write deadline, stalled client pins slots, TPM/daily overshoot, aborted non-stream free, audit `key_id` empty, upstream 4xx as 502, alias bypass [002-adv, 2nd:system-seam#1, 2nd:launch-day#1] | a request record with one exit (**landed in 006**, `internal/gateway/request.go`); still implicit: the charge rule, what "counts", a queue that is a queue, deadlines tied to a failing party | §1 |
| Web client has no session state, only flags and effects | stale path pill, revoked swallowed, leaked transport on retry, two tabs, models never re-read, `/me` errors dropped [2nd: 9 findings across three lenses] | session reducer (state + events) with one effect that enacts I/O and closes what a transition drops | §2 |
| Web client has no message terminal state | mid-stream error looks complete, EOF without `[DONE]` looks complete, reasoning-only reply, stop-while-thinking, refresh loses the turn, interrupted turns re-sent as context [2nd: 5 findings] | message lifecycle: event source → reducer → `complete/stopped/interrupted/no_answer` | §2 |
| Host engine state is a bool and a hidden flag | engine down at start stored as Generic forever (patched by a flag in 10g), slots pinned at 1 (patched by a `SetSlots` push in 10d), `countTimeout` vs `probeTimeout`, redirect guard in the wrong package | explicit engine state (`Unknown → identified × healthy/unhealthy`) read at decision time; a narrower seam | §3 |

Everything that is *not* in those four clusters — invite parsing, tunnel exposure, key hashing, the
streaming pipe, the HTTP/1.1-over-Conn client — the reviews found sound. §7 lists it so nobody
"improves" it.

### 0.2 Before / after public launch

**Must change before public launch** (strangers will hold invites; the standard is "a stranger says
wow"):
1. §1.4 the settle table — one place decides what a request is charged and whether it counted;
   with it the two backlog holes (`TPM/daily checked not reserved`, `aborted non-stream charged
   zero`) close as rows, not patches. Decision 2 in the Report.
2. §1.5 the FIFO slot queue — Protection 4 with exact `status` numbers, no over-admission on
   resize, and the deletion of `SetSlots` (the queue reads the engine state).
3. §1.6 deadline ownership: an absolute 300 s cap on a stream cuts a slow public host mid-answer;
   replace it with a first-byte bound and an idle bound. Decision 1.
4. 007 lands (session + message machines). §2 is the specification 007 should be judged against.
5. §3.2 the engine `Unknown` state, so `status` and `/me` never claim a kind nobody has seen.

**Change after launch** (debt that costs nothing while it waits): the CLI parser and platform seam
(§4 items 1, 9–11), the store throttle (§4 item 4), the daily-counter seed (§4 item 5), the flag and
config-key trims (§5), events for GET routes (§4 item 14), `/v1/models` folded into the stage table
(§1.3 item 5). Before any public mirror of the repo: the 28 MB binary in history (`pm/HANDOFF.md`)
needs a rewrite — a repo action, not a design.

### 0.3 Verdict on the ticket's working opinion

- **"gateway = explicit pipeline with a request record."** Right, and 006 landed it:
  `request` (`request.go:31-61`) owns every resource, `proxy` (`request.go:95-113`) is the stage
  order top to bottom, `finish` (`request.go:303-337`) is the one exit, and
  `TestPanicLeavesThroughFinish` proves it. What the structure still leaves implicit is not the
  record but two decisions hidden in booleans (`queued`, `wroteHeader && 2xx`) and one primitive
  that is not what its name says (a channel "queue"). §1.3 lists seven such places.
- **"web = session reducer owning the transport."** Half right. A reducer that owns I/O is not pure
  and not testable; 007's reading — the reducer decides, one effect enacts and closes what was
  dropped — is the correct form (§2.2). And three of the ruling's seven states (`loadingWasm`,
  `connecting`, `verifying`) are one state with a `step` field: they have identical exits.
- **"host = upstream as a state with observers."** State yes, observers no. Every consumer can read
  the state at the moment it decides (the slot cap at admission, the context at normalization);
  the one push that exists (`SetSlots`, `gateway.go:204-216` + `serve.go:196-223`) should be
  deleted, not generalized into a subscription API (§3.3).
- **"delete `reorder()` in favour of a parser that accepts flags after positionals."** Right; the
  parser is the standard library's own, called in a loop, no dependency (§4 item 1).

---

## 1. Gateway request pipeline

### 1.1 What landed (006, `ed3033f`)

`serveHTTP` (`gateway.go:251-267`) arms a body read deadline when a body is coming, answers
`/healthz`, then builds a `request` and defers `finish`. `serve` (`request.go:71-89`) authenticates
and routes; a proxied POST runs the stage list (`request.go:95-113`):

```
checkHealth → admitKey → readBody → normalize → count → checkBudgets → acquireSlot → callUpstream → relay
```

Each stage takes what it needs onto the record — `admitted`, `buffered`, `reserved`, `releaseSlot`,
`cancelUpstream`, `resp` (`request.go:47-54`) — and returns the first error; `finish`
(`request.go:303-337`) releases in reverse order and records the event. The limiter gained
`reserve` (prompt estimate against TPM/daily, `limits.go:150-180`), `abort` (`:200-211`) and a
`release` that settles the reservation (`:184-195`). The denylist is data (`proxy.go:53-66`), the
waiting set is capped (`gateway.go:233-236`), the write deadline is re-armed per line
(`proxy.go:504`), 401s record nothing, paused/revoked events carry `key_id`, redirects are refused
(`proxy.go:363-369`), engine 400/422 map to 400 (`proxy.go:416-422`). 41 gateway tests, `-race`
clean; the live `n_predict` bypass is closed on the real engine (006 Report).

This is the structure the founder asked for. What follows is not a redesign of it; it is the list
of what the structure still leaves implicit, with the fix for each and the invariant a test should
pin.

### 1.2 What it makes explicit — keep

- The stage order as one readable list, each stage one method with one comment. Keep the list; do
  not let stages grow branches (the one `switch q.kind` in `normalize`, `request.go:211-221`, is
  the right amount).
- The record as the only holder of resources; no stage releases anything itself
  (`request.go:25-30`). Keep that rule absolute — §1.3 item 5 is the one place it is broken.
- Reservation settled in `finish`, reservations visible in `/me`'s `tpm_used`/`today_tokens`
  (`limits.go:232`) so the friend's bar and the limiter agree.
- Deadlines armed and cleared at the stage that owns them (`gateway.go:252-257`, `request.go:190`,
  `request.go:369-371`).
- The denylist as a documented slice with a comment that names what is deliberately *not* on it
  (`proxy.go:44-52`).

### 1.3 What it still leaves implicit — and the fix for each

1. **The charge rule lives in three places.** `finish` charges `prompt + completion` iff
   `wroteHeader && Status/100 == 2` (`request.go:316-318`); `pipeStream` substitutes the chunk
   count when no usage arrived (`proxy.go:544-546`); a non-stream response the client abandoned
   ends in `pipeBody`'s error path with `wroteHeader == false` and is charged nothing
   (`proxy.go:441-444`) — the "aborted non-stream charged zero" backlog item is not a missing
   patch, it is the absence of a table. **Fix:** an `outcome` on the record set by the stage that
   ends the request, and one settle table (§1.4) read by `finish`.
2. **"Counts against RPM" is a boolean set at the top of a stage.** `acquireSlot` sets
   `queued = true` before asking for the slot (`request.go:253`), so a request refused on the spot
   because the waiting set is full (`gateway.go:233-236`) counts as one of the friend's requests.
   A friend behind a busy engine who retries every `Retry-After: 5` burns 12 of 20 RPM per minute
   on 503s they never queued for. **Fix:** the settle table says what counts; "refused before
   entering the queue" is a rejection row (not counted), "timed out in the queue" is a counted row
   (the friend held a place).
3. **The queue is a semaphore.** `acquire` (`gateway.go:222-249`) is a buffered channel plus three
   atomics (`inFlight`, `waiting`, `maxWaiting`, `gateway.go:58-63`) plus a generation swap for
   `SetSlots` (`:204-216`). Go picks a random ready sender, so under load a request can lose the
   slot to newer arrivals repeatedly and hit `QueueTimeout` while later ones ran; after `SetSlots(n)`
   the new channel admits `n` while old holders finish, so `inFlight` transiently exceeds the
   engine's slots — the burst Protection 4 forbids; and `Queue()` reports counters kept beside the
   primitive, not read from it. **Fix:** §1.5.
4. **The absolute `RequestTimeout` is still the bound on a stream** (`request.go:271`, 300 s over
   the whole relay). It is the one deadline not tied to a failing party: a healthy engine at 10
   tok/s answering a 4 096-token request is cut at 300 s with an SSE error, which on a public host
   reads as "broken". **Fix:** §1.6 — a first-byte bound and an idle bound, both engine-side, and
   the absolute cap deleted.
5. **`/v1/models` is a second, hand-rolled pipeline.** `models()` (`proxy.go:256-291`) calls
   `admitKey`, sets `queued` itself, builds its own request, sets `cancelUpstream` and `resp` on
   the record by hand. It works because `finish` is generic, but it is the one route where a stage
   is not a stage. **Fix (after launch):** an `endpoint` kind `modelsEndpoint` whose stage list is
   `checkHealth → admitKey → callUpstream(GET) → relayModels`; the stage list becomes a small
   per-kind table instead of a slice literal inside `proxy`.
6. **The reservation is the prompt estimate only** (006 judgment 7: reserving `prompt + max_tokens`
   "would reject a 10-token-TPM key's first request"). So TPM and daily remain ceilings *for
   prompts*; completions overshoot by up to `max_concurrent × max_output_tokens` per window — 10 %
   at the defaults, 100 %+ for a host who sets `--tpm 2000` for a stingy guest. The 10-token key is
   a test fixture, not a product case; the product rule is "nothing is unlimited unless the host
   says so". **Fix:** §1.4 reserves the worst case and shrinks it to fit, the way context already
   shrinks — one blessed pattern applied to a second ceiling. Decision 2.
7. **The slot cap is pushed, not read.** `refreshLoop` diffs `Info().Slots` and calls `SetSlots`
   (`serve.go:216-220`); `SetSlots` re-derives `maxWaiting` (`gateway.go:215`). Two packages carry
   one number. **Fix:** the queue reads `Info().Slots` at acquire/release (§1.5, §3.3); `SetSlots`
   and the `gatewayServer.SetSlots` method (`main.go:56`) are deleted.

Two smaller ones: `countTimeout` 10 s (`proxy.go:25`) wraps a 3 s inner timeout for tokenize
(`client.go:19, 182`) and is live only for the model list (§4 item 8); and `abort` removes "the
newest `req` entry" (`limits.go:205-210`) — exact in count, wrong in identity when
`max_concurrent > 1`. A `seq` on the entry (set by `admit`, held on the record) removes exactly
its own.

### 1.4 The settle table

Add to the record:

```go
type outcome uint8

const (
    outcomeNone      outcome = iota // still running
    outcomeRejected                 // refused before the queue: 4xx/5xx from stages 0–6, or the waiting set was full
    outcomeQueueLost                // client gone or QueueTimeout while holding a place in the queue
    outcomeEngineErr                // dial failed, or a non-2xx from the engine
    outcomeServed                   // 2xx relayed to the end (usage object seen or not)
    outcomeCut                      // 2xx started, then: client stopped reading, client gone, engine idle/error mid-stream
)
```

and to the limiter one call, `settle(a *admission, counted bool, charged int)`, replacing
`release` + `abort`. `finish` reads the table:

| `outcome` | counted against RPM | charged (TPM + daily) |
|---|---|---|
| Rejected | no — the admission entry (by `seq`) is removed | 0; reservation released |
| QueueLost | **timed out:** yes (a place was held) · **client gone:** no | 0 |
| EngineErr | yes | 0 |
| Served, usage object seen | yes | `prompt + completion` as reported |
| Served, no usage object (stream) | yes | pre-check prompt + delta chunks seen (002's blessed deviation) |
| Cut, stream | yes | pre-check prompt + delta chunks seen |
| Cut, non-stream (client gone before the body was relayed) | yes | the reservation — the engine did the work; today `proxy.go:441-444` charges 0 |

**The reservation** at `checkBudgets` becomes `prompt + max_tokens` (the request's worst case),
with the same shrink-to-fit that context already applies (`proxy.go:211-226`): if
`window_used + prompt + max_tokens > TPM`, set `max_tokens := TPM − window_used − prompt` when that
is ≥ `minOutputTokens` (16), else 429 `rate_limited` with the numbers; the same against the daily
budget. Then reserve exactly `prompt + max_tokens`. Settle corrects `today` and the window entry to
the charged amount. Effects: (a) `Σ live reservations + Σ settled charges` in the window never
exceeds TPM — the limit is a ceiling for tokens, not for prompts; (b) the short question at
18 000/20 000 is still admitted, with a 1 900-token cap and `finish_reason: length` if it needed
more; (c) a key whose TPM cannot hold `prompt + 16` is refused — which is the only honest answer
for a 10-token-TPM key. No new concept: the sliding log holds the reservation as an entry; the
shrink is the existing rule applied to a second ceiling.

### 1.5 The slot queue

Replace the channel semaphore (`gateway.go:58-63, 204-249`) with a queue whose cap is read from
the engine state at decision time:

```go
type slotQueue struct {
    mu       sync.Mutex
    cap      func() int        // engine.Info().Slots (override applied), min 1 — read live
    inFlight int
    waiters  []*waiter         // FIFO; a released slot is handed to waiters[0]
}
type waiter struct{ ch chan struct{} }   // closed when a slot is handed over

// acquire: if inFlight < cap() → take; else if len(waiters) ≥ max(2, 2×cap()) → refuse now
// (outcomeRejected); else enqueue and wait ≤ QueueTimeout or ctx (outcomeQueueLost on either).
// On timeout/ctx: remove self; if a slot was handed over in the race, release it (hands it on).
// release: if waiters non-empty and inFlight ≤ cap() → hand over (inFlight unchanged);
// else inFlight--.
```

`Queue()` returns `inFlight, len(waiters)` under the one mutex — exact. A cap decrease drains
naturally (no hand-over while `inFlight > cap`); a cap increase takes effect on the next arrival or
release, which under load is milliseconds away. `SetSlots`, `maxWaiting`, `semMu`, the two atomics
and `waitCap` go; the `--slots` override stays where it is (`upstream.Slotted`, `client.go:23-25`).
`TestSetSlotsResizesTheGlobalQueue` becomes "the queue follows `Info().Slots` on the next acquire".

### 1.6 Deadlines — one owner each

Each deadline bounds one party's failure. No deadline is a general "request timeout".

| Deadline | Bounds whom | Owner | Default | Today |
|---|---|---|---|---|
| header read | a client that never finishes its request line | `http.Server.ReadHeaderTimeout` | 30 s | `gateway.go:151` |
| body read | a client that stalls its body (valid key or not) | armed at entry, cleared at `readBody` | 30 s | `gateway.go:252-257`, `request.go:190` |
| queue wait | an engine that is full | `acquireSlot` | 30 s | `gateway.go:238` (`QueueTimeout`) |
| engine first byte | an engine that accepted the request but does not start (its own queue) | `callUpstream`: `ResponseHeaderTimeout` on the engine transport | 120 s | none — `client.go:59` sets it to 0 and the absolute cap stands in |
| engine idle | an engine that stalls mid-stream | `relay`: `time.AfterFunc(idle, cancelUpstream)` reset per line | 60 s | none |
| client write | a client that stops reading | `relay`: write deadline re-armed per line | 60 s | `request.go:369-371`, `proxy.go:504` |
| idle connection | a keep-alive client between requests | `http.Server.IdleTimeout` | 2 min | `gateway.go:152` |
| auxiliary engine call | tokenize / model list | the engine package | 3 s | `client.go:19`; `proxy.go:25` `countTimeout` is dead for tokenize |

Deleted: the absolute `RequestTimeout` (`request.go:271`; `Config.RequestTimeout`,
`gateway.go:30`; the `serve --request-timeout` flag and `request_timeout` config key,
`serve.go:46`, `config.go:24`). With first-byte, idle, and write deadlines in place nothing a
stalled party can do escapes a bound, and a slow-but-live stream is never cut. This changes
`docs/ARCHITECTURE.md` §Concurrency & queue (**decision 1**).

### 1.7 Normalization — done; pin it

006's `normalize` (`request.go:202-226`) is the one place the friend's JSON becomes the engine's,
with shrink-to-fit in `checkBudgets` because it needs the count (006 judgment 2 — correct). Two
follow-ups only:

- Return a value, not side effects on `q`: `normalized{body, model, stream, text, maxTok,
  stripped}` so `count`, `checkBudgets` and `callUpstream` read fields instead of re-inspecting the
  map (`clampMaxTokens` and `fitContext` both walk `max_tokens`/`max_completion_tokens`,
  `proxy.go:179, 232`).
- One post-condition test over a fixture table (every denylisted key, every cap spelling, absent
  model, disallowed model, prompt over/at/under the context): after `normalize` + `checkBudgets`,
  no denylisted key is present; `max_tokens` is present, numeric, `≤ key cap`, `≤ eff − prompt`
  when `eff > 0`, and (§1.4) `≤ TPM − used − prompt`; `model` present and allowed; `include_usage`
  iff `stream`; every other field byte-identical (`json.Number`). Today the same facts are spread
  over `TestOverrideKeysStripped`, `TestMaxTokensClampAndPassthrough`,
  `TestContextTooLongBoundary`, `TestModelAllowlistAndFill`, `TestIncludeUsageInjectionKeepsOtherOptions`.

### 1.8 Invariants — which exist, which are missing

| # | Invariant | Status |
|---|---|---|
| I1 | **Exactly-once release.** For every way a request can end (14 codes, success, stop, client gone while queued, waiting set full, stalled body, stalled reader, engine idle, panic): after the handler returns `bodies == 0`, `inFlight == 0`, `len(waiters) == 0`, every key's `inFlight == 0`, live reservations 0. | partial: `TestPanicLeavesThroughFinish`, `TestStalledReaderFreesSlots`, `TestPreQueueRejectionIsUncounted`, `TestBodyReadDeadline` are rows; make it one table over every exit |
| I2 | One event per authenticated request, none per 401; `key_id` set whenever a key resolved. | exists: `TestAuditTruth`, `TestAuthCodes` |
| I3 | Headers once: after `wroteHeader` a failure is an SSE event or nothing. | implicit in `TestStreamUpstreamDiesMidway`; assert it directly |
| I4 | Admission order: no body byte read and no engine call before `admitKey`; `bodies ≤ Σ max_concurrent`. | exists: `TestAdmissionBeforeBody` |
| I5 | **Ceiling.** Under any interleaving, `Σ reservations + Σ charges` in the window ≤ TPM and `today` ≤ daily. | missing (holds for prompts only; §1.4) |
| I6 | Charge and count exactly as the §1.4 table, one test row per outcome. | missing as a table; rows scattered in `TestTPMAndDailyAfterRealUsage`, `TestStreamWithoutUsageFallsBackToChunkCount`, `TestClientDisconnectCancelsUpstream` |
| I7 | **FIFO and exact resize.** With `cap = 1`, A, B, C queued in order run in order; with `cap` 2 → 1 mid-run, `inFlight` never exceeds 2 and reaches 1 without a new admission; `Queue()` equals the queue. | missing (§1.5) |
| I8 | **Deadlines.** Stalled reader freed within the write deadline; stalled body within the read deadline; engine that sends headers but no bytes within the idle deadline; engine that never sends headers within the first-byte deadline; a live slow stream (one byte every 10 s for 10 minutes) is never cut. | partial: first two exist; the last three are §1.6 |
| I9 | Normalization post-conditions (§1.7). | partial, five tests |
| I10 | Secrets never in logs, bodies, events, or the engine request. | exists: `TestMain` gate |

### 1.9 In one sentence

006 built the pipeline; what remains is to move three decisions out of booleans into one table
(§1.4), replace the one primitive that is not what it claims (§1.5), tie every deadline to a
failing party (§1.6), and write the I1/I5/I6/I7/I8 tables that make the structure checkable.

---

## 2. Web session and message state machines

### 2.1 What is there

`App` holds `live: Live | null` (`App.tsx:11-17, 20`). `Connect` keeps `stage`, `failure`,
`formatError`, `remembered`, a `stageRef` and a `once` ref (`Connect.tsx:30-37`) and runs the
connect sequence inside one `try` whose `catch` cannot reach the transport it opened
(`Connect.tsx:66-88` → `[2nd:web-transport#3, web-ui-state#5, launch-day#9]`). `Chat` keeps `me`,
`path`, `models`, `streaming`, `error`, `retryIn` and seven effects: ping every 30 s swallowing
failure (`Chat.tsx:60-73` → stale pill `[2nd:×3]`), models once (`:75-79` → LM Studio never
re-read `[2nd:launch-day#6]`), save-when-not-streaming (`:82-84` → refresh loses the turn
`[2nd:launch-day#7]`), a countdown that counts ticks (`:87-100` → `[2nd:web-ui-state#9]`),
`refreshMe` swallowing every error (`:108-112` → revoked mid-session ignored `[2nd:×2]`). A reply
is a `Message` with `error?: string` (`storage.ts:4-14`); a failure is marked only when content is
empty (`Chat.tsx:155-169` → truncated reply looks complete `[2nd:×3]`); `streamChat` resolves on
EOF and ignores `{"error":…}` events (`api.ts:118-138` → `[2nd:×3]`).

007 (`007-wt`) has already turned the parser into an event source (`api.ts:109-187 chatEvents`,
`StreamEvent`), added `MessageStatus` and `isAnswer` (`storage.ts:10-15`), the host scope
(`storage.ts:56-82`), and recorded the PM's design ruling (session reducer, message reducer, one
scope function). What follows is the specification those should meet.

### 2.2 Session machine

**States** — four, one of them carrying a step:

```ts
type Session =
  | { s: 'idle'; notice?: string }                              // the connect screen; notice says why we are here
  | { s: 'connecting'; attempt: number; mode: 'direct' | 'tunnel';
      step: 'wasm' | 'relay' | 'handshake' | 'verify'; pct?: number;
      transport?: Transport }                                   // present from 'handshake' on: the thing to close on exit
  | { s: 'connected'; attempt: number; transport: Transport; secret: string; scope: string;
      me: Me; path: Path; engine: 'ok' | 'offline' }
  | { s: 'degraded'; /* the same fields as connected */ reason: 'path' | 'engine'; since: number };

type Path =
  | { kind: 'measured'; rttMs: number; via: string; at: number }
  | { kind: 'unknown'; since: number; last?: { rttMs: number; via: string; at: number } };
```

`degraded` is the PM's ruling and 007's reading (ii) — same payload as `connected` plus a reason —
so the chat screen renders from either. This document would have made it a field (`path.kind ===
'unknown' || engine === 'offline'`), because every transition out of `degraded` is a transition out
of `connected` too; if 007 lands it as a state, keep it and do not add a second one (`reconnecting`
is `connecting` with a `notice`). The ruling's `loadingWasm/connecting/verifying` are `step` values,
not states: they share every exit (fail → `idle(notice)`, cancel → `idle`, superseded → ignored).

**Events**

| Event | From | Carries |
|---|---|---|
| `CONNECT` | the form | invite text, mode |
| `STEP` | the connect effect | step, pct, and the transport once it exists |
| `SESSION_UP` | the connect effect | transport, path, privateKeyJSON |
| `ME_OK` | connect effect, periodic `/me`, post-request `/me` | `Me` |
| `ME_ERROR` | same | `FriendlyError` (fatal or not) |
| `PING_OK` / `PING_FAIL` | the ping effect | rtt, via |
| `REPLY_ENDED` | the message reducer | terminal status, error code if any |
| `DISCONNECT` | the sidebar, or any fatal error | reason |
| `FORGET` | the connect screen | — |
| `CANCEL` | the connect screen while connecting | — |

**Transitions** (anything not listed is ignored; an event carrying an `attempt` that is not the
current one is ignored — that is the "superseded attempt" guard, 007 promise 3):

| State | Event | Next |
|---|---|---|
| idle | CONNECT | connecting(attempt+1, step wasm or verify for direct) |
| connecting | STEP | connecting(step, pct, transport) |
| connecting | SESSION_UP | connecting(step verify, transport) |
| connecting | ME_OK | connected(path from SESSION_UP, engine from `me.host.upstream.healthy`, scope = hostScope(addr, me.key.id)) |
| connecting | ME_ERROR / CANCEL | idle(notice = error copy for the step it failed at) |
| connected | PING_OK | connected(path measured) — or from degraded(path) → connected |
| connected | PING_FAIL | degraded(reason path, path.kind unknown, last = previous measured) |
| connected / degraded | ME_OK with `healthy=false` | degraded(reason engine) |
| degraded(engine) | ME_OK with `healthy=true` | connected |
| connected / degraded | ME_ERROR fatal (`invalid_key`, `key_paused`, `key_revoked`) | idle(notice) |
| connected / degraded | ME_ERROR transient | unchanged (last `me` kept) |
| connected / degraded | REPLY_ENDED with a fatal code | idle(notice) |
| connected / degraded | REPLY_ENDED interrupted by a connection error | degraded(reason path) until the next PING_OK |
| any | DISCONNECT | idle(notice) |
| idle | FORGET | idle (storage: invite + privateKey removed) |

**Effects** — exactly one `useEffect` in `App`, keyed on `(s, attempt)`:

- `connecting` → run `openTransport` → `getMe`, dispatching `STEP/SESSION_UP/ME_OK/ME_ERROR` tagged
  with `attempt`.
- `connected | degraded` → start the ping interval (30 s) and the `/me` interval (60 s; 007 promise
  4 — this is the one `/me` poll; the post-request refresh dispatches into the same reducer path).
- **Every transition:** if `prev.transport && prev.transport !== next.transport` → `prev.transport.close()`.
  This one line is the structural form of "no leaked sessions" (007 promise 3): the reducer decides
  which transport is current; the effect closes whatever stopped being current — on `/me` failure,
  on unmount, on a superseded attempt, on disconnect.

**Which surface renders which state**

| Surface | Reads |
|---|---|
| Connect screen | `idle.notice`, `connecting.step/pct` (the step list); format errors are local form state |
| Header pill | `path`: measured → `relayed via nyc · 64 ms`; unknown → `path unknown · last 64 ms 2 min ago`; `engine === 'offline'` → `engine offline` next to the host name |
| Empty state / composer | `engine` and `me.host.models` (empty → "waiting for a model", composer disabled, models re-read by the `/me` interval) |
| Privacy line | `me.host.log_prompts` (007 promise 12; the field landed in 006, `proxy.go:317`) |
| Banner | the current reply's terminal error (§2.3) with `retryAt` |

**Storage keys** (007 already has these; requirement: one scope function):

| Key | Scope | Holds |
|---|---|---|
| `bn.invite` | global | the last invite text (identity of *this browser's* last host) |
| `bn.privateKey` | global | the tunnel identity; written only by the tab holding the Web Lock `bn.identity` (007 promise 10) |
| `bn.conversations.<scope>` | `scope = hostScope(addr, key.id)` | that host's conversations |
| `bn.settings.<scope>` | same | model, system prompt, temperature for that host |

`hostScope` is a hash of `addr + key.id` — public values, never the secret. A different host or a
rotated key starts empty; a stored model not in `me.host.models` resets (007 `modelFor`).

### 2.3 Message lifecycle

**Event source** — 007's `chatEvents` (`007-wt:api.ts:125-187`): `reasoning(text) | content(text) |
usage(in,out) | done | eof | aborted | error(code, FriendlyError)`. Nothing throws; the last event is
always one of `done | eof | aborted | error`. Keep it exactly.

**Reducer** — pure, one file, unit-tested:

```ts
type Reply = { content: string; reasoning: string; tokens?: {in, out};
               status: 'streaming' | 'complete' | 'stopped' | 'interrupted' | 'no_answer'; note?: string };

reduceReply(r, ev):
  reasoning → r.reasoning += text
  content   → r.content += text
  usage     → r.tokens = {in, out}
  done      → status = r.content !== '' ? 'complete' : 'no_answer'   (note: 'The model finished without an answer' / 'only thought')
  eof       → status = 'interrupted' (note: 'The connection dropped before the reply finished')
  aborted   → status = 'stopped'     (note when content === '': 'Stopped while thinking' or 'Stopped before the first token')
  error     → status = 'interrupted' (note: error.title; the FriendlyError also goes to the banner and, if fatal, to the session as REPLY_ENDED)
```

**Surfaces**

| Surface | Reads |
|---|---|
| Assistant row | `content`, `reasoning`; a one-line `note` under the message for every status but `complete` and `streaming` (007 promise 1: not a banner) |
| Thinking block | open while `status === 'streaming' && content === ''`; collapses when content starts or at any terminal status |
| Stop / Send buttons | the current reply's `status === 'streaming'` (replaces the `streaming` boolean, `Chat.tsx:42`) |
| Next request's context | `isAnswer(m)` (007): user turns and `complete`/`stopped` replies; never `interrupted` or `no_answer` unless the user regenerates from them |
| Banner | `{error, retryAt}`; `remaining = max(0, ceil((retryAt − now)/1000))` recomputed on a 1 s tick and on `visibilitychange` — the tick reads the clock, it never counts (007 promise 8) |

**Persistence** — with a reducer, persistence becomes rules instead of a `streaming` guard
(`Chat.tsx:82-84`): save on the user turn before any I/O; checkpoint the streaming reply at most
every 2 s; save on every terminal transition; on load, any reply still `streaming` becomes
`interrupted` with note "the page was reloaded". This closes `[2nd:launch-day#7]`, which 007 lists
as backlog "if it falls out cheaply" — it does, once the reducer exists.

### 2.4 Invariants (each is a vitest)

- **W1 At most one live transport.** Over a scripted sequence — connect, `/me` fails, retry, a
  superseding connect while the first is mid-handshake, disconnect, unmount — the fake bridge's
  open-session count is ≤ 1 at every step and 0 at the end, and every session opened was closed
  exactly once.
- **W2 No swallowed fatal.** Every source of a fatal code (`/me` poll, post-request `/me`, stream
  error, `/v1/models`) ends in `idle(notice)`; every transient error leaves `me` unchanged.
- **W3 The pill never shows a measurement without its age.** `describePath(path)` for
  `kind: 'unknown'` includes "last … ago"; for `measured` older than 2 intervals it also shows age.
- **W4 Every reply reaches exactly one terminal status**, for each of the seven event-source
  endings (done with content, done without, eof, aborted with/without content, error
  transient, error fatal), and only `complete`/`stopped` replies re-enter context.
- **W5 Attempt guard.** Events tagged with a stale `attempt` change nothing.
- **W6 Storage scope.** Two hosts, or one host with two keys, never see each other's conversations
  or settings; the legacy global keys are cleared once.
- **W7 Reload mid-stream** yields the user turn plus an `interrupted` reply, never a lost turn.

### 2.5 With 007

Agreements: the event source, `MessageStatus`, `isAnswer`, `hostScope`, "the reducer decides, one
effect enacts" (007 Log 11:24 (i)), `degraded` carrying the live payload (ii). Two places where
this document asks for one step more, neither of which changes what 007 has written so far:

1. Fold the ruling's `loadingWasm/connecting/verifying` into `connecting.step` (§2.2). Fewer
   transitions to test; the connect screen already renders a step list, not a state name.
2. Persistence rules (§2.3) instead of the streaming guard, so promise-13's backlog item closes
   with the reducer rather than later.

---

## 3. Host upstream as a state

### 3.1 What is there

`client` (`client.go:28-38`) holds `info Info{Kind, URL, Healthy, ModelContext, Slots, Models}`,
`setSlots` (the `--slots` override) and `sniffed bool` (005 fix 10g). `Open` guesses `Generic`
when nothing answers and relies on `sniffed = false` to re-sniff later (`client.go:102-113`,
`kinds.go:79-86`); `defaultSlots` fakes a slot count per kind (`client.go:73-78`). `Refresh`
replaces `info` on success and flips `Healthy` on failure keeping the rest (`kinds.go:75-109`).
`serve` polls every 10 s, logs transitions, and pushes slot changes into the gateway
(`serve.go:196-223`). The gateway reads `Info().Healthy` per request (`request.go:150`),
`Info().Models` for model fill (`proxy.go:155`), `Info().ModelContext` for fit (`proxy.go:212`),
and builds its own `http.Client` from `BaseURL()` + `Transport()` with the redirect guard
(`proxy.go:353-369`).

Two things leak: the "we have not seen this engine" state is a hidden bool plus a guessed kind, so
`status` and the startup banner print `openai-compatible … slots 1` for an engine nobody has met;
and the gateway holds pieces of the engine (URL, transport, redirect policy, two timeouts) that
belong to the engine.

### 3.2 The state

```go
type Health struct {
    OK    bool
    Since time.Time   // when OK last changed
    Err   string      // last probe error while !OK; "" while OK
}

type Info struct {
    URL          string
    Kind         Kind       // Unknown until an engine answered a signature probe; Generic only when one answered /v1/models without a signature
    Health       Health
    ModelContext int        // last known; 0 while Unknown or unreported
    Slots        int        // last known, override applied; 1 while Unknown
    Models       []string   // last known
    ProbedAt     time.Time
}
```

Transitions, all inside `internal/upstream` and all through one `probe(ctx)`:

| From | Probe result | To |
|---|---|---|
| `Open(url)` / a `Detect` candidate | — | `Unknown`, `Health{OK:false, Since: now, Err: "not probed yet"}`; then `probe` |
| `Unknown` | signature answers | `identified(kind)`, `OK`, context/slots/models filled |
| `Unknown` | nothing answers | `Unknown`, `!OK` (Err updated; Since unchanged) |
| `identified` | refresh succeeds | `identified`, `OK`, fields replaced |
| `identified` | refresh fails | `identified`, `!OK` (fields kept: `/me` keeps telling the truth about what it knew) |

`Unknown` replaces `sniffed` (one constant instead of a flag; `defaultSlots` goes away — slots are
1 until an engine says otherwise, and vLLM's "2 unless `--slots`" becomes the override's default
applied at identification). `Detect` returns the first candidate that reaches `OK`. The probe
interval stays a `serve` loop (the process owns its goroutines) but the loop only calls
`up.Refresh` and logs `Health` changes — nine lines, no slot plumbing.

Surfaces: the startup banner and `status` print `upstream  (not identified yet)  http://…  NOT
ANSWERING for 12s` while `Unknown`; `/me.host.upstream.kind` is `"unknown"`; the web empty state
already renders `healthy=false` as engine offline (§2.2).

### 3.3 Readers, not observers

Every consumer reads `Info()` at the moment it decides:

| Reader | Reads | When |
|---|---|---|
| `checkHealth` | `Health.OK` | per request |
| `normalize` / `checkBudgets` | `Models`, `ModelContext`, `Kind` (tokenizer) | per request |
| the slot queue (§1.5) | `Slots` | per acquire/release |
| `/me`, admin `status`, startup banner | all | per call |
| `serve` refresh loop | `Health` | to log a transition |

`Info()` is a copy under an `RLock` (`client.go:83-89`) — one allocation for the models slice;
at request rate this is nothing. No subscription API, no callbacks, no `SetSlots`. The only
"observer" in the system is a log line.

### 3.4 The interface the gateway depends on

```go
// internal/upstream — what the gateway is allowed to know about the engine.
type Engine interface {
    Info() Info
    CountTokens(ctx context.Context, text string) (n int, exact bool, err error)
    // Do sends one request to the engine: bearer added, base URL private, redirects never
    // followed, ResponseHeaderTimeout = first-byte deadline (§1.6). The caller owns resp.Body.
    Do(ctx context.Context, method, path string, body []byte, stream bool) (*http.Response, error)
}
```

Removed from the gateway's view: `BaseURL()`, `Transport()`, `Refresh()` (the CLI's concern),
`upstreamURL` and `doUpstream` (`proxy.go:353-369`), `countTimeout` (`proxy.go:25`), and the
redirect guard (which moves into the one `http.Client` the engine owns). Protection 1 gains a
structural form: the engine's address never crosses the seam, so it cannot end up in a
friend-facing message by accident. `Slotted` stays a CLI-side interface for `--slots`.

### 3.5 Invariants

- **E1** `Kind == Unknown ⇒ Slots == 1 && ModelContext == 0 && len(Models) == 0`.
- **E2** A refresh failure never changes `Kind`, `ModelContext`, `Slots`, `Models`; it only flips
  `Health.OK` and sets `Err` (exists: `TestRefreshKeepsTheLastGoodInfoWhenTheEngineGoesAway`).
- **E3** An engine down at `Open` and up at the next probe is identified with its real kind and
  slots, and the gateway's queue cap follows on the next acquire with no call from `serve`
  (replaces `TestServeRoutesTunnelLogAndFollowsSlots`'s `SetSlots` assertion).
- **E4** No `*url.URL` and no `http.RoundTripper` of the engine is reachable from
  `internal/gateway` (compile-time: the gateway imports only `Engine`).
- **E5** `Do` never follows a redirect and never sends the friend's `Authorization` (exists:
  `TestUpstreamRedirectNotFollowed`; moves package).

---

## 4. Debt inventory

Disposition: **keep** (sound, leave it), **redesign → §n** (replaced by a structure above),
**delete**. When: **before** public launch / **after**.

| # | Item | Where | Why it exists | Disposition | When |
|---|---|---|---|---|---|
| 1 | `reorder()` — a second flag grammar (bool detection, `=value`, dangling values, `--` synthesis) | `main.go:198-234`, tests `TestFlagsAfterPositionals`, `TestDanglingValueFlagIsAnError` | stdlib `flag` stops at the first positional; 003 found it, 005 fixed a bug in it (10h) | **delete**; replace with the interspersed loop: `for { fs.Parse(args); if fs.NArg()==0 \|\| fs.Arg(0)=="--" {break}; pos = append(pos, fs.Arg(0)); args = fs.Args()[1:] }` — stdlib parses every flag, so the 10h class (dangling value) is stdlib's error, not ours. Keep both tests. | after |
| 2 | The charge rule in three places; `queued` set before the queue is entered | `request.go:253, 316-318`, `proxy.go:441-444, 544-546` | 006 kept 002's charging semantics inside the new structure | **redesign → §1.4** `outcome` + settle table | before |
| 3 | Ad hoc effects and flags: ping/models/save/retry/refreshMe effects, `streaming`, `error`, `retryIn`, `stageRef`, `once`, `Live` | `Chat.tsx:42-46, 60-112`, `Connect.tsx:36-37, 66-88`, `App.tsx:11-17` | 004 built the UI before any failure had been seen | **redesign → §2** (007 in flight) | before |
| 4 | `keys.json` mtime throttle (≤1 stat/s) plus the miss-path forced reload (10a) | `store.go:72-76, 115-128`, `now` clock injection `:55` | 003 feared a stat per request; 005 added the bypass when the throttle bit | **delete the throttle**: stat on every `Lookup`/`List` (a stat is microseconds; 20 rpm × N keys is nothing), keep the stamp compare. Removes `lastCheck`, the fake clock, the miss-path branch, and the documented "≤1 s window" caveat (005 Report). | after |
| 5 | Two-lane usage counters: limiter memory (`limits.go:40-48`) vs `usage.jsonl`; `keys list` scans the whole file for last-seen (`keys.go:166-170`); daily budget resets on restart (002 known limitation, `limits.go:12-13`) | `limits.go`, `usage/aggregate.go`, `keys.go` | the contract gave counters no persistence | **keep both lanes** (live limits vs history are different questions) **and seed** `today` per key from `usage.jsonl` since UTC midnight at gateway start — one `Aggregate` call, no new file. Closes the restart hole. | after |
| 6 | `SetSlots` generation swap + `maxWaiting` atomic + `refreshLoop` slot plumbing + `gatewayServer.SetSlots` | `gateway.go:58-63, 204-216`, `serve.go:196-223`, `main.go:56` | 005 fix 10d for an engine down at start; 006 made the waiting cap follow it | **delete → §1.5/§3.3** (the queue reads `Info().Slots`) | before |
| 7 | `sniffed` flag + `Generic` as a guess + `defaultSlots` | `client.go:37, 73-78, 107-111`, `kinds.go:79-86` | 005 fix 10g | **redesign → §3.2** `Unknown` kind | before |
| 8 | `countTimeout` 10 s (dead for tokenize; live for `/v1/models`) vs `probeTimeout` 3 s | `proxy.go:25`, `client.go:19` | two packages each bounding the same call | **delete** `countTimeout` → §3.4 (`Do` owns timeouts) | with §3 |
| 9 | `platform` seam: `tunnelServer`, `tunnelOptions`, `tunnelStatus`, `gatewayServer`, `gatewayOptions`, `realTunnel`, `warn` | `main.go:29-77`, `wire.go` | 003 built blind against 001/002; the stub is gone | **shrink**: keep an injectable tunnel (tests need a relay-free fake, `TestServeRoutesTunnelLogAndFollowsSlots`); delete `gatewayOptions` (use `gateway.Config`), `tunnelOptions`/`tunnelStatus` (use `tunnel.Options`/`tunnel.Status`), `warn` (dead) | after |
| 10 | `retryAfterUpstreamDown = 10` and `refreshEvery = 10s` — one fact in two packages | `gateway.go:38`, `serve.go:21` | comment says "matches" | **fold**: `upstream.ProbeInterval` const used by both | after |
| 11 | `Status().Clients` = open port-80 connections, so a browser between requests counts 0 | `tunnel.go:54-56`, `status.go:42` | tailcat exposes no peer list (001 judgment 2) | **keep the counter, fix the label**: `status` says `N open connections`; "friends seen in the last 5 min" comes from the limiter's `LastSeen` | after |
| 12 | `quiet()` string-prefix filter on tailcat's log | `tunnel.go:115-122` | tailcat logs a NetworkMap dump on the caller's Logf (005 10b) | **keep** (upstream issue candidate) | — |
| 13 | wasm loader: `booting` singleton, `waitForTunnel` 15 s poll, `go.run` promise voided, no asset timeouts | `wasm.ts:17-53, 58-78` | `go.run` never resolves by design | **keep the shape**; 007 promise 11 adds asset timeouts and holds the run promise. Optional later: `main_js.go` calls `window.__infercatReady?.()` so the poll goes away | after |
| 14 | A usage event per `/me` and `/v1/models` (every request + a 60 s poll = ~1.5k lines/day/friend of no tokens) | `request.go:327-336`, `usage.go:15` | 002 promise 8: "one event per request" | **propose**: events for POST routes only; last-seen for `keys list` stays correct via the POST events, live last-seen via the limiter. (006 backlog "keyed rejection events at request rate" is the same lane.) | after (contract line; PM rules) |
| 15 | Second `serve` on Windows overwrites `admin.port`/`admin.token` | `sock_windows.go:20-46` | no unix-socket liveness check on Windows `[2nd:launch-day#4]` | **fix without a new file**: dial the recorded port with the recorded token; 200 → refuse "another host is serving" (same as unix `sock_unix.go:32-39`). Also move `admin.Serve` before `saveConfig`/tunnel start so the guard runs first. | after (Windows is not the demo host) |
| 16 | `InfercatTunnel.stats()` and `serve --verbose` — debug affordances not in the contract | `main_js.go:71-76`, `serve.go:54` | 005 fixes 10l/10b | **keep**; add both to `docs/ARCHITECTURE.md` (PM) | after |
| 17 | `client_closed` — a `Code` that is never on the wire | `errors.go:32`, `request.go:349-351` | usage needs a status for "friend went away" | **keep** as a usage status; list it under the event, not in the error table | — |
| 18 | `hostAddr` asks the admin socket, then the saved key | `keys.go:143-151` | `--ephemeral` makes the saved key wrong while a host runs | **keep** (both are needed exactly because of `--ephemeral`; if §5 cuts the flag, the admin lookup goes with it) | — |
| 19 | `web/dev` fakes duplicate the gateway's error shapes | `fake-backend.ts:45-79` | test harness | **keep** | — |
| 20 | `abort` removes "the newest `req` entry" | `limits.go:205-210` | pre-queue rejections must not count | **redesign → §1.4** `seq` on the entry, held on the record | before |
| 21 | `/v1/models` as a hand-rolled route setting record fields itself | `proxy.go:256-291` | a GET with no body did not fit the POST stage list | **redesign → §1.3 item 5** per-kind stage table | after |

---

## 5. Concept budget check

BELIEFS: invite = address + key; key = person; limits live on the key; no groups, roles, plans. The
code embodies these, and the following. Each row argues for existence or removal.

### 5.1 Product objects and states

| Concept | Values | Verdict |
|---|---|---|
| Invite | `ic1.<addr>.<secret>` | keep (the product) |
| Key | `{id, name, status, secret_hash, created_at, limits}` | keep |
| Key status | `active`, `paused`, `revoked` | keep all three: paused is reversible and the friend's copy differs ("works again when they resume"); revoked keeps history under its id, which delete would not |
| Limits | `rpm`, `tpm`, `daily_tokens`, `max_concurrent`, `max_output_tokens`, `max_context`, `models` | keep six; **`max_context` is the weakest** — with shrink-to-fit its only effect is a per-key prompt ceiling below the engine's (KV memory / TTFT protection). Keep for v1, remove if unused by v1.1 |
| Upstream kinds | `llama.cpp`, `vllm`, `ollama`, `lmstudio`, `openai-compatible` (+ `unknown`, §3.2) | keep; `unknown` replaces the `sniffed` flag (net 0) |
| Engine health | `Health{OK, Since, Err}` | replaces `Healthy bool` (net 0) |
| Request outcome | `rejected`, `queue_lost`, `engine_err`, `served`, `cut` (§1.4) | internal; replaces `queued` + `wroteHeader && 2xx` + three charge sites |
| Web session | `idle`, `connecting(step)`, `connected`, `degraded` (§2.2) | 4 states; replaces `Live \| null` + `ConnectStage` (5 names) + `streaming` + `error` |
| Message status | `streaming`, `complete`, `stopped`, `interrupted`, `no_answer` | replaces `error?: string`; the four terminal values are the four distinct truths the surface must tell |
| Path | `measured`, `unknown(last)` | replaces `PingResult \| null` |

### 5.2 CLI verbs and flags

| Concept | Verdict |
|---|---|
| `serve`, `keys add/list/pause/resume/revoke/rotate/limits`, `status`, `usage`, `version` | keep |
| `--data-dir` (global) | keep |
| `serve --upstream`, `--upstream-key`, `--slots`, `--name`, `--dev-listen` | keep |
| `serve --derpmap-url`, `--region` | keep (self-hosted relay before publicity, `pm/HANDOFF.md`) |
| `serve --log-prompts`, `--verbose` | keep (per-run, never persisted) |
| **`serve --queue-timeout`, `--request-timeout`, `--max-body`** | **delete** (3 flags + 3 config keys): nobody sets them, they were contract defaults; `--request-timeout` is deleted by §1.6 anyway; 30 s / 4 MiB become constants |
| **`serve --ephemeral`** | **cut candidate**: a per-run mode that changes the address so every invite breaks; nothing in the product path needs it (tests use `tunnel.Options.Ephemeral`, which stays). PM rules |
| `keys add/limits --rpm --tpm --max-concurrent --max-output-tokens --max-context --daily-tokens --models` | keep = the limits |
| `usage --key`, `--since` | keep |

### 5.3 Config keys (`config.json`)

`upstream`, `upstream_key`, `slots`, `dev_listen`, `derpmap_url`, `region`, `name` — keep (7).
`queue_timeout`, `request_timeout`, `max_body` — delete with the flags (§5.2).

### 5.4 Files in the data dir

`host.key.json`, `keys.json`, `usage.jsonl`, `config.json`, `admin.sock` (Windows: `admin.port` +
`admin.token`), `tunnel.log` (005 10b) — keep all; no new file is proposed anywhere in this
document (the daily seed reads `usage.jsonl`; the single-instance guard reuses the admin files).

### 5.5 Error codes (wire)

14: `invalid_request`, `not_found`, `invalid_key`, `key_paused`, `key_revoked`,
`model_not_allowed`, `body_too_large`, `context_too_long`, `rate_limited`, `concurrency_limited`,
`budget_exhausted`, `queue_timeout`, `upstream_down`, `upstream_error`. Merge candidates examined:
the three 429s (next steps differ: wait N s / wait for your own reply / come back tomorrow), the two
503s (retry in 5 s / the engine is off), the two 403s (temporary / final), 413 vs 422 (shorten the
message / start a new chat). Each pair has different friend copy in `api.ts:189-214`, so none
merges. `client_closed` is a usage status, not a wire code (§4 item 17). 006's backlog asks about a
408 for stalled bodies and an engine-429 → 503 mapping: both are reuses of existing rows' meaning
(400 "your request did not arrive" is already truthful; engine 429 is `upstream_down` with
Retry-After, an existing code). Verdict: 14, unchanged.

### 5.6 Routes and `/me`

`/healthz`, `/me`, `/v1/models`, `/v1/chat/completions`, `/v1/embeddings`. Embeddings has no
caller in the product (the web client never calls it); under the stage list it costs one `kind`
branch in `normalize` and one test. Keep for OpenAI-compat completeness; if a later change makes it
a burden, cut it before adapting it. `/me` fields: keep all, including `tpm_used` and `in_flight`
that the client does not render (truth surface, cheap); `host.log_prompts` keep.

### 5.7 Leaked or half concepts, flagged

| Leak | Proposed |
|---|---|
| `sniffed` bool (hidden state) | `Unknown` kind (§3.2) |
| `SetSlots` + generation semaphore + `maxWaiting` + `refreshLoop` slot diff | delete (§1.5, §3.3) |
| `queued` bool and `wroteHeader && 2xx` as the charge/count rule | `outcome` + settle table (§1.4) |
| `abort` by "newest entry" | `seq` on the admission (§1.4) |
| `RequestTimeout` as an absolute stream cap | first-byte + idle deadlines (§1.6) |
| `countTimeout` vs `probeTimeout`; `retryAfterUpstreamDown` vs `refreshEvery` | one const each (§4 items 8, 10) |
| `gatewayOptions`/`tunnelOptions`/`tunnelStatus` mirrors, dead `warn` | delete (§4 item 9) |
| `Live`, `ConnectStage`, `streaming`, `error`, `retryIn`, `Message.error` | the two machines (§2) |
| three tuning flags + config keys; `--ephemeral` | delete / cut (§5.2) |
| `stats()`, `--verbose` (undocumented affordances) | keep, document |

Net effect if everything above lands: −3 flags, −3 config keys, −1 interface method (`SetSlots`),
−2 seam methods (`BaseURL`, `Transport`), −4 mirror types, −1 hidden flag, −4 gateway counters;
+1 kind constant (`unknown`), +1 internal enum (`outcome`), +2 web machines that replace ~9 flags.
Fewer things, each with one meaning.

---

## 6. Proposed tickets

Ordered to reduce risk fastest. Sizes are proposals for the PM to price. None is scheduled here.
(BELIEFS' next theme is polish; these are the cleanup lane beside it, and 1–3 are the ones a
public launch should not wait on.)

1. **Gateway settle table, slot queue, deadlines** — size 3, sensitive, **before launch**. On top
   of the landed 006. Binding: `outcome` on the record and one `settle` (§1.4, table verbatim,
   folding `release`/`abort`; `seq` on the admission); reservation = `prompt + max_tokens` shrunk
   to fit TPM/daily (§1.4); FIFO slot queue reading `Info().Slots` (§1.5) and `SetSlots` deleted
   end to end (gateway, `gatewayServer`, `refreshLoop`); first-byte and idle deadlines replace
   `RequestTimeout` (§1.6) with the flag and config key deleted; `normalized` value + the §1.7
   post-condition table. Evidence: the I1 table (every exit × every counter = 0), I5 randomized
   ceiling, I6 one row per outcome, I7 FIFO and resize, I8 the five deadline fixtures including the
   slow-but-live stream. Concept budget 0 (−1 flag, −1 config key, −1 method, +1 internal enum).
   Contract lines changed: §Concurrency & queue, admin queue semantics (exact).

2. **Engine state and seam** — size 2, sensitive, **before launch**. Binding: `Kind == Unknown`
   replaces `sniffed` and `defaultSlots`; `Health{OK, Since, Err}` replaces `Healthy`; `Engine`
   interface with `Do` (§3.4) replaces `BaseURL`/`Transport` in the gateway's view; redirect guard,
   first-byte timeout, bearer and `probeTimeout` live in the one `http.Client` the engine owns;
   `countTimeout` deleted; `status` and the banner print "not identified yet / NOT ANSWERING for
   Ns". Evidence: E1–E5. Concept budget 0. Can land before or after ticket 1 (ticket 1's queue reads
   `Info().Slots` either way).

3. **007 follow-through** — size 1, normal, **before launch** (after 007 lands). Binding: fold the
   step states into `connecting.step` if 007 landed them as states; persistence rules (§2.3: user
   turn before I/O, 2 s checkpoint, `streaming → interrupted` on load); one `/me` path (the
   post-request refresh dispatches into the same reducer as the 60 s poll). Evidence: W1–W7 as
   vitests over the reducers; screenshots for the reload case. Concept budget 0.

4. **Concept trim** — size 1, normal, **after**. Binding: delete `--queue-timeout`, `--max-body`
   (and `--request-timeout` if ticket 1 did not) as flags and config keys, constants in their
   place; cut `--ephemeral` if the PM rules so (keep `tunnel.Options.Ephemeral`); fold
   `retryAfterUpstreamDown`/`refreshEvery` into `upstream.ProbeInterval`; usage events for POST
   routes only (contract line; PM rules). Concept budget: negative.

5. **CLI and store cleanup** — size 1, normal, **after**. Binding: replace `reorder()` with the
   interspersed parse loop (§4 item 1) keeping both tests; shrink the `platform` seam (§4 item 9);
   delete the store throttle and the miss-path reload (§4 item 4) keeping `TestHotReload` and
   `TestLookupMissForcesReload` as "visible on the next call"; seed daily counters from
   `usage.jsonl` at start (§4 item 5) with a test that a restart mid-day keeps `today`; `status`
   label for open connections (§4 item 11); Windows second-serve guard via token dial (§4 item 15);
   `/v1/models` as a kind in the stage table (§4 item 21). Concept budget 0.

6. **`docs/ARCHITECTURE.md` v1** — PM-owned, not an engineer ticket: fold in §1.6 deadlines, the
   exact queue semantics, `Engine` in place of `Upstream` as the gateway seam, `unknown` kind,
   `host.log_prompts`, `stats()`, `--verbose`, and the flag/config deletions.

Landing order and why: 1 and 2 are independent; 1 first because it is the Protection-4 change a
public launch depends on. 3 waits for 007. 4 and 5 are independent of everything and can fill any
gap.

---

## 7. What NOT to change

Each of these the reviews probed and found sound, or a ruling settled. Changing them buys nothing
and risks a promise that is currently kept.

- **Tunnel exposure (Protection 1).** `OnTCP` nil for every port but 80, `ServedTCPPorts {80,80}`,
  no `OnTCPForward`/`AllowProxy`/SSH/files (`tunnel.go:124-133, 166-171`), pinned by
  `TestProtection1Config`/`TestOnTCPGate`; the parked-connection listener with its 1024 cap
  (`listener.go`). `[001-adv]`: "through the tunnel only port 80 reaches the gateway; host loopback,
  LAN, internet, and other tunnel ports all drop silently."
- **Host key handling.** `host.key.json` 0600 in a 0700 dir, `O_EXCL` temp + rename (005 10f),
  address derived from the private key (`tunnel.go:201-238`); short-form address by default,
  full form only with a pinned relay (001 judgment 3).
- **Invite format and both parsers.** `ic1.<tc…>.<secret>`, prefix checked before part count,
  `ic<N>` (N > 1) → "needs a newer app" (`invite.go:39-72`), the TS mirror running the Go vectors
  (`invite.ts`, `invite.test.ts`). `[001-adv]` refuted every edge case raised.
- **Key store.** `sha256:` hashes only, constant-time compare across every key
  (`store.go:136-144`), atomic writes with fsync (`:323-363`), fail-closed on corruption or an
  unknown file version, secret shown once. `[003-adv]`: "secret never logged/persisted/echoed".
- **Gateway auth and secrecy.** Bearer parsing (15 refuter cases), lookup on every request with no
  caching (`request.go:121-147`), the `TestMain` gate that fails the package if the secret appears
  in any log line or body, the friend's `Authorization` never forwarded, the engine's address never
  in a friend-facing message. `[002-adv]` clean.
- **The landed pipeline's shape.** The stage list, the record, `finish` as the one exit
  (`request.go`), the denylist as data, admission before body, deadlines armed by their owning
  stage, 401s unrecorded, `key_id` on paused/revoked, redirects refused, engine 400/422 → 400 —
  §1 refines inside it; nothing in §1 reopens it.
- **The limiter's mechanism.** One sliding 60 s log for RPM and TPM with exact Retry-After
  (002 judgment 1), reservations visible in `/me` (006) — §1.4 extends it; it does not replace it.
  Per-key concurrency as an immediate 429, never queued (002 judgment 11).
- **The streaming pipe.** Hand-rolled, flush per event boundary, `include_usage` forced,
  `reasoning_content` byte-for-byte, chunk-count fallback for an aborted stream (blessed
  deviation), client disconnect cancels the engine (`proxy.go:490-551`).
- **Shrink-to-fit context** (005 10c) and the `max_tokens` clamp that never rejects.
- **Error format.** OpenAI-shaped bodies, 14 codes with statuses and types (`errors.go:40-55`),
  Retry-After on every 429/503, mid-stream errors as SSE events.
- **The wasm bridge API and its leak discipline.** `connect/dial/ping/close`,
  `read/write/closeWrite/close`, every `js.Func` released, inert stubs after close, `Uint8Array`
  guard (005 10l/10m, `main_js.go`); `ping()` as a TCP connect through the relay with `via` from the
  DERP map (001 ruling).
- **HTTP/1.1 over a `Conn`.** One connection per request, `Connection: close`, no `closeWrite`
  after the request (documented reason, `http1.ts:62-64`), a real `ReadableStream` body
  (`http1.ts`); `pm/DECLINED.md` on pooling.
- **Admin over a unix socket, read-only, 0600**; the Windows loopback + token fallback.
- **`--log-prompts` and `--ephemeral` never persisted; `--upstream-key` persisted at 0600** (003
  ruling).
- **Product name in one constant per language**, and the invite prefix beside it.
- **Detection order** llama.cpp → Ollama → LM Studio → vLLM, sniffed by signature not port
  (`kinds.go:40-71`).
- **The dev harness.** `web/dev` fakes, the Playwright runners, `hack/tunneldemo`,
  `hack/measure.sh`, `leak-check.mjs` — the only things that prove the real bridge and the real
  relay agree with the unit tests.

## 8. Visual system

The app is drawn in **Swiss ink**, the brand direction the founder picked on 2026-09-06 (pm/LAUNCH.md
F8; the specification and the rendered mock are `docs/brand/swiss-ink.md` and `.html`): black ink on
white paper, a strict grid, hairline rules for separators and ink rules for frames, zero border
radius anywhere, and exactly one accent — a printer's cobalt `#1F3BFF` — reserved for the thing the
reader can act on. Dark is true black with the same cobalt, and cobalt never carries text there (the
`#7E93FF` tint does). Type is Archivo for everything and IBM Plex Mono for what is *measured or
machine-issued* — invite codes, the path pill, latencies, token counts, versions — never for prose,
which is what keeps the metrics readable as facts. Both faces are self-hosted woff2 under
`web/public/fonts` (SIL OFL 1.1, in THIRD_PARTY_NOTICES.md) so the app asks no third party for
anything (Protection 3; `launch-check` asserts it). **Every colour is a token in the `:root` and
`prefers-color-scheme: dark` blocks at the top of `web/src/styles.css` — including the syntax
highlighting, which is drawn from the same five palette colours. No component, and no generated
image, names a hex outside that file and its two deliberate mirrors: `web/dev/brand.mjs` (the social
cards) and `web/public/favicon.svg` (the mark, which has no stylesheet to read).**
