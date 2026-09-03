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
