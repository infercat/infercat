---
id: 026
title: `connect` — the host binary as a client: local OpenAI-compatible endpoint over the tunnel
kind: normal
size: 2
status: queued
updated: 2026-09-03
release: demo-1 (founder: queue it; in the launch if it lands before the post)
---

# 026 — `bunny-network connect <invite> --listen 127.0.0.1:11435`

**Why.** Today the only client is the browser. A native client makes "any OpenAI-compatible app" true
on day one (Open WebUI, Cursor, Claude Code, the OpenAI SDK, curl), goes **direct** after the handshake
(a native process can hole-punch; the browser cannot), and bridges two machines with no browser at all.
It is exactly what LM Link's users asked for (docs/MARKET-LMLINK.md: "any client, not just LM Studio").

**Promises.**
1. `connect <invite> [--listen ADDR]` decodes the invite, opens the tunnel (one session, reused),
   verifies with `GET /me`, and listens locally. Every local request is forwarded over the tunnel with
   `Authorization: Bearer <secret>` injected; the client's own Authorization header is ignored. Streaming
   is relayed with immediate flush (reuse the gateway's relay discipline, not `httputil` buffering).
2. Startup prints, in this order: host name and model(s), the path (`direct · 0.8 ms` or `relayed via
   <region> · N ms`, re-checked every 30 s and printed on change), the local URL, and the one-line
   "set your app's base URL to http://127.0.0.1:11435/v1, any API key". `/me` is served locally too.
3. Errors are the gateway's, mapped to the friend's words: paused, revoked, host asleep (with the same
   15 s bound as the web client), rate limited with Retry-After, busy (queued keepalives pass through).
   Reconnects on its own with backoff when the session drops; never hangs on a dead session.
4. Evidence: tests for injection, streaming flush, error mapping; a live run where the OpenAI Python SDK
   and `curl` complete a streamed chat through the tunnel, and `tailcat ping --until-direct` shows the
   path went direct; `hack/measure.sh` gains a `connect` mode and its numbers go into docs/MEASURE.md.

**Size 2** (≤400 lines). Concept budget 1 (the client mode). **Scope:** `cmd/bunny-network/connect.go`,
`internal/tunnel` (client side: `Dial(invite)` returning a session), tests, README "Quickstart (friend
with an app)". Not `internal/gateway`, not `web/**`.

## Log

## Report
