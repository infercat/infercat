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

- 03:56 — ACK. Lane `t029-observability` on main `0e3f64c` (= origin/main; 026 `connect` landed). Read BELIEFS, ARCHITECTURE, 029, the 026/028/003/009 records, and the seam code. Own host will run on `--dev-listen 127.0.0.1:6860` under `…/tmp/bn029-data`; the founder's host (9091, `…/tmp/bn-founder`) is read-only for a transcript.
- Premise check before editing (no contest on shape): the gateway already exposes `Queue()` exact and `AllCounters()`; the tunnel's `tailcat.Server.Status()` (tailcat.go:1929) returns an `ipnstate.Status` with one `PeerStatus` per client key — `Relay` (region code), `CurAddr` (set only on a direct path), `RxBytes`/`TxBytes`, `LastHandshake`, `Active` — so sessions need no tailcat change. Engine telemetry reaches the engine through the existing seam (`Engine.Do` GET `/metrics`, probe-bounded), so `internal/upstream` stays untouched. Live `/metrics` today: llama.cpp b9553 offers `requests_processing`, `requests_deferred`, `predicted_tokens_seconds` and **no memory metric**; vLLM 0.25 offers `process_resident_memory_bytes`, `num_requests_running/waiting`, `kv_cache_usage_perc`. Memory will show where offered and `—` where not.
- Two things the ticket words cannot be measured on the host side, said now rather than faked: (1) "handshake ms" as a latency — tailcat registers a client at its meow (`onMeow`, tailcat.go:1362) without a timestamp and answers at once; the latency belongs to the side that measures it (`connect`'s dial, 028's instrument). (2) Peak slot occupancy: the queue has no high-water mark and the ticket forbids more than a read-only accessor, so the peak is sampled at 1 Hz beside the queue's exact `now` — a burst shorter than a second can pass under it; the JSON says `slots_peak_sampled`.
- 04:09 — **Premise correction, found by the first live test, not by reading:** `tailcat.Server.Status()` returns *no peers at all* — its `ipnstate.StatusBuilder` never sets `WantPeers` (tailcat.go:1424–1430; `Peers = []` on a live session in `TestSessionOpenPathRedial`), and nothing exported reaches the engine or magicsock. So per-key path, WireGuard handshake and WireGuard bytes are not visible to a host on tailcat 0.4.0 without reflection into a dependency's private fields, which I did not do. Promise 3 is delivered at our own layer instead: the listener meters every client by its tunnel address (tailcat derives it from the node key, so it is the client's identity) — open conns, TCP-payload bytes both ways, first seen, last byte, active (a byte in 2 min). The path, RTT and handshake time are measured where they can be — by `connect` for its one session — and the host's block says once that paths are the client's to measure. A one-line tailcat change (`sb.WantPeers = true`) would give the host the WireGuard view; adjacent, below.
- 04:11 — First cut committed: `admin.Events` (a `usage.Recorder` in front of the file recorder: fan-out to subscribers, prompt text stripped, a minute of completions for tok/s), `GET /events` (NDJSON, one line per event, ends with the host), `admin.Watch`; `status --watch [--interval]` (ANSI clear-and-redraw, 20 request lines beneath, Ctrl-C clean, a host that stops answering is said and retried); `serve --log-requests`; `connect --log-requests` and `connect --data-dir` (an admin socket for the bridge: `status`/`status --watch` against it show the host, the path with RTT and handshake time, what is in flight, its request stream); sessions, engine (`/metrics` through the seam: llama.cpp busy/waiting, vLLM busy/waiting/memory/KV-cache) and process (goroutines, heap, sys, RSS on Linux) in `/status` and in the block. Tests: 8 new + Peers assertions in the tunnel's session test. Build/vet/test green.
- 04:13 — Live, own host on 6860 (`…/bn029-data`, four keys) + `connect` on 11436 with `--data-dir …/bn029-bridge`: drill 1 (four streams on two slots, a 429) — every promised line shape appeared; the bridge's `status` shows `direct … · 0.2 ms (handshake took 107 ms)`.
- 04:19 — **Defect found live and fixed with its fixture:** while `status --watch` ran, the host's goroutine count climbed by one per poll (139 → 427 over five minutes) and fell back when the watch stopped. Cause: `admin.dial` builds a throwaway `http.Client` per call and its kept-alive unix socket sat on the host as one connection goroutine per poll. Fix: one socket per admin call, closed with it (`DisableKeepAlives`), plus `IdleTimeout` 30 s on the admin server. `TestFetchLeavesNothingBehind` fails on the pre-fix code (checked by reverting the one line) and passes after; live re-measure: 142 → 144 during 30 polls → 142 after.
- 04:20 — Drill 3 on the fixed binary: the transcript in the Report (five streams of ~1000 tokens on two slots → `queued 4.0s / 6.5s / 10.9s`, a `client_closed` cut at 4.0 s, a `429 concurrency_limited`, `engine says 2 busy`, `peak 2`).
- 04:22 — Accounting from the diff: **891 raw source lines, 867 excluding help text, against 400.** Contest below with the map; the freeze carries the finished work. `git fetch`: main moved to `7822fa7` (handoff, pm-only); rebased clean. Own host and bridge stopped; nothing else touched.

## Contest

**Size.** The six promises as written cost **867 raw source lines** (added + deleted, tests and help text excluded — the accounting convention of 018/024/026); the ticket prices them at ≤400. Measured map:

| Piece | Raw lines | Promise |
|---|---|---|
| admin: the event stream — hub (`events.go`), `GET /events`, `Watch` client, close-once | 170 | 1, 2 |
| admin: `Status` shape — sessions, engine, process (types with their truth in comments) | 70 | 3, 4, 6 |
| admin: process stats, Linux RSS | 45 | 6 |
| tunnel: per-client metering in the listener (conns, bytes, first seen, last byte) | 101 | 3 |
| status.go: the `--watch` loop | 60 | 1 |
| status.go: the request line and its words | 48 | 1, 2, 5 |
| status.go: sessions / engine / process / bridge blocks, byte words | 108 | 3, 4, 5, 6 |
| serve.go: `--log-requests`, key names, hub wiring | 45 | 2 |
| serve.go: 1 Hz sampler (peak), `/metrics` fetch + parse, `buildStatus` | 92 | 4, 6 |
| connect.go: `--log-requests`, status writer, events, bridge status with handshake time | 110 | 5 |
| wire / main / sock_unix glue | 18 | — |
| **Source total** | **867** | |
| help text (surface, excluded) | 24 | |

What could come out and what it costs: the bridge's admin socket and block (−90; then `status` against a bridge data dir, the second half of promise 5, is gone); the engine's `/metrics` (−50; promise 4's busy/waiting/memory); process stats (−45; promise 6's goroutine counters, which 028 needs); the sessions table (−100; promise 3 entirely). All four cuts together reach ~580, still 1.45× the rung. The floor for the promises as written is size 3 — the same shape as 026: six promises across four commands and three packages were never a size-2.

**Ask:** re-price to **size 3 (≤900)** and land the freeze as is — or name the cut. Concept budget: 1 budgeted, 1 used (the event stream: `GET /events` + `admin.Events`). `status --watch` / `--interval`, `serve --log-requests`, `connect --log-requests` are the flags the promises name; `connect --data-dir` is the global flag reused; the new JSON fields are fields, not concepts; no new state file (a bridge's socket is the admin socket in the data dir it was given).

## Report

**Core.** A host (or a bridge operator) can now watch, from a plain terminal, what is happening and what just happened — and the numbers say where they come from. Live, own host on 6860 (llama.cpp on 18080, 2 slots) with `connect` on 11436 as one of the friends; five ~1000-token streams launched together, a second request from a one-at-a-time key, and one client cut at 4 s. The last frame of `status --watch --interval 1s` (a plain `ESC[H ESC[2J` redraw each second; 30 frames in 28 s):

```
Bunny Network 0.0.1-dev — up 27s
upstream  llama.cpp  http://127.0.0.1:18080  healthy  context 32768  slots 2
tunnel    tco2FwWCDsHh0eB5…FpGQEt  relay New York City  0 clients
sessions  1 active of 1 seen  ·  in 923 B  out 487 KB  ·  paths: the client's to measure, not visible to a host
  …39b5:bf0c  0 conns  in 923 B  out 487 KB  last byte just now  age 22s
queue     0 in flight, 0 waiting  (peak 2 in flight, sampled)
engine    72 tok/s over the last minute  ·  engine says 0 busy, 0 waiting  ·  memory not reported
process   145 goroutines  heap 3.2 MB  sys 19.8 MB  rss —

ID        NAME   STATUS  IN FLIGHT  RPM  TODAY  LAST SEEN
k_b9206d  alice  active  0          2    4960   just now
k_30b9c0  bob    active  0          1    2349   just now
k_322a48  carol  active  0          1    1813   just now
k_8ac1b7  dave   active  0          1    1995   just now

rpm counts model calls and the app's /v1/models lookup; tpm and tokens/day count model calls only

04:20:15  bob  chat  0ms  429 concurrency_limited
04:20:18  carol  chat  gemma  18→562 tok  ttft 62ms  4.0s  client_closed
04:20:21  bob  chat  gemma  33→917 tok  ttft 62ms  6.5s  ok
04:20:25  dave  chat  gemma  33→970 tok  queued 4.0s  ttft 4.1s  10.9s  ok
04:20:27  alice  chat  gemma  33→884 tok  queued 6.5s  ttft 6.5s  12.8s  ok
04:20:31  alice  chat  gemma  33→959 tok  queued 10.9s  ttft 10.9s  17.0s  ok
```
Mid-run (11 s in), the same block said `tunnel … 1 client` / `…39b5:bf0c  2 conns` / `queue 2 in flight, 1 waiting (peak 2 in flight, sampled)` / `engine says 2 busy, 0 waiting` / alice `IN FLIGHT 2`, dave `1`. The host's own terminal (`serve --log-requests`) printed the identical six lines as they happened. The bridge, `status --data-dir …/bn029-bridge` at the same moment:

```
Bunny Network 0.0.1-dev — up 11s  ·  bridge
host      t029 host  relay New York City  tco2FwWCDsHh0eB5…FpGQEt
path      direct 192.168.199.132:61121 · 0.3 ms  (handshake took 118 ms; session 11s old)
local     http://127.0.0.1:11436  2 in flight
```
and its terminal (`connect --log-requests`): `04:20:27  t029 host  chat  12.8s  ok` / `04:20:31  t029 host  chat  17.0s  ok`. `GET /events` on the host's socket, read with curl during the run, produced the same six events as NDJSON (`bn029-events3.ndjson`), none with a `prompt` field. Full artefacts under `…/tmp/bn029-*` (serve log, connect logs, watch frames, JSON snapshots).

**The JSON shape (`GET /status`), for 028 to read.** Unchanged fields kept their names; new ones:
```
mode: "host" | "bridge"            name: the host's display name (a bridge: the host it reaches)
tunnel.sessions: [ {key, path: "unknown"|"direct"|"relayed", via?, rtt_ms?, handshake_ms?, conns, rx_bytes, tx_bytes, since, last_byte, active} ]   (never null)
tunnel.rx_bytes, tunnel.tx_bytes   totals over sessions (host: TCP payload on port 80; in = from friends)
queue.in_flight, queue.waiting     exact, as before
engine: { slots_peak_sampled, tokens_per_s_1m, metrics, busy, waiting, memory_bytes, kv_cache_pct }
process: { goroutines, heap_bytes, sys_bytes, rss_bytes }
```
What 028 can read, and how each number is made: `queue.in_flight`/`queue.waiting` exact from the queue; `engine.slots_peak_sampled` is the highest `in_flight` seen by a 1 Hz sampler since start (a burst shorter than a second can pass under it — the queue has no high-water mark and a read-only accessor cannot add one; sample `/status` at 1 Hz and you get the same resolution); `engine.tokens_per_s_1m` = completion tokens of requests that *finished* in the last 60 s ÷ 60 (a long stream lands whole where it ended); `engine.busy`/`waiting`/`memory_bytes`/`kv_cache_pct` come from the engine's `/metrics` when `metrics` is true (llama.cpp with `--metrics` publishes busy and waiting and **no memory metric**; vLLM publishes all four — `process_resident_memory_bytes` is its API-server process, read live on 8010); `process.goroutines`/`heap_bytes`/`sys_bytes` from `runtime/metrics` (no stop-the-world); `process.rss_bytes` from `/proc/self/statm` on Linux and **0 on macOS** — 028's instrument already holds `--host-pid`, so `ps -o rss= -p PID` remains the RSS source on this laptop, while goroutines/heap/sys come from here. Each `/status` call costs the host one `GET /metrics` to the engine (bounded at 1 s) — fine at 1 Hz; and each admin call now uses its own socket (the leak in the Log), so polling for minutes leaves the host flat.

**Edges seen.** Prompt/completion text never leaves the process on the stream or the terminal even with `--log-prompts` (stripped in the hub; the file keeps it); a subscriber that stops reading is skipped, never waited for; `/events` on a process without a hub answers 503 with a sentence; the stream ends cleanly when the host stops and the watcher reconnects on its own; a host that stops answering mid-watch is shown as such and retried, never a silent freeze; a burst of events redraws once per event without refetching `/status`; the bridge between sessions shows `path lost — reconnecting`; a `--watch` interval floors at 100 ms; the request line shows the code alone for a stream that ended with an error event (status 200) and `client_closed` without its 499; the sampled peak and the sampled first-seen are named as sampled; a client identity is never forgotten (the list grows with identities, not traffic); Windows keeps the loopback+token transport — `Watch` and `/events` carry the token like `/status`, cross-compiled and vetted.

**Verification.** `go build ./...` OK · `go vet ./...` OK · `go test ./...` on the rebased lane (`7822fa7`): **158 passed / 0 failed / 2 skipped** top-level (the 2 skips are the pre-existing opt-in live probes `TestLiveLlamaCPP`, `TestSavedAddrLive`), 105 subtests passed / 0 failed. New: `TestEventStreamTailsTheRecorder`, `TestEventStreamNeverBlocksTheRequestPath`, `TestFetchLeavesNothingBehind` (admin); `TestRequestLine`, `TestParseMetrics`, `TestStatusBlockShowsSessionsEngineAndProcess`, `TestStatusWatchStreamsRequests`, `TestServeLogRequestsPrintsTheLine` (cmd); Peers assertions inside `TestSessionOpenPathRedial` (tunnel, in-process relay). Cross-compiles: `GOOS=darwin|linux|windows GOARCH=amd64 go build ./...` OK ×3; `GOOS=windows go vet ./internal/admin ./cmd/bunny-network` OK. `gofmt -l cmd internal` empty. Manual: the three drills above, each to files under the job tmp.

**Accounting** (raw added / deleted from `git diff origin/main`, at the freeze commit):

| Bucket | Budget | Measured | Verdict |
|---|---|---|---|
| Go source: `internal/admin` (+270/−9), `internal/tunnel` (+88/−13), `cmd/bunny-network` (+480/−33) | ≤400 | **891 raw; 867 excluding 24 help lines** | **over 2.2× — see Contest** |
| Go tests (`status_test.go` 243, `events_test.go` 125, `admin_test.go` +35/−10, others +15/−3) | — | +418 / −13 | 8 new tests |
| Help text (`status`, `serve`, `connect`) | surfaces | 24 | |
| Ticket record | — | Log + Contest + Report | |
| Files outside the scope contract | 0 | 0 (`internal/gateway` untouched; `hack/**` untouched) | met |
| Dependencies | none | 0 added (`runtime/metrics`, stdlib) | met |

**Concepts: 1 budgeted, 1 used** (the event stream). Flags named by the promises: `status --watch`, `--interval`, `serve --log-requests`, `connect --log-requests`; `connect --data-dir` is the global flag reused. No new state file, config key, or error code.

**Declared.** Live actions: own host on 6860 under `…/tmp/bn029-data` (four keys minted; invites in `…/tmp/bn029-{alice,bob,carol,dave}.json`, mode 0600) started three times and stopped; `connect` on 11436 with `--data-dir …/tmp/bn029-bridge`, stopped; ~12 chat requests and about two minutes of 1 Hz `GET /metrics` against the shared llama-server on 18080 (never restarted; 028's concurrent run at that time was on vLLM 8010, which I read once with `GET /metrics` for the field names and sent nothing else); the founder's host on 9091 queried once, read-only, with the new `status` — **its old binary has no `/events` and none of the new fields, so the new `status` prints `0 goroutines`, `no /metrics from this engine`, `0 active of 0 seen` against it until it runs this build** (compatibility is off pre-release; saying it so nobody reads that as the engine's truth). Intended, not bugs: `ttft` on a queued request includes the queue wait (the friend's time to first byte; `queued` sits beside it); a completed stream counts in `tokens_per_s_1m` at the minute it finished; `status --watch` clears the whole screen and keeps 20 request lines (a terminal shorter than the block scrolls; a legacy Windows console without VT processing would print the escape bytes — Windows Terminal is fine); the block's `N clients` (addresses with an open connection) and a session's `conns` overlap by design; the request line shows the model as the friend sent it (`gemma`), not the engine's file name.

**Adjacent, not fixed.** (1) tailcat `Server.Status()` returns no peers because its `StatusBuilder` never sets `WantPeers` (tailcat.go:1424); one upstream line would give the host per-key path, WireGuard handshake and WireGuard bytes — worth a tailcat issue, and then a size-1 follow-up here fills `path`/`via` on the host. (2) `admin.dial` still builds a client per call; a package-level client would be tidier now that keep-alives are off. (3) The README does not yet mention `status --watch` / `--log-requests` (README is the PM's per the 009 ruling; one "Watching the host" paragraph). (4) `hack/measure.sh` could read `engine.tokens_per_s_1m` instead of parsing streams, once 028 settles the instrument.

## Freeze

- **Base commit:** `7822fa7` (= `origin/main` at freeze; 030 and the handoff landed after dispatch, rebased onto them, no conflicts)
- **Lane:** worktree branch `t029-observability`, three commits over base
- **Code patch SHA-256** (`git diff origin/main -- ':!pm/' | shasum -a 256`): `0a2fa95202bb511264d70307b56eca86a3eb611259229630211fdad1aa7e9661`
- **Accounting:** the table above — source 891 raw / 867 excl. help against ≤400 (contested); tests +418/−13; concepts 1/1; scope 0 files outside; dependencies 0.
- **Checks at freeze:** as printed under *Verification*.
