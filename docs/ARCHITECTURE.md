# Architecture contract (v1, 2026-09-02 evening — v0 was the build-night seam contract; v1 folds in tickets 005–011 and `docs/DESIGN.md`)

This file is the seam contract every engineer codes against. It is binding where it says MUST.
Change it by contesting to the PM, never silently. The product name lives in
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
                                                    │             └─ reverse proxy ──▶ upstream (llama.cpp / llama-swap / vLLM / Ollama / LM Studio)
                                                    │   admin API on unix socket (status)  │
                                                    └─────────────────────────────────────┘
```

Browser traffic is relay-only until tailcat ships WebRTC (issue #4). Native clients get direct paths.

Tailcat 0.6.0 clients accept both address forms. New identities persist `infercat_format: 2`
and enable the WireGuard pre-shared key (PSK); their invites use `ic2`. A missing marker is
legacy even if the file already contains a PSK: that identity remains unchanged and mints `ic1`.
Clients accept both versions; older clients reject `ic2` with the newer-app message before
connecting. The full new address contains a shared secret, so keep the whole invite private.
To migrate, stop `serve`, run `infercat identity upgrade`, then start it and rotate/add keys to
re-issue every invite. The command preserves the old file as `host.key.json.pre-ic2` without
overwriting an existing backup and atomically replaces the identity. `serve` and the upgrade
hold one OS lock for the data directory; process exit releases it. Old invites name the retired
endpoint, so an unreachable result cannot prove that an upgrade happened.

## Go layout and ownership (scope contracts)

| Path | Owner ticket | Purpose |
|---|---|---|
| `cmd/infercat/` | 003 | CLI: `serve`, `keys …`, `status`, `usage`, `invite` |
| `internal/product/` | PM | name constants |
| `internal/invite/` | 001 | invite encode/decode (`ic1/ic2.<tc>.<secret>`) |
| `internal/tunnel/` | 001 | tailcat server wrapper → `net.Listener` |
| `web/wasm/` | 001 | wasm bridge (Go, GOOS=js) → `web/public/infercat.wasm` |
| `internal/gateway/` | 002 | http.Handler: auth, limits, queue, clamps, proxy, errors, CORS(dev) |
| `internal/keys/` | PM (types) / 003 (file store) | key types, Store interface, FileStore with hot reload |
| `internal/usage/` | PM (types) / 003 (recorder) | usage events, Recorder interface, JSONL recorder + aggregates |
| `internal/upstream/` | PM (types) / 003 (impl) | upstream detection, health, tokenize, slots |
| `internal/admin/` | 003 | unix-socket status API + client |
| `web/` (except `web/wasm/`) | 004 | the web client |
| `docs/`, `pm/` | PM | contract, measurement, PM records |

Module: `github.com/infercat/infercat`, Go 1.27.1 (auto toolchain), tailcat pinned `v0.6.0`.

## Data directory

`--data-dir` default: `os.UserConfigDir()/infercat` (mac: `~/Library/Application Support/infercat`).

| File | Owner | Format |
|---|---|---|
| `host.key.json` | 001/009 | tailcat `PrivateKey` JSON plus `infercat_format`: the host identity, created once; an explicit stopped-host upgrade replaces it with a backed-up v2 identity. `Addr() == SavedAddr(dir)` always. `--ephemeral` never writes it. |
| `keys.json` | 003 | see Key store below; gateway re-reads on mtime change (≤1/s, plus on any lookup miss); the CLI pokes `POST /reload` on the admin socket after every write so changes are live at once |
| `usage.jsonl` | 003 | one `usage.Event` per line, append-only |
| `admin.sock` | 003/009 | unix socket, HTTP: `GET /status`, `POST /reload` (Windows: loopback port in `admin.port`); every platform authenticates with per-run `admin.token` |
| `config.json` | 003 | persisted `serve` settings: `upstream`, `upstream_key` (0600), `slots`, `dev_listen`, `derpmap_url`, `region`, `name`, `web_url`. `--log-prompts`, `--ephemeral`, `--verbose` are per-run and never persisted (Protection 3). Retired keys (`queue_timeout`, `request_timeout`, `max_body`) are ignored on load. |
| `tunnel.log` | 005 | the tailcat/wgengine log (truncated at start); `serve --verbose` prints it instead |

## Invite format (001 defines in Go, 004 mirrors in TS; MUST match)

```
ic1.<legacy tailcat address>.<secret>
ic2.<PSK tailcat address>.<secret>
```
- `ic1` / `ic2` = legacy / PSK identity format. A higher version → "this invite needs a newer app".
- `<tailcat address>` = the `tc…` string exactly as `tunnel.Server.Addr()` returns it (base64url, no dots).
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
func (s *Server) Addr() string                               // the tc… address (short form; full form when DERPMapURL/Region set)
func (s *Server) Status() Status                             // {Addr, RegionName, Started time.Time, Clients int}
func (s *Server) Close() error
const Port = 80
```
MUST: `OnTCP` returns nil for any port ≠ 80 (Protection 1). No `OnTCPForward`, no `AllowProxy`, no SSH/files services.

## wasm bridge (001) — JS API, MUST match exactly (004 codes against this)

Built from `web/wasm/main_js.go` to `web/public/infercat.wasm` (+ `web/public/wasm_exec.js` copied from
`$(go env GOROOT)/lib/wasm/wasm_exec.js`). Build tags: same list tailcat's `internal/buildtags.WasmTags()`
produces for v0.4.0, pinned in `web/wasm/build-tags.txt` with a test that the file compiles. Global:

```ts
declare global { interface Window { InfercatTunnel: InfercatTunnel } }
interface InfercatTunnel {
  connect(opts: {
    addr: string;            // tc… address (from the invite)
    derpMapURL?: string;     // default https://tailcat.dev/derpmap.json
    privateKey?: string;     // tailcat PrivateKey JSON to reuse identity; ephemeral if absent
    verbose?: boolean;
    onLog?: (line: string) => void;
  }): Promise<Session>;      // resolves after the first successful ping (handshake up); rejects with a message on timeout (60 s)
}
interface Session {
  addr: string;
  privateKeyJSON: string;                       // persist to keep the same client identity
  stats(): { liveFuncs: number };                // debug hook (005): js.Func handles alive; must stay flat across dials
  dial(port?: number): Promise<Conn>;           // default 80; MUST NOT redo the handshake; cheap
  ping(): Promise<{ rttMs: number; via: string; direct: boolean }>;  // via e.g. "DERP(nyc)"; in the browser this is a TCP connect through the relay (tailcat's disco ping is unusable under js/wasm) and direct is always false today
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
| `GET /me` | `{key:{id,name,status}, limits:Limits, usage:{rpm_used, tpm_used, today_tokens, in_flight}, host:{name, upstream:{kind,healthy,model_context}, models:[ids], vision:{id:true|false|null}, audio:{transcriptions:model-id|null,speech:model-id|null}, relay:{region}, log_prompts:bool}}`. `kind` is `"unknown"` until an engine answered a signature probe; `healthy` reflects the last probe; the client discloses `log_prompts`. `vision` contains only invite-visible model ids and is refreshed with every probe (null = unknown); the app reads the selected model’s entry. llama.cpp reports `/props` modalities.vision, Ollama `/api/show` capabilities, LM Studio `/api/v0/models` type (`vlm`); vLLM assumes true on a successful model probe. Failed engine refreshes preserve prior state. Image-bearing requests refused by a 4xx naming images/multimodal input return `images_not_supported` (400, no retry) with the engine’s message, except context overflow retains its existing mapping. |
| `GET /v1/models` | engine list filtered by the intersection of the host pin and the key's allowed models; per-key concurrency applies; **not counted against RPM** (ruled 2026-09-02, ticket 014: the friend's meter counts messages) |
| `POST /v1/responses` | Stateless translation to the chat pipeline, streamed or non-streamed; same auth, limits, text queue and settlement. See Responses below. |
| `POST /v1/chat/completions` | stream and non-stream. Gateway MUST: flush every SSE chunk immediately; inject `stream_options.include_usage=true` when streaming; normalize the body once (strip engine-override aliases such as `n_predict`/`n`/`best_of`/`priority`, fill `model`, clamp `max_tokens` to the key's cap and shrink it to fit the context and the TPM/daily windows, floor 16); pass `reasoning_content` through untouched. An engine 400/422 caused by the request maps to 400 `invalid_request` with the engine's message — except a context overflow (llama.cpp `exceed_context_size_error`, vLLM "maximum context length"), which maps to 422 `context_too_long` so clients never retry it (036); 5xx → 502 |
| `POST /v1/embeddings` | pass-through with auth + limits |
| anything else | 404 in error format |

Error format (OpenAI-shaped, MUST):
```json
{"error":{"message":"human sentence","type":"invalid_request_error|authentication_error|permission_error|rate_limit_error|upstream_error|server_error","code":"invalid_request|invalid_key|key_paused|key_revoked|model_not_allowed|not_found|body_too_large|context_too_long|rate_limited|concurrency_limited|budget_exhausted|queue_timeout|upstream_down|upstream_error|storage_failed"}}
```
Statuses: 400 invalid_request (malformed JSON/body) · 404 not_found · 401 invalid_key · 403 key_paused/key_revoked/model_not_allowed · 413 body_too_large · 422 context_too_long · 429 rate_limited/concurrency_limited/budget_exhausted (+ `Retry-After` seconds) · 503 queue_timeout/upstream_down (+ `Retry-After`) · 502 upstream_error · 500 storage_failed (server_error: the host could not store the image; no charge).

Dev mode: `serve --dev-listen 127.0.0.1:9090` additionally serves the gateway on loopback with permissive CORS
(`Access-Control-Allow-Origin: *`, headers `authorization, content-type`) so the web app can be developed
with real `fetch` before the wasm path exists. Loopback only; refuses non-loopback addresses.

## Key store (types by PM in `internal/keys/keys.go`; FileStore by 003)

`keys.json`:
```json
{"version":1,"keys":[{"id":"k_7f3a2b","name":"alice","secret_hash":"sha256:…","status":"active","created_at":"…","limits":{…}}]}
```
Defaults for a new key: rpm 20 · tpm 20000 · max_concurrent 1 · max_output_tokens 4096 · max_context 0 (= upstream's) ·
daily_tokens 200000 · models [] (= all). Secret shown once at `keys add`; store keeps `sha256:` hex only.

`serve --models a,b` remembers a host-wide pin in config.json; `--models all` removes it. The pin
intersects every key's allowlist for model listings, `/me` model metadata and request admission
(`model_not_allowed` on refusal). Omitted models still select the first permitted engine model,
then the first model in the effective allowlist. Unreported pinned ids are allowed (swap engines
may load them later); the banner identifies them, and admin status exposes `models_pinned`.

## Usage events (types by PM in `internal/usage/usage.go`; recorder by 003)

One JSON object per authenticated request (401s are not recorded; `endpoint` capped at 64 bytes), no prompt
content unless `--log-prompts`:
`{ts, key_id, endpoint, model, status, code, stream, prompt_tokens, completion_tokens, queued_ms, ttft_ms, total_ms}`.
`code` may be the usage-only status `client_closed` (never on the wire). `usage` and `status` count model
calls as requests and show app polls (`/me`, `/v1/models`) separately.

Public-bridge gateway events add `via: "bridge"` from local request context. Missing historical
`via` means `direct` (including the tunnel/dev listener). Read aggregates add `by_via` maps of
the existing Stats at report, day, key, and daily-key levels; original totals are unchanged.
Each bucket computes its own percentiles from successful model-call samples. `usage` prints
per-via totals and a VIA column per key. Gateway refusal codes remain in `errors_by_code`.
Edge-only refusals such as `host_busy`/`host_offline` have no host event and are excluded;
durable edge refusal accounting is a later slice.

Usage events and every admin aggregate bucket carry `meters: [{class, unit, measured, charged}]`:
`tokens` / tokens, `audio` / seconds, and `speech` / characters. Measured values derive from
unchanged telemetry (including the prompt/completion split and audio provenance); charged values
come from settlement. A rejected text request records zero, a non-stream cut records its reservation,
and a stream cut or served request records its settled usage. Restart restores each class's day
from charged totals; minute windows and outstanding reservations start empty. `ts` remains request
start for listings and telemetry; `settled_at` records the exact charge time. Meter filters and
per-class daily buckets use `settled_at`, falling back to `ts` for older rows. Legacy rows without
meters retain the old measured-equals-charged interpretation; an explicit zero charge never falls
back to telemetry. The CLI prints charged totals beside observed token telemetry. Existing key
files remain unchanged: the limiter derives `map[class][]Budget{Unit, Window, Amount}` in memory,
with both minute and day token windows; RPM, concurrency, output/context ceilings and model policy
stay beside those budgets. Reading a key file never rewrites it.

## Engine (`internal/upstream`; state per `docs/DESIGN.md` §3, landed by 011)

The engine is a state, not a value: `Kind` is `Unknown` until a signature probe answers (`Generic` only when
`/v1/models` answered without a signature); `Health{OK, Since, Err}`; `ModelContext`, `Slots` (override
applied; 1 while Unknown), `Models` are last-known and survive a failed refresh. One `Refresh` is the only
probe; `serve` polls it every 10 s and logs health transitions. Consumers **read** `Info()` when they
decide (health at request entry, models/context at normalization, slots at slot acquire/release); nothing
is pushed. The gateway depends only on
```go
type Engine interface { Info() Info; CountTokens(ctx, model, text, messages) (n int, exact bool, err error); Do(ctx, method, path string, body []byte, stream bool) (*http.Response, error) }
// CountTokens counts under the engine's chat template when messages are given (llama.cpp /apply-template → /tokenize; vLLM /tokenize {messages}); estimates add 4/message + 16. (036)
```
`Do` owns the bearer, refuses redirects, applies the first-byte deadline (120 s) and the probe bound (3 s)
on GETs; the engine's URL never crosses the seam (compile-time: the gateway names only `Engine`).
Detection order when `--upstream` is absent: llama.cpp / llama-swap `127.0.0.1:8080` · Ollama `11434` · LM Studio
`1234` · vLLM `8000`; probes carry `--upstream-key`; the first candidate that reaches OK wins. Exact token
counts via `/tokenize` for llama.cpp and vLLM; others estimate ceil(chars/4).
llama-swap is identified by `/v1/models` `owned_by: "llama-swap"` before any `/props` probe
(v255, 7761aa1). Per-model `context_length`, `status.value` and last-known slots live in
`Info.ModelDetails`, alongside `Vision`; loaded models alone get `/props?model=<id>` probes.
The global context is the minimum positive reported context; slots are the minimum per-model
estimate (loaded probe, else that model's last known, else 1), with `--slots` overriding.
Unloaded models are never started by discovery; their ordinary first request loads them.
`--upstream auto` forgets a remembered URL and detects again.

## Destinations

The gateway's router selects one destination by the existing route before health checks and early
key admission; it validates the parsed model at the existing normalization point. A destination
owns an ID (`text`, `transcribe`, or `speech`), kind `engine`, origin `local`, a shared `Info()`
source, the existing typed text `Engine` or `AudioEngine` transport, live offers (models, vision,
audio kinds), and one instance of the bounded FIFO `slotQueue`. Capacity is its own engine's
`Info().Slots`, at least one; `--slots` continues to affect text only, with no new flags. Transports
keep addresses, bearers and deadlines; audio gains no artificial tokenizer or refresh method.
Text routes are chat, Responses (translated to chat), embeddings and models; absent audio routes remain `not_found`, unloaded
allowed text models remain valid, and audio still falls back to its configured/default model.
The request releases the resolved destination's slot through its existing single `finish` exit;
the settle table is unchanged. `Queue()` and the existing `/status.queue` now describe text only;
`/status.destinations` adds id, kind, models, slots, in_flight and waiting for each destination
on the authenticated admin surface only. `/me` stays unchanged: friends receive their existing
allowlist-filtered models, vision and audio offers, not engine structure. New usage rows carry
`destination`; an empty value in older rows means text. Token counting receives the selected model
(vLLM and llama.cpp/llama-swap tokenization), and context admission uses its positive per-model
context when reported, otherwise the engine context. Independent audio capacity and model-aware
admission are the intentional corrections; routes, existing fields and error bodies stay intact.

## Request pipeline, limits, and deadlines (006 + 010; `docs/DESIGN.md` §1)

One pipeline, one exit. A request record owns every resource; the stage order is fixed in one function:
`checkHealth → admitKey (RPM + per-key concurrency) → readBody → normalize → count → checkBudgets (reserve
`prompt + max_tokens`, shrunk to fit TPM/daily) → acquireSlot → callUpstream → relay → finish`. `finish`
is the single deferred exit; it settles by the **outcome** table: rejected before the queue → not counted,
reservation released · queue timeout → counted, 0 charged · client gone while waiting, or before any request byte reached the engine (`httptrace.WroteRequest` never fired) → not counted, 0 charged ·
engine error → counted, 0 · served → charged as the engine's usage (or pre-check prompt + deltas seen when
no usage object) · cut (client stopped reading / gone / engine stalled) → charged the reservation for a
non-stream cut, deltas seen for a stream. RPM counts model calls (`/v1/chat/completions`, `/v1/responses`,
`/v1/embeddings`) only.

Each destination queue: FIFO; cap read live from its own `Info().Slots`; waiting set capped at max(2, 2×cap) with an immediate
503 `queue_timeout` on overflow; `Queue()` exact under one mutex. No `SetSlots`. **A streaming request that
must wait writes its response head at once and an SSE comment `: queued` on joining and every 5 s** (under
the client write deadline, so a dead reader drops its place as `client_closed`); a queue timeout after the
head is an SSE error event `{"error":{"code":"queue_timeout","retry_after":N}}` followed by `[DONE]`.
Every stream error event is followed by `[DONE]`. Non-streaming requests keep the 503. (018)

Deadlines, one owner each (no absolute request timeout): header read 30 s · body read 30 s · queue wait
30 s · engine first byte 120 s · engine idle 60 s (reset per line) · client write 60 s (re-armed per event)
· idle keep-alive 2 min · auxiliary engine call 3 s. A slow-but-live stream is never cut. Body cap 4 MiB.
These are constants; the `--queue-timeout`, `--request-timeout`, `--max-body` flags are retired.

## Admin API (003), unix socket `admin.sock`, HTTP

`GET /status` → `{product, version, uptime_s, tunnel:{addr, region, clients}, upstream:{kind, url, healthy, since, model_context, slots}, queue:{in_flight, waiting}, keys:[{id,name,status,in_flight,rpm_used,today_tokens,last_seen,connected,sessions}]}` — `queue` numbers are exact; `clients` = open port-80 connections. `POST /reload` re-reads `keys.json` now.

079 adds optional `bridge: {enabled,url,connected,since,last_error,requests_today}`, also shown
as one line by `status` and refreshed by `status --watch`. It is absent without a registration.
Connected means the live socket has acknowledged its first friend-key snapshot; `since` marks
the current enabled/connection state, and `last_error` exposes a sanitized connection failure,
cleared on reconnection. `requests_today` reads completed gateway events with `via: "bridge"`
from usage.jsonl, by request start date in UTC (model calls, polls, and gateway refusals).
The count survives restart and off/on; a usage read failure is reported in `last_error`.
The optional status contains no bridge token, key hash, or peer-supplied error text.

`expose --off` now atomically persists `disabled: true` in the existing mode-0600 bridge.json;
`expose --on` preserves the token and enables it again. An absent disabled field means enabled,
so older registrations still work. Both commands invoke the existing `/reload`; a failed reload
is reported after the saved change, never as confirmation of the running state. On startup/reload,
disabled configuration stops the client and remains visible with its URL; deleting bridge.json
forgets the registration and requires a fresh one-time code. No extra state file or admin route.

a public URL decrypts TLS at the edge and in our object; the tunnel mode's "nobody in the middle" does not carry over.

Per-key `connected` means a matching credential was seen through a tunnel session in the last
60 seconds; `sessions` counts distinct node-derived peer addresses in that window, not open TCP
connections. The address is stable across a client's TCP connections; a second matching key
reassigns that session. Paused/revoked credentials count as sightings; unknown credentials and
the dev listener do not. The gateway keeps this association in memory only, prunes expired
sightings on writes/status reads, and clears it on restart. Session identities never enter
usage events, usage snapshots, or disk. Remote-admin `in_use` keeps its separate rule below.

The host console (`069`, Protection 1) listens only on a literal loopback IP, by default
`127.0.0.1:9101`; remembered `serve --console off` disables it. `/api/*` shares the admin
handler, while `/` serves the embedded console bundle. Every admin route on either listener
requires the per-run bearer in `admin.token` (0600, removed at shutdown on every platform);
Unix CLI clients read it too. The startup banner prints only the console address and the command
to open it; only `infercat console` (including `--print`) emits a URL with the token in its fragment.
The page consumes it into memory and clears the fragment, never using browser storage. Responses forbid caching; there is no CORS grant, and the listener
rejects alternate Host headers. The console is host-local by default; the opt-in exception is described below. The JSON API adds
key list/detail, mint, pause/resume/revoke/rotate, partial limits updates, aggregate usage
(`today` or trailing seven UTC days, with explicit daily counts), engine info including probe
error, and settings. Key reads exclude hashes; usage exposes counts only. `log_requests` in
settings is the runtime value and `log_requests_remembered` is false. Writes use the CLI's
store operations and shared mint/rotation helpers, then reload the running store; a notification
failure after a committed write is logged without reporting the write as a refusal.

085 adds one opt-in exception to Protection 1: while remote access is enabled, the existing
port-80 gateway accepts `/console/` (authenticated status) and an explicit `/console/api/*`
whitelist. A separate `ia1.<address>.<secret>` bearer controls keys, settings and stored data, never `/v1`.
Its SHA-256 hash and enabled-since timestamp live in mode-0600 `admin.json`, separate from
friend keys. The switch persists across restarts; enabling requires the loopback console on.
Off returns 404; a wrong bearer while on returns 401. Rotation invalidates the old bearer.
A separate per-peer rolling failure budget allows 30 missing/wrong credentials per minute:
in-flight checks reserve a slot before hashing, successful checks refund it, and exhaustion
returns 429 before hashing even for a valid code. The trusted tunnel peer identity (or normalized
remote IP fallback, never headers) owns the budget. Idle entries expire; the 4096-entry cap
refuses new peers instead of evicting existing budgets. A credential-free exhaustion line is
logged at most once per minute globally; usage retains refusal counts. The existing authenticated
read/write request budgets are unchanged.
The gateway forwards only validated methods/paths and the usage window to its literal loopback
listener, adding the local token itself and replacing caller headers. Remote settings cannot
change the console address; no console settings route accepts an engine URL or prompt logging.
The bearer is “in use” when accepted within ten minutes (bearer-only, not a device count).
Remote requests produce counts-only `kind: "console"` usage events. Browser entry is ticket 086;
released web apps reject the unfamiliar `ia1` prefix with their existing unsupported-prefix copy.

Settings PATCH validates the whole whitelist before an atomic config write. Name applies live
to status and `/me`; the web URL applies to the next generated link; slots and console address
wait for the next start. Log-requests changes live for this run only and is never persisted.
Non-HTTPS web URLs are refused except literal loopback/RFC-1918 IPv4 or localhost HTTP URLs;
credentials, query and fragment are refused. No reachability check or implied trust verification.

## Measurement (`docs/MEASURE.md`, PM)

Every published latency/throughput number comes from a command written there and is reproducible.

### JavaScript client workspace

`packages/client` provides the private, browser-only `@infercat/client` v0: an `ic1` invite creates a session with tunnel-bound fetch, `/me`, path status, and OpenAI SDK constructor options. The app's `web/src/transport` re-exports the shared implementation in `packages/client/src/transport`; the library never imports app UI or state. It uses the existing version-coupled `InfercatTunnel` wasm interface and a matching `wasm_exec.js`, defaults to the hosted artifact, and accepts an explicit artifact URL. Fetch preserves HTTP error responses, while `me()` throws typed gateway errors; close aborts active requests. See the package README for browser requirements, the Go runtime limitation that blocks Node, artifact pinning, and isolated-host verification.

The read-only console (`074`) refreshes authenticated snapshots every two seconds and joins live
key counters (including TPM) to key limits and usage. It keeps the last facts muted while retrying
an unavailable host. EN/ZH share one copy table; rows open a keyboard-accessible drawer, and all
write controls remain disabled. Usage adds exact per-model call counts and UTC daily buckets for
the host and each key; percentiles are computed from samples, never added. Settings expose only
whitelisted display fields, including the runtime prompt-logging truth and configured/effective
web URL. Missing model context and historical uptime are not inferred. `make console-build`
updates the embedded bundle; `make check` builds separately and refuses stale committed
`console/dist` bytes without rewriting them.

### Explicit audio engines

`serve --upstream-transcribe URL --upstream-speech URL` adds the configured
`POST /v1/audio/transcriptions` and `POST /v1/audio/speech` routes. Each base URL
has a corresponding `-key` flag; both may point at the same OpenAI-compatible
server. Flags and `--max-transcription-seconds` are remembered in the existing
0600 config file. URL/key changes require the next `serve`; admin reload reloads
keys and re-probes both audio engines. A successful `/v1/models` probe (or `/health`
fallback) plus explicit configuration makes a route available when a model is known.
`--upstream-transcribe-model` / `--upstream-speech-model` optionally select the
host's default id; otherwise the first `/v1/models` id is used. A missing request
model, or one not in that engine's list, becomes this default before allowlist
checks. `/me.host.audio` contains `transcriptions` and `speech` model ids, or null
when unavailable or not shared with this invite. A health-only engine needs an
explicit model flag. Model flags are remembered and take effect on next serve.
This states configured reachability, not inferred model capability. Native
whisper.cpp `/inference` translation is not implemented.

Audio uses the existing key authentication, key and host model allowlists,
per-key RPM/concurrency, bounded global queue, body/read/first-byte/idle/write
deadlines, and one final settlement path. It does not consume token budgets.
Transcription file bytes and other fields are preserved while model and
response_format are normalized (duplicate file/model/response_format fields are
refused). Default/json requests ask the engine for verbose_json to obtain duration,
then return only {"text"}; explicit verbose_json/text/srt/vtt retain their formats.
Speech JSON values other than normalized model pass through and audio bytes
are flushed as received, with no SSE framing. The transcription request cap is
25 MiB; speech retains the ordinary 4 MiB cap.

Speech reserves characters at admission. Served responses and client cuts after the
first audio byte charge the reservation; upstream failures and pre-audio client cuts
do not. Every dispatched speech request retains its measured characters separately
from the settled charge.
Transcription retains its existing reservation charge on a cut. After response
headers, an audio failure aborts the downstream stream rather than completing a
truncated 200 response; the existing settlement path still runs.
Audio-engine 429s become 503 `upstream_down`, preserving Retry-After (HTTP dates are
converted to seconds), and these busy refusals release the RPM entry. The speech
caller retries at most twice: after 3 seconds, then the returned hint, with abortable
waits and a 120-second maximum automatic delay.

Usage JSONL adds `kind` (`transcription` or `speech`), `seconds`,
`reserved_seconds`, `overrun_seconds`, `seconds_estimated`, and `characters` as
applicable. Characters count Unicode code points. Neither audio nor transcription
or speech text is written by the gateway, even with `--log-prompts`. Aggregate
settled meters restore daily budgets on restart; unreadable or malformed
usage history refuses audio rather than silently resetting its budget.
`status` and the startup banner name both configured audio routes and engines.

### Run core

`internal/run` separates the lifetime of a run from an HTTP request and from engine
capacity. The host constructs the store and manager before exposing the gateway, runs startup
and periodic expiry sweeps, and cancels the manager before draining listeners.
An explicitly configured image engine registers the image run kind. A trusted kind chooses a
step, a tool/approval wait, or a terminal output; clients cannot submit executable
code as a kind. Each engine step has a distinct attempt ID, persisted before
execution, and an injected executor returns only after releasing and settling its
resources. `StepResult.Usage` carries the resulting usage event (including meters
when supplied by the gateway); run totals must not charge those steps again.

States are queued, running, waiting, done, failed and cancelled. Waiting holds no
engine or key capacity. Cancellation is cooperative, terminal states are final,
and a waiting event is published only when an immediate internal resume is safe.
Interactive/planted are stored labels; they do not change destination FIFO order.
On restart, unfinished runs become failed/interrupted and unsettled attempts are
marked accounting-uncertain. Engine work is never replayed. This does not promise
crash-exact accounting across the run snapshot and the usage recorder.

A per-key atomic snapshot commits state and its event sequence together before
notifying subscribers. The last 256 events support cursor replay; an old cursor
gets an explicit reset inventory, while malformed, foreign-epoch and future
cursors are refused. Each key has at most two subscribers. A slow subscriber is
closed and must reconnect, so it cannot block an executor. The friend-facing `GET /v1/events` sends this log as SSE with `Last-Event-ID` or
a `cursor` query. It rechecks the key during streaming. An unknown/previous epoch
is refused; reconnect without a cursor to receive the current reset inventory.

### Run gateway adapter

Authenticated `POST /v1/runs` accepts `{kind, input, priority?}` and returns 202 with
an ID only after durable creation. No kind is registered in production yet, so
submission currently refuses unknown kinds. `GET /v1/runs/{id}` reads the owner's
run; `DELETE` requests idempotent cancellation, including a concurrent terminal
transition. Foreign and absent IDs both return not-found. These control routes
record zero-resource usage events; they do not charge the model step twice.
Run submission bodies are capped and globally bounded before reading, without
holding the new step's key/engine admission. SSE has two subscribers per key,
write deadlines and keepalives; slow readers disconnect and replay on reconnect.
Admin `GET /runs?key_id=` stays behind admin authentication and returns metadata
only, at most 100 entries with `truncated` if an all-key list exceeds that bound.
Admin `GET /stored?key_id=` reports one key’s storage metadata; `DELETE` takes
`{cursor,terminal,images,cleanup}` from the confirmation and refuses changed counts
or cursor with 409. GET counts persisted terminal records under the store lock only.
Clear skips manager-owned workers and returns actual cleared/skipped counts; after
a committed clear, cleanup/read failures return 200 with a warning. It commits terminal removal,
validated image cleanup intents and an epoch reset together, then publishes Reset
with remaining summaries. Cleanup intents survive restart in the same snapshot.

The injected gateway `ExecuteStep` adapter resolves the current key by ID, creates
the existing request owner and uses its normal pipeline. It currently accepts JSON
chat-completion and embedding steps; audio/image consumers require their own
capability ticket. Its bounded sink retains at most 1 MiB; a streaming result is
stored as a JSON string containing SSE, and also obeys the encoded output cap.
The result is observed only after `q.finish` releases capacity and records the
same `Meters` and `SettledAt` returned to the run. An HTTP 200 stream ending early
is a failed step, not success. No self-HTTP request, stored bearer or second ledger
is introduced. Run SSE never keeps a model slot and exits on manager shutdown.

Run steps do not update the console’s last-seen or connected-session indicators;
those still reflect ordinary gateway requests and tunnel sessions.


### Managed coding-agent providers

`internal/agentconfig` owns only provider spans for `connect --configure opencode,dsh`:
an extra JSONC file selected with `OPENCODE_CONFIG`, and a YAML block-map insertion under
Harness `llm-pi-ai.providers`. It never serializes surrounding settings or changes
`agent-default-model`; `/me` supplies model IDs and available limits/capabilities, and the
listener supplies the local URL. Global receipts under the user config directory
`infercat/agents` bind each span's SHA-256, target, before-hash and connection owner; a
cross-process file lock serializes operations. Backups are written once and pending receipts
precede target replacement, so an interrupted process can be cleaned up with `--unconfigure`.
Removal checks the span and preserves other bytes, refusing changed markers, duplicate spans,
symlinks or foreign entries depending on managed parent mappings. A later connection cannot
claim an existing owner; an older connection's exit cannot remove a newer owner's span.
`status` reads these local receipts independently of the admin endpoint. Replacements use
synced temporary files and rename, preserving existing permissions; external editors do not
share our lock, so simultaneous edits during replacement are not a transactional collaboration
protocol. No invite or gateway bearer is written into agent settings. Windows locking is
compiled separately; native Windows paths/shell invocation remain unproved.

## Responses (142)

`POST /v1/responses` is a request-local, stateless adapter over chat completions. After
authentication, health, early key admission and the bounded body read, it maps instructions,
messages (text/images), function calls and their outputs into chat messages. The engine sees
`/v1/chat/completions`; usage keeps the friend's `/v1/responses` endpoint. The existing tokenizer,
model policy, output clamp, context/TPM/daily fitting and text queue apply; `q.finish` alone
settles/releases. Stream estimates count original content, reasoning or tool-argument delta chunks,
never the Responses envelopes. Reported chat usage remains authoritative; the Responses wire omits
usage when the engine supplied none. Internal settlement estimates are never presented as engine
usage.

Client reasoning items, including `encrypted_content`, are accepted as opaque bookkeeping but never
decoded, inserted into chat messages, or fabricated. The client carries its own history; the host
stores no response chain. Actual backend reasoning produces a summary-only reasoning item, never
encrypted content. Non-stream output puts reasoning before the message; streams preserve arrival
order.

Client function tools are translated mechanically. Namespace functions receive deterministic
chat-safe aliases derived from namespace and function name, with collision/duplicate checks; output
calls recover both original names. History never declares tools: the pinned Codex serializer
preserves namespace/name separately; a bare history name resolves only to a unique declared function
across namespaces. Ambiguous or undeclared history names refuse with a namespace diagnostic.
Undeclared calls generated by the engine keep their raw name for the client to handle. This is a
mapping guarantee, not a multi-agent compatibility claim. Instructions or schemas inside tool
definitions remain data. Hosted tools cause a whole-request 400 `invalid_request`, even if the model
might not have chosen them. For Codex 0.154.0, set `web_search = "disabled"` (config_toml.rs
`web_search`, WebSearchMode::Disabled, core/src/tools/hosted_spec.rs in tag `rust-v0.154.0`).
Non-null chaining identifiers, `store:true`, background mode, WebSockets, item references and
unsupported input or tool types are also refused with the existing gateway error shape.

Output ids and indices stay stable for the request. Streaming follows the [Responses function-call
lifecycle](https://developers.openai.com/api/docs/guides/function-calling#streaming):
created/in-progress, added items/parts, deltas, part/argument done, item done, then completed with
engine-reported usage when present. A missing finish reason before `[DONE]`, an unknown finish
reason, length truncation or a content-filter finish ends with `response.incomplete`. Early EOF or
bare `[DONE]` fails. After the head, failures use `response.failed` with the existing code and host
sentence, same id and next sequence; no chat error frame or `[DONE]`. Delivery is best effort under
the existing write deadline, without retries or deadline extensions; disconnects remain recorded
even when no terminal frame can reach the client. Stream source and encoded response/event assembly
use the existing 64 MiB output bound. Non-stream output uses the same item mapping. No
`/responses/compact` or WebSocket service is added; Codex's non-OpenAI provider mode compacts
locally through the same stateless route.

| Compatibility path | Evidence |
|---|---|
| Existing chat clients → current host | Original chat stream bytes, accounting invariants and host compatibility suite unchanged; route matrix tests exercise the shared clamp and auth. |
| Codex 0.154.0 → current host via native connect | Two turns, a plain function call, persisted item history and terminal usage; no hosted search. |
| Original 133 requests → current host | Verbatim refusal fixtures: both advertise hosted web search. Supported projections are labelled derived, not captured. |
| Item-finalisation omissions | 133 captured event fixtures and generated streams: missing completed fails the turn; missing item-done loses client history. |

The current web client does not consume Responses; its versioned `/me` fixtures
and strict shape guards remain unchanged. Older hosts have no Responses route.

Omitted `store` means not stored here; explicit `store:true` is refused. The adapter
ignores `include`, `prompt_cache_key`, `metadata`, `client_metadata`, `service_tier`,
`user` and `truncation`. It never truncates input: an overlong context remains a
422. These ignored fields do not enter the engine prompt. GET on this POST-only
route is 404; WebSocket upgrades remain explicitly unsupported.

## Image runs (144a)

An explicitly configured `--upstream-images URL` is probed through `/v1/models`
(or `/health`); `--upstream-images-key` and `--upstream-images-model` follow the audio
engine settings. An absent image model leaves the capability absent. `/me` adds
optional `host.images {model, retention_days, queue_cap, queued}`, image limits
`daily_images` (default 20) and `max_queued_images` (default 8), and
`usage.today_images`. Zero/absent legacy fields take the defaults without rewriting
stored keys. Queue caps and image daily limits can be edited through the key
CLI/admin surface. Text/audio request-concurrency limits exclude image work;
images retain the shared RPM ledger and their own resource reservation.

`POST /v1/images/jobs` accepts `{prompts:[string], conversation?:string, client_request_id?:string}` and
atomically admits one batch, returning 202 `{jobs:[run]}`. One prompt is interactive;
several are planted. One image worker chooses interactive before planted and FIFO
within each priority; running work is not preempted. `GET /v1/images/jobs` lists the
key's image runs newest first, with `batch {id,index,count}` and queue `position`.
`DELETE /v1/runs/{id}` cancels queued images immediately; for a running image it
records cancellation but allows that image to finish Done, retaining its output.

`GET /v1/images/outputs/{id}` returns only this key's PNG/JPEG bytes;
`?download=1` adds an attachment disposition. `DELETE` discards the output.
`POST /v1/images/generations` submits through the same worker and waits, returning
`{created,data:[{b64_json}]}`. It accepts prompt, n (1–16, subject to the queue cap),
1024x1024 size, optional configured model and b64_json format. A disconnected waiter
does not cancel or replay its durable jobs. The engine must return bounded base64
PNG/JPEG data; URL-only results are refused, never fetched. Native sd.cpp embedded
control directives are refused in prompts so they cannot override the host's
single-output dimensions/settings. Current decoded dimensions are bounded to 4096².

The `images` meter reserves one image before dispatch. A definitive engine failure
without output releases it; a valid output or ambiguous dispatched outcome charges
one. Measured is one only for a valid image. Settlement uses the existing request
owner and UTC accounting window; no crash-exact charging guarantee is added.
The named refusal codes are `image_queue_full` and `image_budget_exhausted` (429).

Each image run keeps `input {prompt, conversation?, client_request_id?}`. Both
optional strings are bounded to 128 bytes. The client persists its opaque
correlation id before submitting; an ambiguous response is reconciled by listing
this key's runs, never by resubmitting. Correlation does not deduplicate: repeating
an id creates a separate batch. Batch indexes are zero-based. `started` is the
actual worker-acquisition timestamp; final elapsed is `updated - started`.
The list and individual image-run GET/DELETE snapshots include `position` (zero
when not queued). Each output is `{url,mime,w,h,bytes,expiresAt,gone?}`; metadata
is sent without image bytes. One admission event names a batch's first sibling;
clients refresh the whole image list on any run event and after reconnect, so
all siblings, current positions and evictions are observed together.


### Image admission and failure boundaries (156, 159)

The batch's daily image reservation and queued cap now admit together, before any
run is committed. A refused batch (including the synchronous route) creates no
failed rows. Commit failure rolls the reservation back; queued cancellation and
pre-dispatch failure release unused holds. The gateway request owner alone charges
completed or ambiguous dispatched work. Outstanding holds carry over UTC midnight;
charges belong to the settlement day. Restart interrupts queued work without replay.
Zero image limits mean defaults. Negative daily limits mean unlimited (-1); negative
queue limits use the live-run bound (16), reported effectively in `/me`.

One owned HTTP image request runs at a time, with a 15-minute complete-response
ceiling. Successful request-body write establishes dispatch. Sent-but-lost work
uses `image_abandoned` with cause-specific, charged wording; the next dispatch waits
at most 60 seconds for a fresh successful health probe. The next unstarted job keeps
its original hold during that one recovery window; expiry fails it with
`upstream_down` and Retry-After, releases the hold, and frees the worker. Further
jobs fail fast until recovery. Recovery drives bounded Refresh every 3 seconds. A failed probe overlapping an
owned request within max(3 minutes, twice the maximum successfully stored generation duration in this process),
capped at the 15-minute ceiling, is unknown; afterward it marks health down.
Only a valid decoded and successfully stored image trains that duration; faster
successes never shrink it, and restart resets the grace to 3 minutes. Sync completion polls separately
at 100 ms, not at the probe cadence. A remote server may keep computing after a lost connection;
health does not prove remote work stopped. Host shutdown after dispatch records
charged `image_abandoned` (unless suspect); before dispatch it records released
`interrupted` as a run reason only, never an HTTP error code.
After two dispatched charge-eligible failures with no measured success, the
destination becomes suspect. Invalid nonempty output counts too. The first two
charge; further such failures release. Only these breaker-counted outcomes advance
backoff from 1 minute to a 15-minute cap; definitive no-output does not, except
≥12 MiB unparsed responses: released, logged, and breaker-counted. Successful
generation clears it. Process-local abandon
count and retry timestamp are logged and exposed in admin destination status;
restart resets this protection, not usage. The offered `/me.host.images` includes
optional future RFC3339 `retry_at` while suspect (omitted after the wait elapses).
Gateway-authored failure text and retry seconds persist in attempt error output;
upstream-authored snippets do not. Host storage faults produce `storage_failed` (HTTP 500 / server_error),
release the hold, and neither train the grace nor alter the engine breaker. No nonempty b64_json candidate (including URL-only) is definitive no-output and
releases the hold, including responses reaching the 12 MiB parse boundary;
malformed nonempty candidates or invalid image bytes are charge-eligible.

Each configured upstream is re-probed periodically. The host model pin applies to
text only; key allowlists still apply to image/audio models. The friend's HTTP
submissions spend RPM once per batch or synchronous call; internal SubmitBatch and
worker dispatch spend none. Refused submissions refund RPM; per-key caps/reservation
precede any host-wide walk. Output GETs are RPM-exempt; list/per-run reads/cancels
and output DELETE spend RPM,
without taking text/audio MaxConcurrent slots; missing run reads/cancels refund
RPM. Output GETs hold a dedicated per-key read slot through delivery: 4 concurrent,
then 429 with Retry-After 1. List positions use one snapshot;
per-run positions use the scheduler's cached snapshot. Artifact reads release the
store mutex before reading bytes. Clients coalesce
refreshes and respect 429. The 256 MiB image budget counts retained, servable bytes;
unlinkable stale files are logged and retried, never marking a key broken. Status
reports `image_cleanup_pending` for proven retries and `image_orphans_for_review`
for report-only observations. If either is nonzero, it prints "N awaiting cleanup,
M for review". The Go `ImageCleanupPending()` accessor remains their sum.

### Loadout profiles and setup

`internal/profile/data/*.json` embeds one independent version-1 JSON file per tier.
There is no online refresh. `apple-64g` uses the measured E4B Q4 substitute; the
intended 27–32B anchor remains unmeasured. `apple-16g` carries both E4B candidates
under `pending founder decision` and cannot write config. `nvidia-12g` is wholly
unmeasured. BGE-M3 never inherits the measured BGE-small working set. Policy and
headroom reservations are explicitly draft, not measured performance guarantees.

| Object | Fields and units |
| --- | --- |
| Profile | `version`, `id`, `hardware`, `members`, `artifacts`, `headroom`, `promise` |
| Hardware | `os`, `arch`, `gpu` (metal/nvidia/cpu), `ram_bytes`, `vram_bytes`, `measured_on`; RAM/VRAM are compatibility minima |
| Member | `id`, `class` (text/transcribe/speech/embed/image), `engine`, `engine_pin`, optional `artifact` id, `model`, `command`, `env`, `port`, `context` tokens, `concurrency`, `policy`, `unavailable`; only a pending text anchor has `pending` and two `candidates` instead of a selected model |
| Model/candidate | `name` is the advertised API model id; `quantization`, `assets`, `extra_args`, `measurement` |
| Asset | `id`, basename `file`, `bytes`, lowercase `sha256`, HTTPS publisher `url`, `license`, `revision`; support bundles are pinned assets too; optional `archive` describes extraction |
| Archive/artifact | `archive.format` is tar.gz/tar.bz2/zip, `strip` is 0–4 leading components, `bytes` is the expanded ceiling (at most 8 GiB). Profile `artifacts` contain asset pins plus an optional relative `executable`; a member selects one by id |
| Measurement | `status` (measured/unmeasured), `date`, `source`, `rss_bytes`, `tokens_per_second`, `mixed_tokens_per_second`, `ttft_ms`, `note`; notes identify the measured workload/pair, and unmeasured numeric fields stay zero |
| Policy/headroom | `policy.kind` is resident/on-demand/cpu; only on-demand has positive `idle_seconds`. Headroom has `os_bytes`, `kv_bytes_per_slot`, `friends`, `draft`. The shipped draft reserves 6 GiB OS/app and 512 MiB per 16K text slot, two friends, 600-second pool idle |

Parsing refuses unknown fields, duplicate keys, nulls, control characters, invalid
pins/limits and unsupported versions. Profile files are capped at 256 KiB and 16
artifacts. Commands from custom files remain inert. Built-in managed commands expand
`{port}`, `{model}`, `{model_name}`, `{asset:ID}`, `{config}`, `{model_dir}` and
`{artifact:ID}` into argv/environment without a shell; unknown placeholders refuse.
The executable comes only from the selected verified artifact. Audio.cpp receives a
private generated Fun-ASR config; environment defaults are PATH and a member-local HOME.

Setup reads RAM/GPU/VRAM/OS/architecture and free disk using bounded-time OS queries;
unknown fields remain unknown, and NVIDIA VRAM is the largest single device rather
than a sum. Profile selection is fixed tier matching, not a loadout optimizer.
`--custom FILE` validates compatibility only. Cache search covers HF snapshots
(symlink or copy) and blobs, Ollama's content-addressed blobs used by manifests,
and modern/legacy LM Studio model directories. `HF_HUB_CACHE`, `HF_HOME`,
`XDG_CACHE_HOME` and `OLLAMA_MODELS` override defaults. Repeatable `--model-path
MEMBER=PATH` or `ASSET=PATH` supplies an explicit file. Matching sizes are SHA-256
checked; neither filenames nor a running server attest loaded weight bytes.

Only fixed loopback ports are probed, with no redirects/proxy credentials, a
1 MiB response cap and a 15-second request timeout. `/v1/models` establishes health
and advertised model identity. External engines are reused without lifecycle ownership;
their active weights are not attested by health. Pending anchors and conflicting
upstream settings refuse. Custom mode keeps the existing compatibility-only path.

Built-in setup holds the data-directory lock, fetches missing assets and shows each
model's licence names once per setup. Downloads use HTTPS, a one-hour request bound,
exact pinned size, and SHA-256 before use. A partial file is resumed only with an
exact matching Content-Range; a server ignoring Range restarts the partial. Short
or cancelled transfers retain only a partial file. Oversize/hash mismatches never
publish a usable download. Existing cached downloads are rehashed before reuse.

Archives extract into fresh private trees. Paths cannot be absolute or traverse
parents; duplicate entries, hardlinks and device entries refuse. Expanded bytes and
100,000 entries bound each extraction. Symlinks publish last, then every resolved
link is checked to stay inside the tree; failed extractions remove their partial tree.
Completed downloads and extracted trees may remain after a later failure, but the
anchor/config refusals leave the active host configuration unchanged. An ordinary optional
member failure is recorded as unavailable and its profile-owned endpoint is cleared.
Profile-marked unavailable members are neither fetched nor probed.

`internal/supervise` shares the agent guardian's lifetime pipe, process-group cleanup
and 1 MiB wrapping logs. Managed dry checks fail on an occupied port, wait at most
two minutes for model readiness, then send the anchor prompt once. Engines without
socket inheritance still perform their own bind after the port precheck. Each owned
process stops before setup returns; descendants that escape its process group are
outside this guardian's containment. Agent NDJSON and inherited-health semantics are
unchanged. Speech selects the published helpers-v0.1.0 artifact for Darwin arm64
or Linux amd64 and the separately pinned sherpa runtime. Its closed child environment
points the library loader at that verified runtime; no system library installation
is used. The complete pinned Kokoro support archive is extracted for --model-dir;
existing individual model/voice asset ids remain accepted. ASR has no selected
profile artifact yet.

A content-addressed `profiles/install-<sha256>.json` records the profile version/digest,
canonical model/artifact paths, regular-file hashes and symlink targets, external
ownership, unavailable reasons and the materialized command/environment/directory.
Setup also prints that command for stopped members. Its bounded strict reader rejects unknown/duplicate fields, malformed
inventory, a missing anchor and a profile-digest mismatch. The record describes the
verified installation; reading the record alone does not re-attest current disk bytes.
Setup writes the manifest before atomically replacing 0600 `config.json` with its
`profile_install` reference and verified supported endpoints, preserving unrelated
settings. A manifest/config failure leaves the previous config intact; an unreferenced
manifest can remain for diagnosis. After a successful config commit, setup retires real
child trees under `profiles/trees` that the new manifest no longer references; failed
setups do not retire anything. No credentials are inferred or embedded.

Serve validates the installation against the current embedded digest; a mismatch
requires setup again. Before each owned start it rechecks selected file hashes and
symlink targets, then launches the materialized command through the shared guardian.
The text anchor starts before serving; resident/cpu members remain running, and
on-demand members start only for authenticated, admitted work. Startup is coalesced
per member and bounded to two minutes; one cancelled waiter does not cancel another.
The request’s body deadline is suspended during that wait and reset to 30 seconds
afterward; unmanaged requests retain their existing deadline.

A managed lease precedes health/tokenizer work and is released after `q.finish`.
Image execution retains it through output transfer and settlement. The fixed idle
countdown starts on the last release. Process death and three consecutive idle
health failures allow restart with bounded backoff (1–30 seconds). An on-demand
member requires a live lease to restart; a failed start with no remaining leases
does not reverify or launch in the background. Active leases prevent health probes
from stopping a busy engine. There is no pressure-based
eviction or cross-member scheduling. Shutdown closes all owned process groups;
external endpoints are probed but never started, restarted or stopped.

Status lists member states and observed health without treating dormant as healthy.
`/me` and image-job admission advertise dormant configured offers from pinned model
metadata without waking engines; failed members are unavailable. `/v1/embeddings`
uses its own destination, queue and lease when a profile supplies one, preserving
key model restrictions without applying the text anchor’s host pin to embeddings.
`host.embeddings` is additive in `/me`; hosts without a separate embedding member
retain the existing text-engine route. Native speech installation and streaming WAV
playback are proved on Darwin; Linux helper pins are verified, while native Linux
execution and the NVIDIA profile remain unmeasured.

### Native speech helper and helper artifacts

`infercat-speech` is a separate, tagged CGO build, dynamically linked to sherpa-onnx
v1.13.7's C API. The host remains CGO-free. Kokoro v1.1-zh loads once; a single
synthesis slot refuses concurrent requests with 429. The HTTP adapter bounds input,
output and duration, flushes each native batch as PCM16 (optionally in a streaming
WAV envelope), and cancels at callback boundaries. A failed partial generation aborts
the HTTP response. Admission uses script weights and a conservative nonlinear
speed curve calibrated to 90 seconds, below the 120-second safety cap. The full
voice set comes from the pinned bundle. Go normalizes dates, decimals and phone
digits only in Han-containing input; no global Chinese FST rewrites English digits. The browser requests the server's default format and its existing
player respects the returned MIME; WAV playback waits for the completed Blob.

The helper release workflow builds Darwin arm64 and Linux amd64 artifacts from pinned
audio.cpp and sherpa inputs. It verifies sherpa's published archive digest before
compilation, builds the Fun-ASR-Nano audio server, checks dynamic dependencies and
runs both relocated binaries. Our archives exclude sherpa/espeak-ng and model data;
the separately fetched sherpa libraries occupy `sherpa/lib` beside `bin`. Manifests
record source revisions, binary hashes and the voice-list hash, with archive checksums
alongside them.
The helper production pin remains absent from setup until publication; no CI snapshot
URL substitutes for it. Publication follows the founder's release decision.

## Host tools in chat (148)

Only `POST /v1/chat/completions` with a nonempty `host_tools` selection (`make_image`, `web_search`) opts into
host execution. An absent/empty array keeps ordinary chat; malformed or unknown
names, or nonempty opt-in with caller `tools`/`tool_choice`, are refused. No client
is detected and no tools are injected into third-party requests without this field.
The offer is the requested subset of `/me.host_tools`: key-filtered images and
operator-configured search. With no available requested tool, chat stays ordinary. `conversation` and `client_request_id` are optional
128-byte correlation strings on the chat/image records, removed from the model
input. They confer neither authority nor deduplication.

Early admission and body reading are unchanged. Validated body routing durably
creates a `chat` run, then the outer owner finishes before opening the consumer's
gate. That request records one zero-charge `run_handoff` app row; each inner
model attempt has its own normal admission, RPM, meter and `q.finish`. No outer
text slot is held while an inner attempt acquires one, including at concurrency 1.
Streaming starts with `event: run` and `{"run_id":...}`, followed by ordinary chat
text/reasoning deltas. Non-streamed replies carry `run_id` too. The shared
`Step.Observe` borrows bytes into a bounded forwarder; a slow/disconnected reader
ends the attempt through its normal settlement owner. Final success follows the
durable run terminal state, never just the model's last delta.

A turn allows three tool rounds (at most two dispatched searches), hence up to
four separately metered model calls. Each round executes one requested, available
tool. Malformed, unknown or multiple calls execute none and return a tool-error
result before a final prose call. The last permitted round sets `tool_choice:none`;
a further call is refused while emitted prose is preserved. Incomplete tool calls
never execute. Each `make_image` round rechecks the key/offer and atomically submits
its own interactive batch after retaining the executing step. Parent-run and
tool-call associations retain their existing bounds and recovery semantics.

`web_search` uses the operator's startup-loaded Exa credential; config stores only
`search.key_file`. The bounded request contains query, result count and highlight
options, with no friend identity. No retries, redirects or page fetching; timeout
10 s, response body 128 KiB, plain-text output 16 KiB. The typed search step captures
exactly the title/URL/snippet text sent back to the model. Empty/error/timeout
results let the model continue. Each attempted provider call records one `search`
row with class `search`, unit `requests`, charged 1 and measured 1 only for a valid
response. It reserves and settles under the existing per-key meter lock; cancellation
before dispatch releases without a row. `SettledAt` drives restart/day accounting.
The separate `search_per_day` budget defaults to 50 (negative unlimited), is exposed
with `usage.today_searches`, and refuses as `budget_exhausted` until UTC midnight.
It spends neither text RPM nor tokens. Provider bodies and queries never enter the
search telemetry row; captured text stays in the run's existing retained owner.
The disclosed `--log-prompts` opt-in still logs tool queries/results within messages.
Search rows contribute only their resource meters to day/key/via totals, not HTTP
request/model/poll counts, latency percentiles or LastCall.

HTTP disconnect and DELETE request Stop for the chat; committed image jobs remain
independently owned. The originating delivery is tied to the durable run id and
cannot be claimed by copying its input. Restart never replays a chat. If image
submission committed before the parent result, recovery marks its executing step
interrupted with an unconfirmed outcome; the child associations allow inspection,
not automatic resubmission. Shared retained steps and GET/SSE replacement semantics
remain the single run representation.

`chat` registers `InProcess` with non-serial `JoinCancel`: it has no shared external
runtime to force-stop. Cancel cooperatively joins the consumer within the existing
bound. On expiry, the friend sees `cancelled` and the host logs the late consumer,
but its retained lease and settlement stay owned until it actually returns. New
chats continue; no kind-wide quarantine applies. Each model attempt's own deadlines
still bound its return. External-runtime Serial/ForceStop/quarantine rules are unchanged.
