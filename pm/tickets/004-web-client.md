---
id: 004
title: Web client — tunnel fetch, connect screen, streaming chat
kind: normal
size: 5
status: dispatched
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

## Report
