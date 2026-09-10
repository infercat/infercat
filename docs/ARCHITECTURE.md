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

Tailcat 0.6.0 clients accept both old addresses and addresses carrying a WireGuard pre-shared
key (PSK). Hosts keep `DisablePresharedKey: true` and omit the PSK from every minted address
so existing invites stay byte-identical. Old identity files remain untouched; new files store
a PSK at rest but do not use it yet. A later migration must use an `ic2` invite prefix: old
clients otherwise ignore the extra address field and stall at the handshake, rather than
showing the existing newer-app message. This release does not enable PSKs on hosts.

## Go layout and ownership (scope contracts)

| Path | Owner ticket | Purpose |
|---|---|---|
| `cmd/infercat/` | 003 | CLI: `serve`, `keys …`, `status`, `usage`, `invite` |
| `internal/product/` | PM | name constants |
| `internal/invite/` | 001 | invite encode/decode (`ic1.<tc>.<secret>`) |
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
| `host.key.json` | 001/009 | tailcat `PrivateKey` JSON: the host identity, created once (exclusive link; a racing second `serve` adopts the winner). `Addr() == SavedAddr(dir)` always. `--ephemeral` never writes it. |
| `keys.json` | 003 | see Key store below; gateway re-reads on mtime change (≤1/s, plus on any lookup miss); the CLI pokes `POST /reload` on the admin socket after every write so changes are live at once |
| `usage.jsonl` | 003 | one `usage.Event` per line, append-only |
| `admin.sock` | 003/009 | unix socket, HTTP: `GET /status`, `POST /reload` (Windows: loopback port in `admin.port`); every platform authenticates with per-run `admin.token` |
| `config.json` | 003 | persisted `serve` settings: `upstream`, `upstream_key` (0600), `slots`, `dev_listen`, `derpmap_url`, `region`, `name`, `web_url`. `--log-prompts`, `--ephemeral`, `--verbose` are per-run and never persisted (Protection 3). Retired keys (`queue_timeout`, `request_timeout`, `max_body`) are ignored on load. |
| `tunnel.log` | 005 | the tailcat/wgengine log (truncated at start); `serve --verbose` prints it instead |

## Invite format (001 defines in Go, 004 mirrors in TS; MUST match)

```
ic1.<tailcat address>.<secret>
```
- `ic1` = format version. Unknown prefix → "this invite needs a newer app".
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
| `POST /v1/chat/completions` | stream and non-stream. Gateway MUST: flush every SSE chunk immediately; inject `stream_options.include_usage=true` when streaming; normalize the body once (strip engine-override aliases such as `n_predict`/`n`/`best_of`/`priority`, fill `model`, clamp `max_tokens` to the key's cap and shrink it to fit the context and the TPM/daily windows, floor 16); pass `reasoning_content` through untouched. An engine 400/422 caused by the request maps to 400 `invalid_request` with the engine's message — except a context overflow (llama.cpp `exceed_context_size_error`, vLLM "maximum context length"), which maps to 422 `context_too_long` so clients never retry it (036); 5xx → 502 |
| `POST /v1/embeddings` | pass-through with auth + limits |
| anything else | 404 in error format |

Error format (OpenAI-shaped, MUST):
```json
{"error":{"message":"human sentence","type":"invalid_request_error|authentication_error|permission_error|rate_limit_error|upstream_error","code":"invalid_request|invalid_key|key_paused|key_revoked|model_not_allowed|not_found|body_too_large|context_too_long|rate_limited|concurrency_limited|budget_exhausted|queue_timeout|upstream_down|upstream_error"}}
```
Statuses: 400 invalid_request (malformed JSON/body) · 404 not_found · 401 invalid_key · 403 key_paused/key_revoked/model_not_allowed · 413 body_too_large · 422 context_too_long · 429 rate_limited/concurrency_limited/budget_exhausted (+ `Retry-After` seconds) · 503 queue_timeout/upstream_down (+ `Retry-After`) · 502 upstream_error.

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
Text routes remain chat, embeddings and models; absent audio routes remain `not_found`, unloaded
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
non-stream cut, deltas seen for a stream. RPM counts model calls (`/v1/chat/completions`,
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
whitelist. A separate `ia1.<address>.<secret>` bearer controls keys and settings, never `/v1`.
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

Usage JSONL adds `kind` (`transcription` or `speech`), `seconds`,
`reserved_seconds`, `overrun_seconds`, `seconds_estimated`, and `characters` as
applicable. Characters count Unicode code points. Neither audio nor transcription
or speech text is written by the gateway, even with `--log-prompts`. Aggregate
seconds/characters restore daily budgets on restart; unreadable or malformed
usage history refuses audio rather than silently resetting its budget.
`status` and the startup banner name both configured audio routes and engines.
