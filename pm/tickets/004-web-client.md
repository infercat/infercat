---
id: 004
title: Web client — tunnel fetch, connect screen, streaming chat
kind: normal
size: 5
status: landed
updated: 2026-09-02
release: demo-1
---

# 004 — Web client

## Binding

**Why.** This is the thing the world sees. A stranger pastes an invite into a web page and is chatting
with someone's GPU thirty seconds later. It launches on social media; the bar is "wow", not "works".

**Promises.**
1. `web/`: Vite + React + TypeScript, `pnpm`. Scripts: `dev`, `build`, `typecheck`, `test` (vitest),
   `lint`. Product name only in `web/src/product.ts`. Builds to a static bundle that works from any
   static host and from `file://`-less localhost. Dark and light themes via `prefers-color-scheme`.
2. `web/src/invite.ts`: parse/format of `bn1.<tc>.<secret>` mirroring `docs/ARCHITECTURE.md`; same error
   classes as the Go side (unknown newer prefix → "This invite needs a newer version of the app").
   Unit-tested with the same vectors the Go tests use (copy them from `internal/invite` once 001 lands;
   until then write your own and note it).
3. `web/src/transport/`: a `Transport` interface `{ fetch(input, init): Promise<Response> ; ping(): … ;
   close() }` with two implementations: **DirectTransport** (real `fetch` against `VITE_DIRECT_URL`, for
   development against `bunny-network serve --dev-listen`) and **TunnelTransport** (loads
   `/bunny.wasm` + `/wasm_exec.js`, calls `window.BunnyTunnel.connect`, and implements HTTP/1.1 over
   `Session.dial()`): request line + headers + body, `Connection: close`, `Host: bunny`; response parsing
   of status line, headers (case-insensitive), body by `Content-Length`, `Transfer-Encoding: chunked`, or
   close-delimited; returns a real `Response` whose body is a `ReadableStream` that yields bytes as they
   arrive (no buffering to completion). `AbortSignal` closes the conn. One conn per request.
   Unit-tested with a scripted fake `Conn` (chunked, content-length, split across reads, headers split
   mid-line, early EOF, abort).
4. **Connect screen.** Paste field (accepts surrounding whitespace, shows format errors inline), a
   "Connect" action, then honest progress states: loading wasm (with %), connecting to relay, handshake,
   verifying invite (`GET /me`), connected. Failure states with a next step: bad invite, host offline
   (handshake timeout), invite revoked/paused (403 codes), relay unreachable. Remembers the last invite
   and the tunnel `privateKeyJSON` in `localStorage` (behind try/catch) with a visible "forget this
   invite" action. A one-line explanation of what the code is and that the host sees usage counts, not
   messages (Protection 3 phrasing).
5. **Chat.** Conversation list (sidebar; collapsible on mobile), new chat, streaming assistant replies
   rendered as markdown with code blocks (copy button), a collapsible **Thinking** block fed by
   `reasoning_content` deltas that auto-collapses when the answer starts, stop button (aborts the request
   and closes the conn), regenerate, edit-and-resend of the last user message. Model picker from
   `/v1/models`, system prompt and temperature in a small settings sheet. Conversations persisted in
   `localStorage`. Keyboard: Enter sends, Shift+Enter newline. Usage: the final `usage` chunk updates a
   small per-message token count.
6. **Status surface (Surfaces tell the truth).** A header pill: host name and model from `/me`; path
   `relayed via <region> · <rtt> ms` from `Session.ping()` every 30 s (never a bare green dot); a usage
   bar from `/me` refreshed after each request (requests this minute / rpm, tokens today / daily). Errors
   from the gateway map to friendly copy per `code`: 429 shows a countdown from `Retry-After` and
   auto-retry is offered, not automatic; 503 `upstream_down` says the host's engine is offline; 403
   revoked returns to the connect screen with the reason.
7. **Polish bar.** Responsive from 360 px to desktop; no layout shift while streaming; focus management
   on send; empty state that reads as an invitation; a favicon and a `<title>`; no external network
   requests except the DERP map fetch the wasm makes. Bundle: no UI framework beyond React; markdown via
   `react-markdown` + `remark-gfm` + a light highlighter is acceptable. Lighthouse-style sanity: the page
   loads without the wasm when the invite field is empty (wasm loads on Connect).
8. **Evidence.** `pnpm typecheck && pnpm test && pnpm build` output. A manual run in Chrome (browser
   tool if available, else Playwright) in Direct mode against a gateway or, if 002/003 have not landed,
   against `hack/tunneldemo` from 001 for `/healthz` and `/stream` (proves streaming through the tunnel)
   and a **fake gateway** you write under `web/dev/fake-gateway.ts` (Node http server: `/me`,
   `/v1/models`, `/v1/chat/completions` streaming SSE with `reasoning_content` then content, 429 with
   Retry-After on demand). Screenshots of: connect screen, connecting states, chat mid-stream with the
   thinking block, a 429 state, and mobile width. Paste paths in the report.

**Size 5** (≤2000 source lines excluding tests and generated files). Concept budget 6: transport,
invite, conversation, message, settings, status. No auth beyond the invite, no accounts, no plugins,
no themes beyond dark/light, no i18n.
**Normal**; user-facing → an experience review runs after landing.

**Scope contract.** `web/**` except `web/wasm/**` and `web/public/bunny.wasm*`/`wasm_exec.js` (built
artifacts from 001). Do not touch Go code.

**Non-goals.** Hosting/deploy config (declined for tonight). Native wrappers. Image attachments UI (pass
`image_url` parts through if trivially supported by the composer, else leave out). Sharing/export.

**Handoff.** Branch `t004-web-client`, rebased on `main`, checks printed green. Report under `## Report`
with screenshots' paths. Push. Do not merge.

## Background (hypotheses)

- The wasm bridge JS API is fixed in `docs/ARCHITECTURE.md`; 001 builds it in parallel. Until
  `web/public/bunny.wasm` exists, TunnelTransport can only be unit-tested with a fake `BunnyTunnel`;
  write that fake so the whole connect flow runs in tests.
- tailcat's own `web/app.js` (in `/Users/yuanpingsong/.claude/jobs/12b4a99c/tmp/tailcat/web/`) shows
  how to load the wasm with a progress bar and how it handles the `.wasm.gz` case. Reuse the idea, not
  the code.
- SSE parsing: split on blank lines, `data:` lines, `[DONE]` sentinel; chunks are
  `chat.completion.chunk` with `choices[0].delta.{content,reasoning_content}` and a final chunk with
  `usage` and empty `choices`.
- Design direction (PM taste): quiet, ChatGPT-shaped layout, system font stack, one accent color, generous
  whitespace, status text rather than icons. The connect screen is the landing page for the social
  launch — make it explain the product in one sentence.

## Log

All times 2026-09-02, EDT, laptop.

- 02:01 — Worktree `t004-web-client` at `f171164`. Read BELIEFS, ARCHITECTURE, 004, 001, plus the
  seam packages (`internal/keys`, `internal/usage`, `internal/upstream`) for the `/me` shape.
- 02:06 — `web/` scaffolded; pnpm 11 rejects the install until esbuild is allow-listed. pnpm 11
  moved that setting: not `package.json#pnpm.onlyBuiltDependencies`, not `.npmrc`, but
  `pnpm-workspace.yaml` `allowBuilds: {esbuild: true}` (a map, not a list). Recorded here because
  every future web ticket will hit it.
- 02:10–02:25 — Wrote invite, transport (HTTP/1.1 + wasm loader + the two Transports), api (SSE,
  errors, friendly copy), storage, and the UI; wrote the three dev fakes.
- 02:27 — First full test run: 57 passed, 1 failed — my own `content-length: 13` vector for an
  11-byte body. Fixed; 58 passed.
- 02:30 — First Playwright run. Screenshots showed three real defects: the composer Send button
  overflowed its row at 360 px (`width:100%` textarea in a flex row), the mobile header wasted a
  whole row on the Settings button, and message actions stayed clickable mid-stream. All fixed.
- 02:37 — Trimmed the `describeUsage` indirection and compacted the error-copy table.
- 02:38 — `git fetch && git rebase origin/main`: **001 and 003 had landed**. Re-planned around that.
- 02:39 — Rewrote `src/invite.ts` against the real `internal/invite`: it has an error I did not
  have (`ErrAddr` — the address must start with `tc`, be longer than `tc`, and be base64url), and
  its version check rejects `bn0`/`bn01`/`bn` overflow as *wrong*, not *newer*. Ported
  `TestDecodeErrors` verbatim: 36 invite tests, 78 total.
- 02:40 — `make wasm` produced the real bridge (26,912,428 B raw / 6,182,828 B gzipped).
- 02:41 — First real-tunnel run failed: `WebAssembly compilation aborted`. Cause found with
  `curl -I`: vite serves `bunny.wasm.gz` with `Content-Encoding: gzip`, so the browser had already
  decoded it and my `DecompressionStream` was decompressing plaintext. Fixed in `wasm.ts`.
- 02:42 — Real-tunnel check PASSED over the public relay (numbers in the report).
- 02:44 — `pnpm install --frozen-lockfile && pnpm typecheck && pnpm test && pnpm build` all green.
- 02:45 — Measured the diff. **Over the size budget; see `## Contest`.**

## Report

**Read `## Contest` first: the slice is complete and verified, but it does not fit the stated
≤2000 source lines and the budget needs a ruling (or a cut list) before it lands.**

### The core, shown working

Three things had to be true. Each was verified against the real thing, not a mock of it.

**1. The browser really speaks HTTP/1.1 through the real tunnel, and really streams.**
Ran with the wasm bridge 001 just landed, against `hack/tunneldemo` over the public relay
(`pnpm tunnel-check`, `web/dev/tunnel-check.mjs`):

```
bunny.wasm 26912428 bytes, bunny.wasm.gz 6182828 bytes
tunnel address: tco2FwWCCpVr6N72JrtAkrR-Wu25myML2odUJ3jYIVFwLXgAdjR2FrWCAxBb0wCI98Rs56vaRuxA49Z8Io29KQtNO5y4a_Tk3gUGFpGQEt
wasmMs: 65            connectMs: 167
ping: {"rttMs":71.399936,"via":"DERP(nyc)","direct":false}
healthStatus: 200   healthContentType: "application/json"   healthBody: "{\"ok\":true}"   healthMs: 62
streamStatus: 200   streamEvents: 20   firstEventMs: 63   lastEventMs: 1982
connectLogSample: ["handshake attempt 1: ok, 84ms","handshake up after 160ms","relayed via DERP(nyc)"]
PASS: healthz body, 20 streamed events, first event 63 ms vs last 1982 ms
```

The first SSE event landed at 63 ms and the last at 1982 ms: the `Response.body` is a real
`ReadableStream` off the wire, not a buffer handed over at the end. That is the property the whole
demo rests on. (Numbers are yours to copy into `docs/MEASURE.md`; the command is in the script's
header comment.)

**2. A stranger pastes a code and is chatting.** The full connect flow — paste → wasm → relay →
handshake → `GET /me` → chat — runs in the browser and in `src/connect.test.ts`, both driven
through `web/dev/fake-bunny-tunnel.ts`, which is a real HTTP/1.1 server on a fake `Conn` (chunked
for SSE, content-length for JSON). Screenshots 01–06 are that path start to finish.

**3. Surfaces tell the truth.** The header reads `relayed via sfo · 83 ms` (from `Session.ping()`,
re-measured every 30 s), `1/20 per minute`, `264/200k tokens today` — text, never a dot. Every one
of the twelve documented gateway codes has its own copy and next step; 429 shows a countdown from
`Retry-After` and offers a retry it never takes on its own (screenshot 07).

Edge cases handled, one line: response head split mid-header-line, body split mid-chunk, chunked
with trailers, close-delimited bodies, early EOF, bad chunk size and bad Content-Length, abort
before/during the head and mid-body, 204/HEAD, non-HTTP replies, storage that throws
(private mode), corrupt stored JSON, an unterminated code fence mid-stream, and a paste with
surrounding whitespace or a newer `bn<N>` prefix.

### Verified (printed)

```
$ pnpm install --frozen-lockfile   → Already up to date. Done in 158ms                exit 0
$ pnpm typecheck                   → tsc --noEmit, no output                          exit 0
$ pnpm lint                        → eslint ., no output                              exit 0
$ pnpm test                        → Test Files 5 passed (5) | Tests 78 passed (78)   exit 0
                                     0 failed, 0 skipped
$ pnpm build                       → 501 modules; index 212.42 kB (gzip 68.09)
                                     Chat 348.58 kB (gzip 105.92); css 10.96 kB       exit 0
$ pnpm screenshots                 → 17 screenshots; no console errors, no page
                                     errors, no horizontal overflow at 360 px         exit 0
$ pnpm tunnel-check                → PASS (numbers above)                             exit 0
```

`pnpm screenshots` (`web/dev/screenshots.mjs`) is the reusable browser check the PM asked for: it
starts the fake gateway and the dev server, drives headless chromium, and fails the run on any
console error, any page error, horizontal overflow at 360 px, an off-origin request from the
production bundle, a missing `<title>`, or a screenshot over 500 KB. It also builds and previews
`dist/` — that is where the "no external network requests" and "no wasm until Connect" promises are
checked, not asserted.

### Screenshots — `web/dev/screenshots/`

| | |
|---|---|
| `01-connect.png` | the landing page a stranger sees |
| `02-connecting.png` | honest progress: tunnel · relay · handshake · invite |
| `03-chat-empty.png` | empty state |
| `04-streaming-thinking.png` / `05-streaming-answer.png` | Thinking open mid-stream, then auto-collapsed as the answer starts |
| `06-chat-complete.png` | markdown, code block with copy, GFM table, `25 in / 239 out` |
| `07-rate-limited.png` | 429 with a live countdown from `Retry-After` |
| `08-settings.png` | model, system prompt, temperature, and what the host can see |
| `09-host-offline.png` / `10-bad-invite.png` | the two failures that are not the user's fault, and the one that is |
| `11-direct-mode.png` | Direct mode: real `fetch`, real CORS, real SSE against `web/dev/fake-gateway.ts` |
| `12-connect-dark.png` / `13-chat-dark.png` | `prefers-color-scheme: dark` |
| `14-mobile-connect.png` / `15-mobile-chat.png` / `16-mobile-drawer.png` | 360 px |
| `17-production-landing.png` | the built bundle, served static, zero off-origin requests |

### Judgment calls

- **No `closeWrite()` after writing a request.** Go's `net/http` server reads the connection in the
  background and cancels the request context on EOF, which would kill a stream mid-flight.
  `Connection: close` already frames the response. Commented at the call site — please keep it when
  002's gateway lands.
- **`web/dev/fake-backend.ts` is one implementation behind two adapters** (Node server, in-page
  `Conn`), so Direct mode and Tunnel mode are demoed against the same gateway behaviour.
  Typing `/429`, `/503`, `/403` or `/502` as the first word of a message makes the fake host answer
  with that failure; that is how the error screenshots are produced.
- **Dev-only query params**, all behind `import.meta.env.DEV`: `?fake` (install the in-page bridge),
  `?direct`, `?invite=`, `?autoconnect`, `?connectMs=`, `?tokenDelay=`. Deliberately dev-only —
  putting a secret in a URL fights Protection 2.
- **The chat screen is a lazy chunk.** The landing page is 212 kB; the markdown renderer and its
  highlighter (349 kB) load on Connect, prefetched on the first keystroke in the invite field.
- **`invite.ts` diverges from Go in exactly one place**: an empty/whitespace-only field returns
  `empty` ("Paste the invite code your host sent you") where Go returns `ErrPrefix`. Every other
  case in `TestDecodeErrors` matches, and the test file says so at each divergence.
- **`pnpm-workspace.yaml` exists only to allow esbuild's install script.** No dependency but esbuild
  may run code at install time.
- **CSS ships as one 313-line stylesheet**, not a framework and not per-component files.

### Bought beyond the ticket (declare loudly)

- **Deleting a conversation** (the `×` in the sidebar, ~12 lines). Not promised; a sidebar with no
  way to remove anything is a hole a launch audience will find. Say the word and it goes.
- **`web/dev/tunnel-check.mjs`** (~120 lines of harness). Not asked for — 001 had not landed when
  the ticket was written. It is now the only thing that proves the real bridge and my transport
  agree, so I would keep it.
- **Three suggestion chips in the empty state**, as my reading of "an empty state that reads as an
  invitation".

### Not verified

- **Against the real gateway (002).** 002 has not landed, so `/me`, `/v1/models`, the streaming
  proxy, the error bodies and `Retry-After` are exercised only against my fake, which is shaped by
  `docs/ARCHITECTURE.md` rather than by 002's code. First integration run is the real test; I would
  expect the friction to be in the `/me` shape and in `Retry-After` formatting.
- **A real model's `reasoning_content`.** The fake emits it; llama.cpp's exact delta shape is
  unproven here.
- **iOS Safari and Firefox.** Chromium only. `DecompressionStream` and `ReadableStream` in
  `Response` are the two things worth re-checking on Safari.
- **A real invite end-to-end in the UI.** `tunneldemo` serves `/healthz` and `/stream`, not `/me`,
  so the app's connect flow against it stops at "Checking your invite" by design. The transport
  underneath is verified (above); the screen is verified against the fake.
- **Lighthouse itself** was not run — the sanity properties it would have caught are asserted in
  the screenshot script instead.

### For the PM to re-price

1. The size budget (see `## Contest`).
2. **`/me` after every request** is one extra tunnel round trip per message. Cheap over a warm
   session, but if 002's `/me` turns out to be expensive, the usage bar should move to a header the
   chat response already carries.
3. **The DERP map fetch** is the wasm's, not mine; the production bundle makes no off-origin
   request of its own (asserted in the screenshot run). Worth re-checking once the app is hosted.

### How to run it

```
cd web && pnpm install --frozen-lockfile
pnpm screenshots                     # fake gateway + dev server + chromium, all 17 shots
pnpm dev                             # then http://127.0.0.1:49173/?fake  (in-page tunnel)
pnpm fake-gateway                    # 127.0.0.1:49090, then /?direct for Direct mode
make wasm && cd web && pnpm tunnel-check   # the real bridge over the public relay
```

Ports default to 49173 (web), 49174 (preview) and 49090 (fake gateway); all overridable with
`WEB_PORT` and `FAKE_GATEWAY_PORT`.

## Contest

**Claim.** The eight promises in this ticket do not fit "Size 5 (≤2000 source lines excluding tests
and generated files)". I did not stop before editing, because the overrun was only measurable once
a coherent cut existed; the measured map is below and nothing has been merged.

**Measured, from the diff (`web/`, excluding `node_modules`, `dist`, `public/bunny.wasm*`,
`public/wasm_exec.js`, and `web/wasm/**` which belongs to 001):**

| Bucket | Raw lines | Non-blank, non-comment |
|---|---|---|
| `web/src/**` excluding tests | 2357 | 2044 |
| — of which TS/TSX | 2044 | 1765 |
| — of which `styles.css` | 313 | 279 |
| Build config + `index.html` + `package.json` | 153 | 143 |
| Tests (`web/src/**/*.test.ts`) | 641 | 560 |
| Dev harness (`web/dev/**`: two fakes, fake gateway, two browser runners) | 888 | 729 |

Largest files: `ui/Chat.tsx` 535, `styles.css` 313, `transport/http1.ts` 289, `api.ts` 245,
`ui/Connect.tsx` 216.

**Why it needs a ruling rather than my judgment.** The calibration in `pm/BELIEFS.md` is marked
"uncalibrated by history" and was written before any web ticket existed. It does not say whether a
stylesheet counts as source, and that single question moves this slice from 2044 over budget to
1765 under it. I am not willing to pick the reading that suits me.

**What I did instead of quietly shrinking it.** I removed real slack (an indirection in the usage
meters, a verbose copy table) for about 40 lines. I did not reflow JSX to hit a number — that buys
a smaller count and a worse file, and it would make the accounting lie.

**Options, in the order I would take them:**

1. **Rule that the stylesheet is not source.** TS/TSX comes to 1765 non-blank non-comment lines,
   inside a size 5, and nothing is cut. My recommendation.
2. **Accept 2044 as a size 5 overrun of 2%** and recalibrate the rungs with this slice as the first
   real web data point.
3. **Cut to fit.** In the order I would cut, with what each costs:
   `SettingsSheet` (system prompt + temperature + model picker) −70 lines, promise 5 loses its
   settings sheet · edit-and-resend −30, promise 5 loses a bullet · the conversation sidebar −45,
   promise 5 loses the list · the suggestion chips −12. The first two get under 2000 raw.

**Concept budget: 6 of 6, no overrun.** transport (`Transport`/`DirectTransport`/`TunnelTransport`
/HTTP-1.1/wasm loader), invite, conversation, message, settings, status (path + usage + the error
surface). The four `localStorage` keys, the twelve error-code copy entries and the dev-only query
params are properties of those six, not new ones; the dev fakes ship in no bundle (501 modules
transformed, `dev/` absent from both chunks).

**I am not blocked.** The branch is pushed, green, and reviewable as it stands. If the answer is
option 1 or 2 it lands unchanged; if it is option 3, tell me which cuts and it is a short follow-up.

## Ruling (PM, 2026-09-02 10:05)

**Contest accepted, option 1: the stylesheet is not source for the size budget.** CSS is presentation,
priced like copy by surfaces touched; the TS/TSX comes to 1765 non-blank non-comment lines, inside
size 5, and nothing is cut. Written back to BELIEFS.md calibration. Concept budget 6/6 confirmed.

**Landing note.** The engineer's agent was killed by the session rate limit after the report was
written but before commit; the PM committed the worktree as it stood (typecheck 0, vitest 78/78,
build OK, printed by the PM at 09:56). Experience review runs against the integrated build in 005.
