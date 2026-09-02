# Architecture contract (v0, 2026-09-02)

This file is the seam contract every engineer codes against. It is binding where it says MUST.
Change it by contesting to the PM, never silently. Product name is a working name; it lives in
`internal/product/product.go` (Go) and `web/src/product.ts` (TS) and nowhere else.

## The shape

```
friend's browser                                     host machine
┌──────────────────────┐   WireGuard over DERP     ┌─────────────────────────────────────┐
│ web app (Vite/React) │ ────(WebSocket)────▶ relay ──▶ tunnel (tailcat.Server, userspace) │
│  tunnelFetch()       │                            │   └─ net.Listener on tunnel port 80  │
│  wasm bridge         │                            │        └─ gateway (http.Handler)     │
└──────────────────────┘                            │             ├─ auth: key store       │
                                                    │             ├─ limits / queue        │
                                                    │             ├─ usage recorder        │
                                                    │             └─ reverse proxy ──▶ upstream (llama.cpp / vLLM / Ollama / LM Studio)
                                                    │   admin API on unix socket (status)  │
                                                    └─────────────────────────────────────┘
```

Browser traffic is relay-only until tailcat ships WebRTC (issue #4). Native clients get direct paths.

## Go layout and ownership (scope contracts)

| Path | Owner ticket | Purpose |
|---|---|---|
| `cmd/bunny-network/` | 003 | CLI: `serve`, `keys …`, `status`, `usage`, `invite` |
| `internal/product/` | PM | name constants |
| `internal/invite/` | 001 | invite encode/decode (`bn1.<tc>.<secret>`) |
| `internal/tunnel/` | 001 | tailcat server wrapper → `net.Listener` |
| `web/wasm/` | 001 | wasm bridge (Go, GOOS=js) → `web/public/bunny.wasm` |
| `internal/gateway/` | 002 | http.Handler: auth, limits, queue, clamps, proxy, errors, CORS(dev) |
| `internal/keys/` | PM (types) / 003 (file store) | key types, Store interface, FileStore with hot reload |
| `internal/usage/` | PM (types) / 003 (recorder) | usage events, Recorder interface, JSONL recorder + aggregates |
| `internal/upstream/` | PM (types) / 003 (impl) | upstream detection, health, tokenize, slots |
| `internal/admin/` | 003 | unix-socket status API + client |
| `web/` (except `web/wasm/`) | 004 | the web client |
| `docs/`, `pm/` | PM | contract, measurement, PM records |

Module: `github.com/2185Lab/bunny-network`, Go 1.27 (auto toolchain), tailcat pinned `v0.4.0`.

## Data directory

`--data-dir` default: `os.UserConfigDir()/bunny-network` (mac: `~/Library/Application Support/bunny-network`).

| File | Owner | Format |
|---|---|---|
| `host.key.json` | 001 | tailcat `PrivateKey` JSON (server key; stable token across restarts). `--ephemeral` skips it. |
| `keys.json` | 003 | see Key store below; gateway hot-reloads on mtime change (checked ≤1/s) |
| `usage.jsonl` | 003 | one `usage.Event` per line, append-only |
| `admin.sock` | 003 | unix socket, HTTP, read-only status |
| `config.json` | 003 | persisted `serve` settings (upstream URL + key at 0600, slots, caps, dev-listen) so `serve` with no flags reuses them. `--log-prompts` and `--ephemeral` are per-run and never persisted (Protection 3). |

## Invite format (001 defines in Go, 004 mirrors in TS; MUST match)

```
bn1.<tailcat ConnBlob>.<secret>
```
- `bn1` = format version. Unknown prefix → "this invite needs a newer app".
- `<tailcat ConnBlob>` = the `tc…` string exactly as `Server.ConnBlob()` returns it (base64url, no dots).
- `<secret>` = 32 random bytes, base64url unpadded (43 chars). Never contains `.`.
- Whitespace trimmed; the whole string is case-sensitive.
- Decode returns `{Addr string, Secret string}`; encode is the inverse. Round-trip test required.

## Tunnel (001)

```go
package tunnel
type Options struct { DataDir string; Ephemeral bool; DERPMapURL string; Region string; Logf func(string, ...any) }
type Server struct { /* … */ }
func Start(ctx context.Context, o Options) (*Server, error)   // loads/creates host key, starts tailcat.Server with ServedTCPPorts = {80}
func (s *Server) Listener() net.Listener                     // conns arriving on tunnel port 80; Accept blocks; Close stops accepting
func (s *Server) Addr() string                               // the tc… ConnBlob (short form; full form when DERPMapURL/Region set)
func (s *Server) Status() Status                             // {Addr, RegionName, Started time.Time, Clients int}
func (s *Server) Close() error
const Port = 80
```
MUST: `OnTCP` returns nil for any port ≠ 80 (Protection 1). No `OnTCPForward`, no `AllowProxy`, no SSH/files services.

## wasm bridge (001) — JS API, MUST match exactly (004 codes against this)

Built from `web/wasm/main_js.go` to `web/public/bunny.wasm` (+ `web/public/wasm_exec.js` copied from
`$(go env GOROOT)/lib/wasm/wasm_exec.js`). Build tags: same list tailcat's `internal/buildtags.WasmTags()`
produces for v0.4.0, pinned in `web/wasm/build-tags.txt` with a test that the file compiles. Global:

```ts
declare global { interface Window { BunnyTunnel: BunnyTunnel } }
interface BunnyTunnel {
  connect(opts: {
    addr: string;            // tc… ConnBlob (from the invite)
    derpMapURL?: string;     // default https://tailcat.dev/derpmap.json
    privateKey?: string;     // tailcat PrivateKey JSON to reuse identity; ephemeral if absent
    verbose?: boolean;
    onLog?: (line: string) => void;
  }): Promise<Session>;      // resolves after the first successful ping (handshake up); rejects with a message on timeout (60 s)
}
interface Session {
  addr: string;
  privateKeyJSON: string;                       // persist to keep the same client identity
  dial(port?: number): Promise<Conn>;           // default 80; MUST NOT redo the handshake; cheap
  ping(): Promise<{ rttMs: number; via: string; direct: boolean }>;  // via e.g. "DERP(sfo)" or "203.0.113.7:41641"
  close(): void;
}
interface Conn {
  read(): Promise<Uint8Array | null>;           // null on EOF; no concurrent reads
  write(data: Uint8Array): Promise<void>;
  closeWrite(): Promise<void>;
  close(): void;
}
```

## Gateway HTTP API (002 serves; 004 consumes). Plain HTTP/1.1 inside the tunnel.

Auth: `Authorization: Bearer <secret>` on every route except `/healthz`.

| Route | Behaviour |
|---|---|
| `GET /healthz` | `{"ok":true}` always; no auth; no other info |
| `GET /me` | `{key:{id,name,status}, limits:Limits, usage:{rpm_used, tpm_used, today_tokens, in_flight}, host:{name, upstream:{kind,healthy,model_context}, models:[ids], relay:{region}}}` |
| `GET /v1/models` | upstream list filtered by key's allowed models |
| `POST /v1/chat/completions` | stream and non-stream. Gateway MUST: flush every SSE chunk immediately; inject `stream_options.include_usage=true` when streaming; clamp `max_tokens`; enforce context; pass `reasoning_content` through untouched |
| `POST /v1/embeddings` | pass-through with auth + limits |
| anything else | 404 in error format |

Error format (OpenAI-shaped, MUST):
```json
{"error":{"message":"human sentence","type":"invalid_request_error|authentication_error|permission_error|rate_limit_error|upstream_error","code":"invalid_key|key_paused|key_revoked|model_not_allowed|body_too_large|context_too_long|rate_limited|concurrency_limited|budget_exhausted|queue_timeout|upstream_down|upstream_error"}}
```
Statuses: 401 invalid_key · 403 key_paused/key_revoked/model_not_allowed · 413 body_too_large · 422 context_too_long · 429 rate_limited/concurrency_limited/budget_exhausted (+ `Retry-After` seconds) · 503 queue_timeout/upstream_down (+ `Retry-After`) · 502 upstream_error.

Dev mode: `serve --dev-listen 127.0.0.1:9090` additionally serves the gateway on loopback with permissive CORS
(`Access-Control-Allow-Origin: *`, headers `authorization, content-type`) so the web app can be developed
with real `fetch` before the wasm path exists. Loopback only; refuses non-loopback addresses.

## Key store (types by PM in `internal/keys/keys.go`; FileStore by 003)

`keys.json`:
```json
{"version":1,"keys":[{"id":"k_7f3a2b","name":"alice","secret_hash":"sha256:…","status":"active","created_at":"…","limits":{…}}]}
```
Defaults for a new key: rpm 20 · tpm 20000 · max_concurrent 1 · max_output_tokens 2048 · max_context 0 (= upstream's) ·
daily_tokens 200000 · models [] (= all). Secret shown once at `keys add`; store keeps `sha256:` hex only.

## Usage events (types by PM in `internal/usage/usage.go`; recorder by 003)

One JSON object per request, no prompt content unless `--log-prompts`:
`{ts, key_id, endpoint, model, status, code, stream, prompt_tokens, completion_tokens, queued_ms, ttft_ms, total_ms}`.

## Upstream (interface by PM in `internal/upstream/upstream.go`; impl by 003)

Detection order when `--upstream` absent: llama.cpp `127.0.0.1:8080` (`/props`) · Ollama `11434` (`/api/tags`) ·
LM Studio `1234` (`/v1/models`) · vLLM `8000` (`/v1/models`). Kind-specific: llama.cpp `/props` gives
`total_slots` and `default_generation_settings.n_ctx`; `/tokenize` exact counts. vLLM `/tokenize` exact counts;
`/v1/models[].max_model_len`; slots from `--slots` (default 2). Others: estimate tokens = ceil(chars/4), slots default 1.

## Concurrency & queue (002)

Global semaphore = upstream slots. Per-key semaphore = `max_concurrent`. Bounded wait `--queue-timeout` (default 30 s)
then 503 `queue_timeout` with `Retry-After`. Request timeout `--request-timeout` (default 300 s). Body cap
`--max-body` (default 4 MiB).

## Admin API (003), unix socket `admin.sock`, HTTP

`GET /status` → `{product, version, uptime_s, tunnel:{addr, region, clients}, upstream:{kind, url, healthy, model_context, slots}, queue:{in_flight, waiting}, keys:[{id,name,status,in_flight,rpm_used,today_tokens,last_seen}]}`.

## Measurement (`docs/MEASURE.md`, PM)

Every published latency/throughput number comes from a command written there and is reproducible.
