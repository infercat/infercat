# MEASURE — every published number comes from a command here

## Test upstream (laptop)

```
~/Desktop/repos/2185Lab/bunny-kit/binaries/llama-server/b9553/llama-server \
  -m ~/.cache/bunny-network/models/gemma-4-E2B-it-Q4_K_M.gguf \
  --host 127.0.0.1 --port 18080 -np 2 -c 65536 --no-mmproj --metrics   # 32K per request
```

Note: llama.cpp divides `-c` across `-np` slots; with `-c 8192 -np 2` the engine reports `n_ctx` 4096 **per slot**, and that is the effective context the gateway must enforce. **Since 2026-09-03 02:05 the laptop engine runs `-c 65536 -np 2` (32K per request; the model's native context is 128K).** The host picked the change up on its own refresh (no restart). All numbers above this line were taken at 4K; 028 re-measures with realistic prompt sizes.

## Numbers (fill from ticket reports; date + command + result)

| Date | What | Command | Result |
|---|---|---|---|
| 2026-09-02 | Browser → public relay (nyc) → host on same laptop, headless Chromium | `node web/wasm/demo-check.mjs` against `go run ./hack/tunneldemo` | connect 183 ms; ping 77.6 / 31.2 / 32.9 ms via DERP(nyc), direct=false; `GET /healthz` 64 ms first dial, 33 ms repeat dial (no re-handshake); `/stream` 20 SSE events at 100 ms cadence intact |
| 2026-09-02 | CLI cross-check | `tailcat ping <addr>` | 27.9–29 ms via DERP(nyc) |
| 2026-09-02 | wasm download | `ls -l web/public/bunny.wasm.gz` | 6,182,826 bytes gz (26,912,436 raw) |

## Integration measurements (ticket 005, 2026-09-02)

Host: this laptop, home Wi-Fi, relay `nyc` (DERP region 301). Engine: llama.cpp b9553, Gemma 4 E2B
(`gemma-4-E2B-it-Q4_K_M.gguf`), `-np 2`, per-slot ctx 4096, on `127.0.0.1:18080`. Prompt:
`Why is the sky blue? Answer in three sentences.`

**Path label.** `tailcat ping --until-direct --timeout=15s <addr>` on this host:
`pong 27.15 ms via DERP(nyc)` then `pong 410µs via [2600:…]:52114` — a **direct** path exists, so a
host-side `tailcat socks` connection is NOT relayed once NAT traversal completes. The **browser**
path has no UDP and is always relayed (`direct=false`, docs/ARCHITECTURE.md §wasm bridge); its status
pill reads `relayed via nyc · 64–71 ms`.

### Host CLI, `hack/measure.sh` (N=3, curl; Tunnel via `tailcat socks`)

```
DIRECT=127.0.0.1:9090 ADDR=tc… SECRET=… N=3 sh hack/measure.sh
```
| Path | median TTFT | median tok/s | note |
|---|---|---|---|
| Direct (loopback `--dev-listen 127.0.0.1:9090`) | 32 ms | 167.9 | runs 76/31/32 ms · 167.9/169.1/167.0 |
| Tunnel (`tailcat socks` → relay-or-direct) | 26 ms | 161.4 | runs 33/26/20 ms · 164.9/157.3/161.4; a direct path was available (see label) |

TTFT is the same order of magnitude either way; token rate through the tunnel is within ~4 % of
loopback. The relay adds no perceptible latency to the stream here.

### Browser (headless chromium, `web/dev/int-check.mjs`, console-timestamped TTFT = first rendered delta)

| Path | connect | status pill | TTFT | tok/s | usage |
|---|---|---|---|---|---|
| Direct mode (`VITE_DIRECT_URL=http://127.0.0.1:9090`, real fetch) | — | `direct · 6 ms` | 102–113 ms | 166–169 | — |
| Tunnel mode (built `web/dist`, wasm bridge, public relay) | 1124 ms | `relayed via nyc · 64–71 ms` | 152–163 ms | 167–169 | `/me` bar `2/20 per minute · 2.2k/200k today` |

Browser TTFT runs ~40–60 ms higher than the host CLI (one relay round trip plus the browser's
render), still well under the "feels instant" bar. Connect (`BunnyTunnel.connect` → first handshake →
`GET /me`) is ~1.1 s on this relay. wasm bundle: `bunny.wasm` 26,928,804 B raw / 6,189,248 B gz
(`make wasm`). The wasm bridge holds no js.Func or heap across 300 tunnel dials
(`web/wasm/leak-check.mjs`: liveFuncs 5→5, heap Δ<2 MiB, GC'd).

### vLLM (ticket 005 promise 7), Direct mode over the read-only ssh forward

`serve --upstream http://127.0.0.1:8010` (via `ssh -N -L 8010:127.0.0.1:8010 max@max-ws.lab`),
`entropy-v2-gemma4-12b-w4a16-group128`, context 8192, slots 2. A streamed Direct-mode chat returned
475 SSE data lines, usage `prompt_tokens:32 completion_tokens:472`. Exact `/tokenize` counting is
used, not `ceil(chars/4)`: vLLM `/tokenize` with the chat template returns 32, matching the engine's
own `prompt_tokens:32` (the gateway's `CountTokens` posts to `/tokenize`, internal/upstream/kinds.go:187).

### Self-hosted relay test (2026-09-03 01:05–01:20 EDT) — derp.2185lab.com, DigitalOcean NYC, 2 vCPU / 4 GB

Test host only (`--data-dir …/bn-relaytest --region derp.2185lab.com`); the demo host stayed on Tailscale's
public relays per the founder's ruling. Relay: derper 1.102.3 from source, Let's Encrypt cert (Sep 3 →
Dec 2), DERP `/derp/probe` 200, STUN answers tailscale's own client (a hand-rolled probe did not — our
error, not the relay's). Address embeds `derp.2185lab.com` (`tailcat parse` shows the node hostname).

| Path | Command | Result |
|---|---|---|
| CLI, until direct | `tailcat ping --until-direct <addr>` | first pong 29.8 ms **via our relay**, then 0.8 ms **direct** (hole-punched; same LAN) |
| CLI tunnel vs loopback, N=3 | `hack/measure.sh` | direct 33 ms TTFT / 173.8 tok/s · tunnel 33 ms / 173.4 tok/s (identical: traffic went direct after rendezvous) |
| Browser (wasm, relay-only) | `web/dev/real-check.mjs reconnect` from the relay-pinned data dir | connect by link 5.9 s (first run; relay cold for a new key); reconnect 10.6 s; path pill **"relayed via derp.2185lab.com · 65 ms"**; relay `derp_accepts` 1 → 6 during the run |
| Browser via Tailscale NYC (reference, earlier today) | same harness | 64–79 ms, connect ~1 s warm |

Verdict for the founder's decision: **our relay performs the same as Tailscale's NYC relay from this
location** (65 ms vs 64–79 ms browser RTT; CLI paths go direct either way). The remaining comparison is
operational (uptime, regions, cost), not latency. No switch made.

### `connect` — the host binary as a client (ticket 026, 2026-09-03 03:41 EDT)

Test host on this laptop (`serve --dev-listen 127.0.0.1:6810 --data-dir …/bn026-data`, relay `nyc`),
`bunny-network connect <invite>` on the same laptop at `127.0.0.1:11435`, engine as above (`-c 65536 -np 2`).

| Path | Command | Result |
|---|---|---|
| connect startup | `bunny-network connect <invite>` | banner in ~1.5 s from the handshake: `host t026 host · gemma-4-E2B-it-Q4_K_M.gguf`, `path direct · 0.2 ms` (same machine: the first disco ping already found the loopback endpoint), `local http://127.0.0.1:11435` |
| CLI, until direct | `tailcat ping --until-direct --timeout=30s <addr>` | `pong in 540µs via 192.168.199.132:53267` — direct on the first pong |
| curl, streamed chat | `curl -sN http://127.0.0.1:11435/v1/chat/completions -d '{"stream":true,…}'` | TTFT 24 ms, `[DONE]` at 3641 ms, usage `prompt 22 / completion 566` in the final chunk |
| OpenAI Python SDK 2.48 | `OpenAI(base_url="http://127.0.0.1:11435/v1", api_key="any-key")`, `chat.completions.create(stream=True, stream_options={"include_usage":true})` | first content delta 927 ms (the model thinks first), 5 deltas, total 959 ms, `finish_reason=stop`, usage `27 / 147`; `models.list()` and a non-stream `create` also complete |
| `hack/measure.sh`, N=3, three modes in one run | `DIRECT=127.0.0.1:6810 CONNECT=127.0.0.1:11435 MODES="direct tunnel connect" ADDR=… SECRET=… sh hack/measure.sh` | direct 35 ms TTFT / 168.3 tok/s · tunnel (`tailcat socks`) 37 ms / 164.7 · **connect 36 ms / 164.5** (runs 37/36/36 ms · 165.7/163.2/164.5) |

The `connect` path costs the same as `tailcat socks` — one tunnel TCP dial per request over a session
that is already direct — and is within 2 % of loopback on token rate.

## Load (ticket 028, 2026-09-03 03:30–06:00 EDT) — every layer, layer by layer

**Instrument.** `hack/load` (Go): N simulated friends, each with its own key (`keys add --json`) and its own
native tailcat session, running the ticket's growing-conversation mix — turn 1 a ~300-token question,
append-only history of real replies, every fifth turn a 4–12K-token document paste, replies asked for at
300–2,000 tokens; friend i starts i%8 turns in, so every run covers prompt sizes from 300 to 24K tokens
at once. `--relay-only` disables the client's UDP (tailscale's `TS_DEBUG_ALWAYS_USE_DERP`), so every byte
crosses the relay exactly as a browser friend's does; without it, native friends hole-punch to a direct
path. Sampled once a second: host `status` (in_flight/waiting/clients and, since 029, `process.{goroutines,heap_bytes,sys_bytes}` and
`engine.{busy,waiting,kv_cache_pct}`) on the admin socket, host RSS via `ps` (029's `process.rss_bytes` is 0 on macOS), the engine's `/metrics`
(+ `/slots` on llama.cpp) and RSS, and the relay's `derp_*`/`process_*` varz plus `top` over one ssh
session. Per request: the friend's view (status, code, TTFT, tok/s, bytes up/down, path) in
`requests.jsonl` and the host's own usage event (prompt_tokens, queued_ms, ttft_ms, settle) in
`host.jsonl`; `summary.md` is the table quoted below. Raw run directories are kept outside the repo.

**Set-up (all on this laptop, Apple M5 Max, 18 cores, 64 GB; home Wi-Fi).** Hosts: one per engine per
relay, throwaway data dirs, `--dev-listen 127.0.0.1:682x`; A = llama.cpp on our relay
(`--region derp.2185lab.com`), B = llama.cpp on Tailscale's NYC relay (default), C/D = vLLM on ours/NYC
(`--upstream http://127.0.0.1:8010 --slots 2` over the read-only ssh forward to the workstation), E = a
private llama-server on port 63080 (same binary and model as the laptop engine) for the engine-restart
case. Engines: laptop `llama-server b9553 -np 2 -c 65536` (Gemma 4 E2B Q4_K_M; 32,768 tokens per slot),
workstation vLLM `entropy-v2-gemma4-12b-w4a16-group128` (max-num-seqs 2, max_model_len 8192). Relay:
`derp.2185lab.com`, DigitalOcean NYC, 2 vCPU / 4 GB, derper 1.102.3. The founder's demo host (Tailscale
relay, same llama-server) stayed up throughout and was not touched.

```
go build -o $T/bn028-bin/load ./hack/load
$T/bn028-bin/load --host-dir $T/bn028-a-llama-ours --bin $T/bn028-bin/bunny-network --host-pid <pid> \
  --engine http://127.0.0.1:18080 --engine-pid <pid> --relay-ssh root@206.189.207.168 \
  --relay-only --n 12 --minutes 3 --out $T/bn028-out --name R3-llama-ours-n12
# sessions only:  --mode sessions --n 100 --settle 150s      spike: --mode sessions --n 30 --stagger 0
# pathological:   --noread · --body-bytes 4300000 · --n 2 --sessions-per-friend 6 --max-concurrent 3
#                 --n 1 --sessions-per-friend 50 · --at "75s=ssh root@… systemctl restart derper"
```
