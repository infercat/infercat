---
id: 026
title: `connect` — the host binary as a client: local OpenAI-compatible endpoint over the tunnel
kind: normal
size: 2
status: dispatched
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

All times 2026-09-03, EDT, laptop.

- 03:24 — ACK. Lane `t026-connect` rebased onto main `3eb9033` (the PM's dispatch commit; the worktree had been cut one commit earlier). Scope as ticketed: `internal/tunnel` client side, `cmd/bunny-network/connect.go`, tests, `hack/measure.sh` connect mode, README quickstart, MEASURE numbers. Own host will run on `--dev-listen 127.0.0.1:6810` under `…/tmp/bn026-data`; connect on 11435.
- Premise check before editing: tailcat's `Client.Ping` returns at once after the first meowed ack (`meowWait` is closed by a `sync.OnceFunc`, tailcat.go:1607), so it cannot re-register with a restarted host; a reconnect is a new `tailcat.Client` under the same node key. `DiscoPing` (tailcat.go:1802) is the path measurement that also nudges NAT traversal — what `tailcat ping --until-direct` loops on. No contest on shape: the ticket's design fits.
- 03:28 — First cut: `internal/tunnel/client.go` (`Dial`/`Open`/`Path`/`Redial`/`Close`) and `cmd/bunny-network/connect.go`. One `tailcat.Client` per host; `Dial` retries the meow under the caller's ctx the way the wasm bridge does; `Path` is a disco ping (it also nudges NAT traversal, which is what `tailcat ping --until-direct` loops on); reconnect is a new client under the same node key. The tunnel never sees the secret: `Dial` takes the address, the command holds the key.
- 03:31 — Silence rule: probe, don't assume. The ticket's 15 s is honoured at the dial (a dead session's dial is cut at 15 s → 503 `host_asleep`, Retry-After 30) and as the silence threshold (15 s without a byte → `GET /me`, 10 s); only a host that does not answer `/me` ends the request (`host_asleep` before the head, `host_stalled` + `[DONE]` inside a stream). Reason: the gateway does not write a stream's head until the engine's first byte (proxy.go pipeStream), and an OpenAI-compatible app — Claude Code, Cursor — sends 50–100K-token prompts whose prefill alone passes 15 s on a home GPU; a hard bound would call every one of them "asleep". The web client can afford the hard bound because its prompts are chat-sized. Both branches tested.
- 03:36 — Tests: 7 over a fake session on loopback TCP (injection, per-event flush, error words, asleep bound, silence probe both ways, path reprint + reconnect with backoff, the command's banner and refusals) and 1 over the in-process relay (`TestSessionOpenPathRedial`). Two fixes on the way: a fake host that never read the body never learned the client left (test-only); the terminal line said nothing when a stream ended with the gateway's error event (real; `pipeStream` now returns the event's code).
- 03:38 — Live, own host on 6810 (`…/bn026-data`, relay New York City), `connect` on 11435: banner in order; `path direct · 0.2 ms` at once (same laptop); curl `/v1/models`, `/me`, `/healthz` → 404 `not_found`; streamed chat TTFT 24 ms, `[DONE]` at 3.6 s, usage in the last chunk.
- 03:40 — OpenAI Python SDK 2.48 (venv under the job tmp): `models.list()`, streamed chat `finish_reason=stop` with usage, a non-stream `create`. A first attempt with the SDK's `.stream()` helper and `max_tokens=64` raised the SDK's own `LengthFinishReasonError` (Gemma spent the 64 tokens thinking; `finish_reason=length`) — the SDK's rule, not the relay's; the plain `create(stream=True)` path completes.
- 03:41 — `hack/measure.sh` gains `MODES` and a `connect` mode; one run, three paths: direct 35 ms / 168.3 tok/s · tunnel (`tailcat socks`) 37 / 164.7 · connect 36 / 164.5. `tailcat ping --until-direct`: `pong in 540µs via 192.168.199.132:53267`, direct on the first pong.
- 03:43 — Drill on the running pair: `keys pause` → 403 `key_paused` "your invite is paused — ask t026 host to resume it…"; `--rpm 1` → 429 `rate_limited`, `Retry-After: 9`, the words; SIGINT my host → curl 503 `host_asleep` at 15.0 s; `path lost — the host stopped answering; reconnecting` 28 s after the kill; a request meanwhile fails in 0 s with the same words; host restarted (same data dir) → `path reconnected · direct · 0.5 ms` 4 s later; GET 200.
- 03:44 — Accounting recomputed from the diff: **750 raw source lines against 400**. The first coherent cut disagrees with the price by 1.9×: contest below, with the map; the freeze carries the finished work so one ruling can land it.
- 03:46 — `git fetch`: main moved to `ebc1146` (033, pm-only); rebased clean. Handshake bound aligned to 033's 20 s. Full checks printed below. Own host and connect stopped; nothing else touched (028's load lanes, 030's host, the founder's host on 9090, both llama-servers all still up).

## Contest

**Size.** The four promises as written cost **750 raw source lines** (added + deleted, tests and help text excluded — the accounting convention of 018/024); the ticket prices them at ≤400. Measured map:

| Piece | Raw lines | Promise |
|---|---|---|
| tunnel client side, `internal/tunnel/client.go` (Dial / Open / Path / Redial / Close) | 110 | 1, 2 |
| `connect.go`: imports, bounds, session seam | 56 | — |
| `connect.go`: the command — flags, invite, loopback listen, `/me` verification, banner, serve, shutdown | 77 | 1, 2 |
| `connect.go`: the relay — outbound request, header copy, stream pipe with per-event flush, error-event rewrite, error response, terminal line | 205 | 1, 3 |
| `connect.go`: the friend's words (`hostError`, table, fill) | 55 | 3 |
| `connect.go`: silence rule — watched conn, `/me` probe, bounded fetch, `me` | 113 | 3 |
| `connect.go`: path loop, reconnect with backoff, path words | 87 | 2, 3 |
| `main.go` + `wire.go` wiring (help lines excluded) | 10 | 1 |
| `hack/measure.sh` connect mode | 29 | 4 |
| **Source total** | **750** | |

What could come out and what it costs: the terminal line per request (−20; then an SDK user's 403 has no trace on their screen); the words for the eight codes the ticket does not name (−8; `rate_limited`, `key_paused`, `key_revoked`, `host_asleep`, `queue_timeout` stay); the silence probe replaced by a hard 15 s head bound (−70; every long-prefill request from Claude Code / Cursor becomes "asleep" — argued against in the Log); reconnect without backoff, i.e. fail every request until the friend restarts `connect` (−50; promise 3 says reconnect). All four cuts together reach ~600, still 1.5× the rung. The floor for the promises as written is size 3.

**Ask:** re-price to **size 3 (≤900)** and land the freeze as is — or name the cut. Concept budget: 1 used (the `connect` mode); `--listen` is in the ticket's signature; `--verbose` is `serve`'s flag reused; `host_asleep` / `host_stalled` are the web client's codes reused on the local wire (`web/src/api.ts`), not new ones — if the PM counts them, that is +2 over budget and I need a ruling.

## Report

**Core.** `bunny-network connect <invite>` is a working OpenAI-compatible client of any host, over one tunnel session, with the friend's words for everything the host says. Live, on the running pair (own host, this laptop):

```
$ bunny-network connect bn1.tco2Fw….…
connecting to the host through its relay…
Bunny Network 0.0.1-dev
host      t026 host  ·  gemma-4-E2B-it-Q4_K_M.gguf
path      direct · 0.2 ms
local     http://127.0.0.1:11435
          set your app's base URL to http://127.0.0.1:11435/v1, any API key
03:39:43  POST /v1/chat/completions  3.7s
```
- curl, streamed: TTFT 24 ms, 7 content chunks, usage `22 / 566`, `[DONE]` at 3641 ms. OpenAI Python SDK 2.48: `models.list()`, streamed chat `finish_reason=stop`, usage `27 / 147`, text `red, blue, green`; non-stream `create` too. `hack/measure.sh` (three modes, one run): direct 35 ms / 168.3 tok/s · tunnel 37 / 164.7 · **connect 36 / 164.5** — in `docs/MEASURE.md`.
- `tailcat ping --until-direct --timeout=30s <addr>` → `pong in 540µs via 192.168.199.132:53267` (direct on the first pong; same machine, so the relayed→direct transition could not be shown live — `TestConnectReprintsThePathAndReconnectsWithBackoff` pins the reprint).
- Words and the dead host, live: `keys pause` → `403 key_paused "your invite is paused — ask t026 host to resume it, then try again (host said: this key is paused by the host)"`; `--rpm 1` → `429 rate_limited`, `Retry-After: 9`; host killed → `503 host_asleep` at **15.0 s** with `Retry-After: 30`; `path      lost — the host stopped answering; reconnecting` 28 s after the kill; a request meanwhile → 503 in 0 s; host back → `path      reconnected · direct · 0.5 ms` in 4 s; GET 200. Full transcript: `…/tmp/bn026-drill.out`.

**Edges seen.** App's own `Authorization` dropped, the invite's injected; hop-by-hop headers stripped both ways; chunked and length-framed request bodies; `/healthz` and everything outside `/v1/*`, `/me` → 404 locally, never over the tunnel; the app leaving mid-stream (`client_closed`, tunnel conn freed); a gateway error event inside a 200 stream rewritten with code/type/`retry_after` intact and followed by `[DONE]`; a non-error `data:` line never parsed; a silent host mid-stream → `/me` probe, alive keeps waiting, dead → `host_stalled` event + `[DONE]`; a request during a reconnect fails at once instead of dialling a corpse; two failed path checks in a row (or a hung dial) before a session is declared lost, so one lost ping is not a reconnect; backoff 1→2→4…→30 s; redial keeps the node key; `--listen` refuses non-loopback before dialling; a revoked/unknown invite exits 1 with the words, a paused one connects and says so on the banner; the secret is never printed or logged.

**Verification.** `go build ./... && go vet ./... && go test ./...` on the rebased lane (`ebc1146`): **150 passed / 0 failed / 2 skipped** top-level (the two skips are the pre-existing opt-in live tests `TestLiveLlamaCPP`, `TestSavedAddrLive`), 105 subtests passed / 0 failed. New: 7 `TestConnect*` (cmd) + `TestSessionOpenPathRedial` (tunnel, in-process relay: handshake, 3 dials with no re-handshake, path, redial keeps identity, stopped host fails inside a 3 s bound). Manual: the live runs above, each printed to a file under the job tmp.

**Accounting** (raw added / deleted from `git diff origin/main...HEAD`, at the freeze commit):

| Bucket | Budget | Measured | Verdict |
|---|---|---|---|
| Go source (`internal/tunnel/client.go` +110, `cmd/bunny-network/connect.go` +601 excl. 22 help lines, `main.go` +3 excl. 2 help lines, `wire.go` +7) | part of ≤400 | **721** | over — contested |
| Shell source (`hack/measure.sh`) | part of ≤400 | +18 / −11 = **29** | |
| **Source total** | **≤400** | **750** | **over 1.9× — see Contest** |
| Go tests (`connect_test.go` +558, `client_test.go` +104) | — | 662 | 8 tests |
| Docs (`README.md` +32/−2, `docs/MEASURE.md` +17), help text (24 lines) | surfaces | — | |
| Ticket record | — | Log + Contest + Report | |
| Dependencies | none | 0 added; `tailscale.com/types/key` already a dependency | |

**Concepts: 1 budgeted, 1 used** (the `connect` mode). Reused, not counted: `--listen` (in the ticket's signature), `--verbose` (serve's), `host_asleep`/`host_stalled` (the web client's codes). No new state file, config key, or route.

**Declared.** Live actions: own host started/stopped on 6810 with data dir `…/tmp/bn026-data` (key `friend` minted; its invite in `…/tmp/bn026.invite`, mode 0600); `connect` on 11435, stopped; `pip install openai` into `…/tmp/bn026-venv`. Nothing else was started, stopped, or reconfigured. Intended, not bugs: the head of a *non-stream* request is waited for as long as `/me` keeps answering (a non-stream reply of 4096 tokens takes minutes and the gateway writes its head last); the banner prints `path unknown — the host did not answer a ping` rather than guessing when the first disco ping fails; `tunnel.Dial` takes the address, not the invite, so the secret never enters the tunnel package.

**Adjacent, not fixed.** (1) `hack/measure.sh` prints `Terminated: 15` from the trap killing `tailcat socks` at exit (pre-existing). (2) The gateway's `/me` reports `host.relay.region` as the DERP map's name ("New York City") while the web client translates codes ("New York"); the CLI shows the host's word. (3) A `serve` on loopback with `--dev-listen` and a `connect` on the same machine both work; nothing stops a friend from running `connect` twice on two ports under two identities — the host counts two clients, as the web app's second tab does.
