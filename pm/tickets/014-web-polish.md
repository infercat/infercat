---
id: 014
title: Web polish — the two failures a friend will hit, honest meters, touch, copy (from the friend-experience pass; folds in 013)
kind: normal
size: 5
status: landed
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

**Promises 13–17 (PM, desktop persona of the same experience pass, appended mid-flight).** Same
design rule: through the session and message machines, not around them. Budget unchanged.

13. **Reconnect after the host comes back (blocker).** After a connection-broken failure the client
    kept reusing the dead tunnel session: three Regenerates each waited 30 s and failed while the host
    was demonstrably healthy; a reload + Connect recovered in 2 s. On `degraded(engine)` from a broken
    connection, the retry path must tear down the session and re-establish it (wasm bridge `connect`
    again, then `/me`), with the banner action "Reconnect"; if re-dial fails, say "Still can't reach
    <host> — reload this page to start a fresh connection."
14. **Reply cap ending (blocker).** A reply that ends with `finish_reason: "length"` gets the existing
    `.ended` line: "This stopped at your invite's <N>-token reply limit." with a [Continue] that sends a
    continue turn; Settings' limits sentence includes the reply cap (`max_output_tokens` from
    `/me.limits`).
15. **First suggestion chip.** "Explain what just happened when I pasted that code." makes the model
    ask for the code. Either seed a short default system prompt that makes it answerable or replace the
    chip with a question any model can answer. Engineer's call; say which.
16. **Edit and regenerate keep the old answer.** Editing a message replaces the previous answer with no
    undo and the sidebar title goes stale. Minimum: the button reads "Replace answer", the conversation
    retitles from the current first message, and the previous answer is kept behind a "‹ 1/2 ›"
    meta control if that fits the budget; otherwise keep the old answer as a collapsed "previous answer"
    block.
17. **Rendering.** Single newlines in model output render as line breaks (remark-breaks or
    `white-space: pre-wrap` on paragraphs); reasoning text is rendered as markdown (no raw `**`); the
    empty first conversation is titled "Untitled chat" so the sidebar's "New chat" button is the only
    "New chat".

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

All times 2026-09-02, EDT, laptop.

- 14:37 — ACK. Worktree `t014-web-polish` at `3c1efb2`; scope `web/**` (not `web/wasm/**`) plus the
  one settle-table row in `internal/gateway/**`. Read BELIEFS, DESIGN §2, 014, 007 (all of it),
  then `web/src`, `web/dev`, `internal/gateway/request.go`, `limits.go`, `proxy.go`.
  pnpm 11.13.0, Node 22.23.2, Go 1.27.0. `pnpm install --frozen-lockfile` green in 1.1 s.

- 14:39 — **Ticket instruction correction (mechanical).** The dispatch names `--dev-listen
  127.0.0.1:66090` and "ports 66000–66999". TCP ports stop at 65535; the binary refuses it
  (`bunny-network: listen tcp: address 66090: invalid port`). Used **6609** for the gateway dev
  listener, **6610** for the vite dev server and **6611** for `vite preview`. Nothing else changed.

- 14:44 — **Promise 5 measured on the real stack, before any edit.** Own host on 6609 against the
  shared llama-server (never restarted), key `k_ea4920`, limits 20 rpm / 200k daily.
  Two runs: the dev bundle in Direct mode (browser→gateway requests counted by Playwright) and the
  **production** bundle over the real New York relay (host's own `usage.jsonl` counted, so
  StrictMode cannot be the explanation). Production result:

  ```
  CONNECT   /me 200 · /v1/models 200                      rpm_used 0 -> 1
  SEND #1   /v1/chat/completions 200 · /me 200             rpm_used 1 -> 2   meters "2/20 per minute"
  SEND #2   /v1/chat/completions 200 · /me 200             rpm_used 2 -> 3   meters "3/20 per minute"
  ```

  So a send costs **one** counted request, not two — the per-send `/me` is free
  (`request.serve` routes `/me` without `admitKey`). The two-for-one the reviewer saw is the
  **connect-time `/v1/models`**: `models()` (`internal/gateway/proxy.go:286`) calls `admitKey`, so
  the list call takes an RPM entry, and it lands in the same 60 s window as the friend's first
  message. The friend sends one message and the meter reads **2/20**. Two causes, one symptom:
  the gateway counts a list call as a request (the ticket's ruling), and the client asks for the
  list at all when `/me` already carried `host.models` (`Chat.tsx:99-103` — unconditional).
  In the dev bundle the same effect fires twice under StrictMode, so dev reads 3/20; that part is
  a dev artifact and not the defect.


- 15:0x — Promises 13–17 received mid-flight and appended above. All five fit; the accounting is in
  the Freeze block. Promise 15: I replaced the chip rather than seeding a default system prompt —
  a system prompt is text silently added to every request, it collides with the reader's own in
  Settings, and it buys one chip. The chip now asks something any model can answer about its own
  situation: "How can you answer me if you are running on someone else's computer?"

- 15:2x — Promise 1's deadline did not hold on the real stack, twice, and both causes were real.
  (a) I first guarded the 15 s cut with a tunnel ping, so a host merely *busy* would never be called
  asleep. Measured against a real killed host, the relay kept answering pings for ~45 s after the
  process died, so the guard defeated the deadline it was protecting. Removed: what we know at 15 s
  is that the host has not answered, and the ticket's copy already hedges ("probably"). (b) With the
  guard gone it still failed at 45 s: `ConnReader.pull` and `fetchOverConn`'s write awaited a Conn
  that a dead peer never settles, and `close()` does not settle it either — so every deadline above
  the transport silently became the relay's own timeout. `raceAbort` fixes the class (Stop had the
  same latency). Now 15 s on the real stack, with a vitest for a conn that never settles.

- 15:4x — Rebased onto `origin/main` `118fd62` (016 and 017 landed). Two conflicts, both additive
  and both from 017: `product.ts` (VERSION stamping vs my `privacyLine`) and `Chat.tsx` (the Settings
  version line vs my Cancel/Done actions). Kept both sides of each. My gateway row is untouched by
  016 as the PM expected. Full check re-run from that base and printed in the Report.

## Report

### The core, shown working

Everything below is the **production bundle over the real New York relay against a real
`bunny-network serve`**, not the fakes — the fakes cannot prove any of the three things that
actually mattered. One command reproduces it: `dev/real-check.mjs` (it starts, kills and restarts
the host itself).

**1. Promise 5 — the root cause of "one message costs two requests", found before any edit.**
A send always cost *one* counted request; the per-send `/me` is free (`request.serve` routes `/me`
without `admitKey`). The second request was the **connect-time `/v1/models`**, which `models()`
admitted through `admitKey` — so a list call took an RPM entry and landed in the same 60 s window as
the friend's first message. The friend sent one message and the meter read **2/20**. Two causes, one
symptom, and both are fixed:

```
BEFORE (production bundle, host's own usage.jsonl)
  CONNECT   /me · /v1/models                          rpm_used 0 -> 1
  SEND #1   /v1/chat/completions · /me                rpm_used 1 -> 2    meters "2/20 per minute"

AFTER
  CONNECT   /me                                       rpm_used 0 -> 0    "20 messages left this minute"
  SEND #1   /v1/chat/completions · /me                rpm_used 0 -> 1    "19 messages left this minute"
  SEND #2   /v1/chat/completions · /me                rpm_used 1 -> 2    "18 messages left this minute"
```

Gateway: one settle-table row — `endpoint.countsAgainstRPM()`, read by `settleRow` before anything
else — so a list call is admitted (per-key concurrency still bounds it) and never counted. Client:
`/v1/models` is now a *fallback*, asked only when `/me` reported no models at all; on a normal host
it is never called. `TestModelsMetered` is rewritten to the new truth and now also proves an RPM-1
key can list twice and still have its one message.

**2. Promise 1 (blocker) — a host that went to sleep, on a host I actually killed mid-send:**

```
"Still waiting for Max's laptop…"                                              at  5 s
"Max's laptop didn't answer. It's probably asleep or offline — your message
 is saved, try again in a minute."                                             at 15 s
  pending turn kept: true          action: "Reconnect"
  header: "llama.cpp is not answering on the host — messages will fail until it is back"
  path:   "Max's laptop — not answering · last 71 ms 27 s ago"
  meters: "— / 20 per minute" · "— / 200k tokens today"
```

No raw transport string appears in primary copy anywhere; `dial port 80: context deadline exceeded`
and friends go to a `Details` disclosure. The session is in `degraded(engine)`, the header, the path
pill and both meters agree with each other, and the reader's words are still in the thread.

**3. Promise 13 (blocker) — and the same host coming back:** `Reconnect` re-established the session
in **0.3 s** (the reviewer measured three 30 s failures over the dead one), the pending turn survived,
and the next message went through. `redial` is a session event: the reducer returns to `connecting`,
the one closer in `App.tsx` closes what it dropped, and one effect dials again.

Promise 2 (paused) keeps the chat on screen with the banner and the reader's text back in the
composer; the two codes no waiting can fix still eject, to a screen whose primary action is now
"Paste a new code". All 17 promises are rendered in `web/dev/screenshots/14-*.png` (13 against the
fakes, 4 against the real stack).

### Edge awareness, one line

Handled: a chat and a conversation-list write racing between two tabs; a reply still `streaming` in
storage at load; a `/me` that answers, then stops, then answers again; a pause arriving from `/me`
and from a stream; a redial superseded by a disconnect; a capped reply that is complete as a
transfer; an edited *first* message (the title follows it); Enter during IME composition on a touch
device; a nameless host in every sentence that names one; a model id that is all quantisation tags;
a relay region the city table has never heard of.

### Verified (printed, from base `118fd62`)

```
$ pnpm install --frozen-lockfile  → Already up to date                                    exit 0
$ pnpm typecheck                  → tsc --noEmit, no output                               exit 0
$ pnpm test                       → Test Files 9 passed (9) | Tests 181 passed (181)      exit 0
                                    0 failed, 0 skipped   (007 left 132; +49 here)
$ pnpm lint                       → eslint ., no output                                   exit 0
$ pnpm build                      → 505 modules; index 226.64 kB (gzip 72.95)
                                    Chat 355.48 kB (gzip 108.49); css 13.07 kB            exit 0
$ go test ./internal/gateway/     → ok  internal/gateway  15.0s                           exit 0
$ go build ./... && go vet ./... && go test ./...  → all ok, 11 packages                  exit 0
$ gofmt -l cmd internal           → no output                                             exit 0
$ pnpm screenshots                → 57 screenshots; no console errors, no page errors,
                                    no horizontal overflow at 360 px or 390 px            exit 0
$ dev/real-check.mjs all          → every promise above held on the real relay + host     exit 0
```

`pnpm-lock.yaml` and `package.json` unchanged: **no new dependencies.**

### Judgment calls

- **The "asleep" deadline is not ping-guarded.** I tried to protect a merely *busy* host (the gateway
  parks a request behind a full engine for up to 30 s) by pinging before giving up. Measured: the
  relay answers pings for ~45 s after the host process dies, so the guard broke the blocker it was
  meant to refine. Removed. A busy host is therefore called "probably asleep or offline" in the
  window between 15 s and its own 503 — the copy hedges, and Reconnect costs 2 s. **Re-price if you
  want the busy case distinguished**; it needs the gateway to flush a head before it queues.
- **`raceAbort` in `http1.ts` is bought beyond the ticket** and is declared again below. Without it
  no deadline above the transport is real.
- **"Reconnect" replaces "Try again" for exactly the host-asleep failure** (promise 13 over promise
  1's wording): retrying over a session we just proved dead is the defect 13 exists to remove.
- **Paused rolls the un-sent turn back out of the thread** and puts the text in the composer, because
  promise 2 asks for both "your message is still here" and "the composer keeps its text"; keeping it
  in both places would show it twice.
- **Promise 16 keeps the old answer as a collapsed "Previous answer" block**, the ticket's stated
  fallback, not the "‹ 1/2 ›" pager. The pager is a second navigation model for one case.
- **Promise 15: I replaced the chip, not seeded a system prompt.** A default system prompt is text
  silently added to every request that collides with the reader's own; the reason for the chip was
  that it was unanswerable, and a question that answers itself fixes that for one line.
- **Promise 4 is the real per-conversation layout**, not the read-only-tab fallback: the index holds
  ids, each chat is its own key, and a `storage` event makes a tab re-read before it writes.
- **`/me` now has a 10 s bound.** Without one, "unknown" arrived 30 s after the host died, which is
  long after it stopped being news (promise 3).

### Bought beyond the ticket (declare loudly)

- **`raceAbort` in `web/src/transport/http1.ts`** (+20/−3): every await that talks to a `Conn` is
  raced against the abort signal. A tunnel conn to a peer that has gone away accepts a write and
  never settles it, and `close()` does not settle it either — so promise 1's 15 s deadline was
  landing at 45 s, and Stop had the same latency. This is a transport fix inside `web/**`, in scope,
  but it is a class fix I did not price. Two vitests pin it.
- **A 10 s bound on the background `/me`** (`ME_TIMEOUT_MS`), for the reason above.
- **`web/dev/real-check.mjs`** (new, 190 lines of dev harness): the real-stack runner. The three
  promises above cannot be shown with the fakes, and a screenshot nobody can reproduce is weak
  evidence. It starts and kills only the host it started itself.
- **Four real-stack screenshots** (`14-real-*.png`) beyond the seven the ticket names.
- **Three new fake modes** (`hostAsleep`, `keyPaused`, `meFailsAfter`) and a `/cap` stream word.

### Not verified

- **A busy host mislabelled as asleep** — reasoned about and reproduced only in the sense that I
  removed the guard against it; not exercised with a genuinely saturated engine.
- **Two live tabs in a real browser** for promise 4. The merge is unit-tested over the real
  `localStorage` API through the storage stub; two Chrome tabs were not driven.
- **iOS Safari and Firefox.** Chromium only, as in 004 and 007. Promise 6's touch branch is
  exercised at 390 px with `hasTouch`, not on a real phone.
- **`bunny-network keys pause/resume` against the live host** for promise 2 — the paused surfaces
  were shot against the fake; the real gateway's `key_paused` path is exercised only by 005's
  `int-check.mjs`, which I did not re-run.

### For the PM to re-price

1. **The size is 1496 source lines against a ceiling of 2000, for 17 promises** — 12 priced, 5
   appended mid-flight. The five arrivals cost ~280 of that. It fits, but the ceiling was set for 12.
2. **The transport abort gap was a latent defect under every deadline we have shipped**, not a 014
   item. Worth knowing before the next ticket prices a timeout.
3. **`/v1/models` is still *refused* at the RPM ceiling** even though it no longer counts: `admit`
   checks the ceiling, and the settle table only decides what is kept. Exempting it from admission
   too is a one-line change in `limits.go` I did not take, because the ruling said one settle-table
   row.
4. **Ports.** The dispatch named `--dev-listen 127.0.0.1:66090` and "ports 66000–66999"; TCP stops at
   65535 and the binary refuses it. I used 6609/6610/6611. The range in the next ticket needs fixing.

## Freeze

- **Base:** `118fd62` (public `main`, after 016 and 017 landed). **Lane:** `t014-web-polish`, pushed,
  not merged.
- **Patch SHA-256:** of `git diff origin/main...HEAD --binary` at the freeze commit, reported with
  the freeze message (recording it in this file would change it).

| Bucket | Budget | Measured (raw added / deleted) | Verdict |
|---|---|---|---|
| Source TS/TSX (`web/src/**`, tests excluded) | ≤2000 lines of change | +1230 / −266 = **1496** | inside, for 17 promises |
| Web tests (`web/src/**/*.test.ts`) | not budgeted | +648 / −32 | 9 files, 181 tests |
| Stylesheet (`web/src/styles.css`) | not source (ruling on 004) | +58 / −4 | 10 new surfaces |
| Dev harness (`web/dev/**`) | not budgeted | +398 / −8 | fakes, the 014 shots, `real-check.mjs` |
| Go source (`internal/gateway/**`) | one settle-table row | +20 / −3 | `countsAgainstRPM` + its two call sites |
| Go tests | — | +19 / −7 | `TestModelsMetered` rewritten |
| Ticket record | — | +61 | promises 13–17, Log, Report |
| Dependencies | none without a reason | **0 added**, `pnpm-lock.yaml` unchanged | — |
| Screenshots | ≤500 KB each | 57 files (17 `14-*`), largest 148 KB | inside |

**Concepts: 3 budgeted, 3 used. Promises 13–17 introduced 3 more, each named by the ticket that
asked for it.**

| Concept | Status |
|---|---|
| `degraded('key')` reason | budgeted (1 of 3) |
| Pending turn (`Message.pending`) | budgeted (2 of 3) |
| Undo toast | budgeted (3 of 3) |
| `redial` session event | promise 13 asked for the teardown-and-redial path |
| `Message.capped` + Continue | promise 14 |
| `Message.previous` | promise 16 (the ticket's stated fallback) |

Not counted as concepts (properties of things that already existed, or named in the promise text):
`Live.meOk` and `Live.paused` (siblings of the existing `pathOk`), `Message.waiting` and
`Message.details` (promise 1 names both the "Still waiting" line and the "Details" disclosure), the
`bn.lastHost` storage record and the per-conversation storage keys (promise 9 and promise 4 name
them), `modelLabel`, `cityFor`, `maskInvite`, `privacyLine`, `coarsePointer`, `raceAbort`,
`needsRedial`, `saidInBanner`. Rule against me on any of these if you disagree.


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

## Ruling (PM, 2026-09-02 17:30)

**Landed** on main (merge of `t014-web-polish`); Go and gateway `-race` green, web typecheck/test 181/lint/
build green, printed. `raceAbort` in `http1.ts` and the 10 s `/me` bound accepted as necessary. The
promise-5 root cause (connect-time `/v1/models` took an RPM entry) closed on both sides.

**Rulings on the two re-price items:** (a) `/v1/models` refused at the RPM ceiling — accepted as-is for
launch; the client no longer calls it at connect, so only the 60 s poll can hit it; one-line fix listed in
012. (b) **Busy host reads as asleep** — not acceptable for launch (Surfaces tell the truth; strangers
will queue): ticket 018 makes the gateway send response headers and `: queued` SSE keepalives while a
streaming request waits for a slot, with a queue timeout delivered as an SSE error event, and the client
shows "Waiting for a free slot on <host>…" when headers arrived but no token has.
Dispatch note: the 66000–66999 port range in the brief was invalid (> 65535); the engineer used 6609–6611.
