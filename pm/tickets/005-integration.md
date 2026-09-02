---
id: 005
title: Integration — wire, run end to end, measure, fix what breaks
kind: sensitive
size: 3
status: draft
updated: 2026-09-02
release: demo-1
---

# 005 — Integration

## Binding

**Why.** Four tickets built four pieces against a contract. This ticket proves the contract held: a
friend key minted on the host, a browser pasting the invite, tokens streaming through the relay, a rate
limit firing, a revoke taking effect. Nothing here adds features; it makes the existing promises true
together.

**Promises.**
1. Wiring flipped: `cmd/bunny-network/wire_stub.go` deleted, `wire` build tag removed from `wire.go`,
   `go mod tidy` run, `make check` green from a fresh clone (law 4).
2. `bunny-network serve --upstream http://127.0.0.1:18080 --dev-listen 127.0.0.1:9090 --name "Max's laptop"`
   prints the real tunnel address and relay region; `keys add alice` prints an invite that
   `internal/invite.Decode` and the web client's parser both accept.
3. Web client Direct mode (`VITE_DIRECT_URL=http://127.0.0.1:9090`) completes a streamed chat with
   the Thinking block rendering Gemma's `reasoning_content`, then the answer.
4. Web client Tunnel mode (built bundle served statically, wasm loaded) completes the same chat through
   the public relay. Status pill shows `relayed via <region> · <rtt> ms`. `/me` usage bar updates.
5. Limits: a second key with `--rpm 2` gets a 429 with a countdown on the third request; `keys revoke`
   on it returns the client to the connect screen with the revoked message while alice keeps chatting.
   `keys pause` and `resume` observed. `status` and `usage` show both keys correctly.
6. Queue: with `--slots 1`, two simultaneous chats from two keys → the second queues (observed in
   `status` waiting count) or gets 503 `queue_timeout` if `--queue-timeout 1s`.
7. vLLM: `serve --upstream http://127.0.0.1:8010` (via the read-only ssh forward) completes one streamed
   chat in Direct mode; `/tokenize` exact counting used (log line or `/me`).
8. Measurements in `docs/MEASURE.md`: TTFT and tokens/s for the same prompt via Direct (loopback) vs
   Tunnel (relay), 3 runs each, from a script `hack/measure.sh` that anyone can rerun.
9. Every defect found is fixed on this branch **within the scope contracts of the owning ticket** and
   noted here with file:line and the ticket it belongs to; anything larger is contested, not patched.

**Size 3** (≤900 source lines of fixes; expected far less). Concept budget 0: no new concepts.
**Sensitive** (touches gateway/keys paths while fixing).

**Scope contract.** Any file, because integration fixes cross tickets — but each fix names the ticket
whose contract it lives under, and no fix may widen an interface without a contest.

**Evidence.** Terminal transcripts of 2, 5, 6, 7; Playwright screenshots of 3, 4, 5 (connect screen with
real invite redacted, mid-stream with Thinking block, 429 countdown, revoked state, status pill);
`hack/measure.sh` output; `make check` from a fresh clone printed.

## Background

- Dispatched only after 002 and 004 land. 001 and 003 are on main.
- Local llama-server: `127.0.0.1:18080`, Gemma 4 E2B, `-np 2`, per-slot ctx 4096 (see MEASURE.md).
- The Playwright screenshot script from 004 (`web/dev/screenshots.mjs`) is the starting point for the
  Tunnel-mode captures; extend, do not duplicate.

## Log

## Report
