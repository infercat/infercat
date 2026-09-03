---
id: 028
title: Load test every layer — relay, tunnels per host, gateway, engine — and publish the limits with numbers
kind: investigation
size: 3
status: dispatched
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

## Founder amendment (2026-09-03 02:10): realistic context and message sizes

The 4K context tonight was a test artifact (`-c 8192 -np 2` → 4K per slot); the laptop engine now
runs `-c 65536 -np 2` (32K per request; Gemma 4 E2B's native context is 128K). A bigger context makes
the **prompt** the heavy direction: every turn resends the history, so a mature chat sends 10–20K tokens
(40–80 KB) per message up the tunnel. Tonight's numbers were all short prompts and measured almost
nothing about the uplink. Three components are untested at that size: the relay (browser friends are
relay-only), the wasm HTTP client (hand-serialized request bodies), and the gateway's tokenize pre-check
(a full-prompt round trip to the engine before admission). Layer 4 changes too: prefill of a 20K prompt
on llama.cpp is seconds, so TTFT climbs and slots are held longer — exactly when queue + keepalives matter.

**Message mix (binding; no 100-token pings):** each simulated friend runs a growing conversation —
turn 1 a ~300-token question, history accumulating so prompts climb through 2K, 8K, 16K tokens by
turn ~8; 1 in 5 turns pastes a document (4–12K tokens); replies requested at 300–2,000 tokens. Report
per-turn prompt size alongside every latency number, and report **uplink bytes/s per friend at the
relay** as a first-class metric — it is the relay-sizing input. Run the mix at N = 2, 6, 12, 30 for
inference; note where the engine's context wall (`context_too_long`) or the gateway's shrink-to-fit
kicks in and whether the copy the friend sees is true.

## Founder question the report must answer (2026-09-03 02:25; NOT for the demo)

Resending the conversation prefix every turn is stateless but costs (1) relay bytes (grows with chat
length, matters only on the relayed path), (2) engine prefill (the real cost; llama.cpp reuses a slot's
prompt cache only when the same chat lands on the same slot; vLLM prefix-caches across requests), and
(3) nothing on the privacy side — the client owns history, the host stores no content (BELIEFS
Protection 3). A responses-style stateful API would cut (1) and (2) but invert (3) and couple the host
to one engine's cache model. **Decision rule (founder):** do not optimize what is not a problem. The
report must state, from the realistic mix: relay bytes/s per long-conversation friend (the sizing
number); measured prefill/TTFT vs prompt size per engine, and how much llama.cpp's slot cache and
vLLM's prefix cache actually recover at N friends; and a recommendation among (a) stateless as is,
(b) stateless + prefix-hash slot routing / cache hints in the gateway (keeps the client as owner of
history; no host storage), (c) a stateful conversation API — with the numbers that would justify each.

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

- 2026-09-03 07:15 EDT — ACK. Base `3eb9033` (main), lane `t028-load`. Read BELIEFS, ARCHITECTURE, DESIGN §1/§3, MEASURE, measure.sh, gateway queue/request, tunnel, tailcat v0.4.0 client API.
- 07:20 — Environment: laptop llama-server 18080 up (`-np 2 -c 65536`, per-slot n_ctx 32768, `/metrics` + `/slots` on); vLLM forward to 8010 was down → started `ssh -N -L 8010` in the background (requests only); relay `derp.2185lab.com` idle (2 vCPU/4 GB, derper 12 goroutines, load 0.0); founder's demo host is PID 13765 on the default data dir with `--dev-listen 127.0.0.1:9090` (the brief said 9091/bn-founder; either way untouched).
- 07:25 — **CONTEST (the one allowed product change), then proceeding under the ticket's explicit allowance:** the named failure mode "host goroutines/RSS higher after a run than before" needs a goroutine count, and a Go process on macOS exposes none externally (no `/proc`; `ps` gives RSS only; SIGQUIT dumps and exits). Taken: `GET /debug/vars` on the admin socket (`internal/admin/admin.go`: `Vars{goroutines, heap_alloc, heap_sys, sys, num_gc}` from `runtime`, same auth as `/status`, plus `FetchVars`; one test). Read-only, host-side, never through the tunnel. ~35 lines; trivially revertible if declined.
- 07:30 — Relay-only emulation for browser friends: tailscale's magicsock honours `TS_DEBUG_ALWAYS_USE_DERP` ("disables the use of UDP, forcing all peer communication over DERP"), set at runtime with `envknob.Setenv` before the first client starts. Process-wide, so `--relay-only` applies to every friend in one `load` process; native vs relay-only is two runs, not a mix inside one.
- 07:30 — Engine restart: both real engines are requests-only, so that case runs against a private llama-server I start on a 63xxx port with the same model and restart myself. The founder's host and my hosts share 18080, so "two hosts on one engine" is a real shape here, measured rather than avoided.

## Report
- 04:25 — Runs R1–R11 clean. Two product/dependency defects filed: **035** (tailcat.Client.Close hangs under relay-only — instrument-guarded, affects 026's connect on a permanently-relayed native client) and **036** (context pre-check undercounts the chat-template prompt → vLLM rejects a near-ceiling request as a raw 400 instead of the gateway's 422; measured token gap + vLLM's own message). Gateway held `in_flight` max 2 / `waiting` max 4 in every run; 0 requests past deadline; host RSS 37–83 MB bounded. WIP commit here.
