---
id: 029
title: CLI observability — status --watch, a per-request stream on the terminal, tunnel/engine counters
kind: normal
size: 2
status: dispatched
updated: 2026-09-03
release: demo-1
---

# 029 — CLI observability (plain terminal, no TUI)

**Why.** Founder (2026-09-03): a host or bridge operator needs good observability from the command
line; a TUI is unnecessary, printing is fine. Today `status`, `usage`, and `keys list` exist and are
good snapshots, and the JSONL usage log is the durable record. What is missing is a live view, a
request stream, and telemetry from the two layers under the gateway (tunnel, engine).

**Promises.**
1. **`status --watch [--interval 1s]`**: the `status` block redrawn in place each interval (plain
   ANSI clear, no library), plus a scrolling line per completed request beneath it:
   `12:01:05  alice  chat  gemma-4-E2B  38→412 tok  ttft 61ms  4.1s  ok` — or `429 rate_limited`,
   `503 queue_timeout`, `client_closed`. Sourced from the admin socket (an event stream endpoint,
   `GET /events`, that tails the recorder). Ctrl-C exits cleanly.
2. **`serve --log-requests`**: the same per-request line on the serve terminal itself (off by default:
   the banner stays quiet). Never prompt content.
3. **Tunnel telemetry** on the admin socket and in `status`: connected sessions (distinct client keys),
   per session: path (direct / relayed via <region>), handshake ms, bytes in/out, age; totals. Sourced
   from the tunnel wrapper (`internal/tunnel`), which today only counts open port-80 conns.
4. **Engine telemetry** in `status`: slot occupancy now and peak (from the gateway's queue + settle),
   tokens/s over the last minute, engine memory when the engine exposes it (llama.cpp `/metrics`;
   vLLM `/metrics`), and the engine's own "busy" count if it reports one.
5. **Bridge mode** (026): `connect` prints its own path/latency line on change and supports the same
   `--log-requests`; `status` against a bridge data dir shows the bridge's session and its request
   counters. (Land the hooks in 026's interfaces; the printing lives here.)
6. **Evidence:** a recorded `status --watch` transcript during a `hack/load` run at N=12 (028);
   goroutine/RSS counters visible in `status` so 028 can read them; tests for the event endpoint and
   the line formatter.

**Size 2** (≤400 lines). Concept budget 1 (the event stream). **Scope:** `cmd/bunny-network/status.go`,
`serve.go`, `internal/admin/**`, `internal/tunnel/**` (telemetry only), `internal/gateway` for the
counters it already has (read-only accessors), tests. Coordinate with 028 (shares the counters) and
026 (bridge hooks): dispatch 029 after 026's interfaces exist, or in the same lane.

## Log

All times 2026-09-03, EDT, laptop.

- 12:05 — ACK. Lane `t029-observability` on main `0e3f64c` (= origin/main; 026 `connect` landed). Read BELIEFS, ARCHITECTURE, 029, the 026/028/003/009 records, and the seam code. Own host will run on `--dev-listen 127.0.0.1:6860` under `…/tmp/bn029-data`; the founder's host (9091, `…/tmp/bn-founder`) is read-only for a transcript.
- Premise check before editing (no contest on shape): the gateway already exposes `Queue()` exact and `AllCounters()`; the tunnel's `tailcat.Server.Status()` (tailcat.go:1929) returns an `ipnstate.Status` with one `PeerStatus` per client key — `Relay` (region code), `CurAddr` (set only on a direct path), `RxBytes`/`TxBytes`, `LastHandshake`, `Active` — so sessions need no tailcat change. Engine telemetry reaches the engine through the existing seam (`Engine.Do` GET `/metrics`, probe-bounded), so `internal/upstream` stays untouched. Live `/metrics` today: llama.cpp b9553 offers `requests_processing`, `requests_deferred`, `predicted_tokens_seconds` and **no memory metric**; vLLM 0.25 offers `process_resident_memory_bytes`, `num_requests_running/waiting`, `kv_cache_usage_perc`. Memory will show where offered and `—` where not.
- Two things the ticket words cannot be measured on the host side, said now rather than faked: (1) "handshake ms" as a latency — tailcat registers a client at its meow (`onMeow`, tailcat.go:1362) without a timestamp and answers at once; the host can only see when the current WireGuard handshake happened (`LastHandshake`), so the session line shows that age (what `tailscale status` shows too) and the latency stays with the side that measures it (`connect`'s dial, 028's instrument). (2) Peak slot occupancy: the queue has no high-water mark and the ticket forbids more than a read-only accessor, so the peak is sampled at 1 Hz beside the queue's exact `now` — a burst shorter than a second can pass under it; the JSON says `slots_peak_sampled`.

## Report
