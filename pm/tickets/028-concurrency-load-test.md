---
id: 028
title: Load test every layer — relay, tunnels per host, gateway, engine — and publish the limits with numbers
kind: investigation
size: 3
status: queued
updated: 2026-09-03
release: demo-1
---

# 028 — Load, layer by layer

**Why.** Founder (2026-09-03): concurrency must be reliable and accounted for, and "there are multiple
components that would benefit from load testing." A request crosses four of them, each with its own
ceiling and failure shape; a launch-day spike hits the first two before the engine sees a byte. The
gateway/engine layer is designed and unit-proven; the relay and tunnel layers have never been loaded
by us at all. Be as thorough as the budget allows: one script family, four layers, the same instrument.

## The layers, and what each is tested for

| Layer | Component | Ceiling to find | Failure shape to watch for |
|---|---|---|---|
| 1 | **Relay** (`derp.2185lab.com`, 2 vCPU/4 GB, DERP + STUN) | concurrent client connections; relayed bytes/s; new-connection rate | CPU/memory on the box; dropped/duplicated packets (`derp_packets_dropped` in varz); handshake latency under load; STUN under a burst; what a second host on the same relay does to the first |
| 2 | **Tunnel per host** (the host binary's tailcat server, one netstack) | concurrent friend sessions; new dials/s; conns per session | goroutines and memory per session (must not grow after close); the 1024-parked-conn listener cap; handshake latency at N sessions; direct vs relayed mix; wasm (relay-only) friends vs native (direct) friends |
| 3 | **Gateway** (admission, per-key concurrency, FIFO queue, keepalives, settle) | already designed: S running, ≤2S waiting, rest 503 | proof under REAL clients: every exit releases every resource (goroutine/memory flat after a run); queue fairness under sustained pressure; keepalive writes under 30 waiting streams; a dead reader cut within the write deadline; correct codes at every N |
| 4 | **Engine** (llama.cpp 2 slots; vLLM at its max-num-seqs) | the engine's real parallelism and its per-slot context budget | llama.cpp "no slot available" after we admitted (must surface as busy, never raw 502); `-c`/`-np` shared context causing `context_too_long` below the advertised context; engine memory growth; tok/s per stream at N; vLLM batching behaviour |

## Binding

1. **Instrument.** `hack/load/` (Go): N simulated friends, each with its own key, each holding its own
   tunnel session (native, via the tailcat client library — the same path the future `connect` command
   uses — plus a `--browser-like` mode that pins to relay-only so wasm friends are represented),
   sending streamed chats in a loop for M minutes. Per request: key, status, code, queued_ms, ttft_ms,
   tok/s, bytes, path (direct/relayed). Sampled every second: host `status` (in-flight/waiting), host
   process RSS and goroutines (expose `/debug/vars`-style counters on the admin socket if needed —
   that is the one product change allowed, size 1, contest first), engine memory, relay varz
   (`derp_accepts`, `derp_clients_total`, `derp_bytes_*`, `derp_packets_dropped`, CPU/mem via ssh).
2. **Runs, ascending:** N = 2, 6, 12, 30, then 60 and 100 for layers 1–2 only (friends idle-connected
   plus a trickle of requests — the question there is sessions, not inference). For each N: layer-1
   metrics with all friends pinned to `derp.2185lab.com` and separately to Tailscale's NYC relay
   (comparison for the founder's relay decision); layer-2 metrics on the host; layers 3–4 against (a)
   the laptop llama-server (2 slots) and (b) the workstation vLLM over the read-only ssh forward
   (`--slots` = its real max-num-seqs; requests only — never modify that machine).
3. **Pathological cases**, each run once at N=12: a friend who never reads its stream; a friend sending
   4 MiB bodies; a friend with `max_concurrent 3` bursting; 30 friends connecting in the same second
   (launch-day spike); the engine restarted mid-run; the relay restarted mid-run (sessions must
   re-establish; nothing hangs); a friend that opens 50 tunnel sessions with one key (abuse shape).
4. **Named failure modes to prove absent or ticket:** any request that neither completes nor errors
   within its deadlines; host goroutines/RSS higher after a run than before (leak); relay packet drops
   at any N we intend to support; handshake latency > 5 s at the supported N; raw 502 for an engine
   "busy"; `context_too_long` below the advertised context.
5. **Deliverables:** `docs/MEASURE.md` "Load" section (a table per layer per N, with the sample
   commands); **`docs/LIMITS.md`** (new, one page, plain words): what one relay supports, what one host
   supports, what the gateway guarantees ("with an engine that serves S at once: at most S run, ≤2S wait
   up to 30 s, the rest are told to retry; per friend one at a time by default"), what each engine
   sustains; the recommended public-demo engine and its slot setting; and the relay-sizing rule
   (bytes/s per browser friend → friends per droplet). Every defect found → its own ticket with a
   repro. Any layer whose ceiling is below launch-day need → flagged at the top of the report.

**Size 3** (investigation + the instrument; ≤900 lines under `hack/load/`). Concept budget 0 (the
optional admin counters are the one allowed product change, contested first). **Scope:** `hack/**`,
`docs/**`; `internal/admin/**` only for counters, by contest. Ports 63000–63999. Relay: our own box
(ssh root@206.189.207.168) — read varz and `top`; you may restart derper for the restart case and must
leave it running. The demo host stays on Tailscale's relay throughout (founder rule).

## Log

## Report
