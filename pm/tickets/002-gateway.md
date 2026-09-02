---
id: 002
title: Gateway — auth, per-key limits, queue, clamps, streaming proxy, usage
kind: sensitive
size: 5
status: landed
updated: 2026-09-02
release: demo-1
---

# 002 — Gateway

## Binding

**Why.** The gateway is the commercial core: it is what lets a host hand out invites to strangers and
sleep. It sits between the tunnel listener and the upstream engine and enforces every promise the host
makes to itself (pm/BELIEFS.md Protections 1–4).

**Promises.**
1. `internal/gateway`: `New(cfg Config, up upstream.Upstream, store keys.Store, rec usage.Recorder, logf) *Gateway`
   with `Handler() http.Handler`, `Serve(l net.Listener) error`, `ServeDev(addr string) error` (loopback
   only; refuses anything else; permissive CORS incl. preflight), `Shutdown(ctx)`, and an implementation
   of `usage.Snapshot`. `Config` carries: `Slots int` (global concurrency), `QueueTimeout`,
   `RequestTimeout`, `MaxBody int64`, `LogPrompts bool`, `HostName string`, `RelayRegion func() string`.
2. **Auth.** Every route except `/healthz` requires `Authorization: Bearer <secret>`; `store.Lookup` decides.
   Missing/unknown → 401 `invalid_key`; paused → 403 `key_paused`; revoked → 403 `key_revoked`. The secret
   never appears in logs or usage events.
3. **Routes and error format** exactly as `docs/ARCHITECTURE.md` §Gateway HTTP API, including `/me`,
   `/v1/models` filtered by `Key.AllowsModel`, `/v1/embeddings` pass-through, 404 for anything else.
   Every error body is the OpenAI-shaped JSON; 429 and 503 carry `Retry-After` in whole seconds.
4. **Per-key limits**, all from `Key.Limits`: RPM (token bucket or fixed window, your call, document it),
   TPM (sliding 60 s window over prompt+completion tokens), daily budget (UTC day, prompt+completion),
   `max_concurrent`, model allowlist (403 `model_not_allowed`), `max_output_tokens` (clamp, never reject),
   `max_context` (422 `context_too_long` when prompt tokens exceed the effective context minus the clamped
   `max_tokens`; effective context = min(key.MaxContext if >0, upstream.Info().ModelContext if >0)).
   Token counts for the pre-check come from `upstream.CountTokens` over the concatenated message
   contents (text parts only; image parts count as 0 for the pre-check).
5. **Global concurrency and queue.** A semaphore of `cfg.Slots` around the upstream call; waiting bounded
   by `QueueTimeout` → 503 `queue_timeout`. Per-key semaphore of `max_concurrent` → immediate 429
   `concurrency_limited` (no queueing for per-key; document why: one person's burst must not hold a slot
   for others). Upstream unreachable → 503 `upstream_down`; upstream 5xx/garbled → 502 `upstream_error`
   with the upstream status in the message.
6. **Streaming.** For `stream: true`: inject `stream_options: {include_usage: true}` if absent; proxy
   with immediate flush per SSE event (no buffering); pass `reasoning_content` deltas through untouched;
   client disconnect cancels the upstream request. For non-stream: pass through. In both cases parse
   `usage` from the response (final chunk or body) to fill the usage event and the TPM/daily counters;
   when absent, fall back to the pre-check count for prompt tokens and 0 for completion.
7. **Body handling.** Reject bodies over `MaxBody` with 413 before reading further. Requests with a
   missing `model` get the upstream's first model (or the key's first allowed model) filled in. Unknown
   fields pass through untouched. Request timeout via context.
8. **Usage.** One `usage.Event` per request (including rejected ones, with `Status`/`Code`), with
   `queued_ms`, `ttft_ms` (first byte written to the client), `total_ms`. Prompt/completion text only
   when `LogPrompts`. Counters for `/me` and `Snapshot` are in-memory and survive nothing (restart = zero;
   daily budget therefore resets on restart — document as a known v1 limitation).
9. **Hot reload is the store's job**, but the gateway must call `Lookup` per request (no caching of keys).
10. **Evidence.** `go test ./internal/gateway/...` at "coverage plus adversarial fixtures" using an
    `httptest` fake upstream that (a) streams SSE with 50 ms gaps and (b) can return 500/garbage/hang,
    plus in-memory fakes for `keys.Store`, `usage.Recorder`, `upstream.Upstream`. Required cases: first
    SSE event reaches the client before the upstream finishes (flush proof); include_usage injection;
    reasoning_content passthrough byte-for-byte; every error code and status once; Retry-After present on
    429/503; queue timeout with Slots=1 and two concurrent requests; per-key concurrency; TPM and daily
    budget exhaustion after real usage; context_too_long boundary; body cap; model allowlist; missing
    model fill; client disconnect cancels upstream (fake observes context cancellation); secret absent
    from all log output (capture logf); `/me` shape; dev CORS preflight; ServeDev refuses non-loopback.
    Print the test output in the report.

**Size 5** (≤2000 source lines). Concept budget 6: token bucket, sliding window, daily budget, global
queue, per-key concurrency, error code table. No plugins, no policy language, no per-route limits.
**Sensitive** (Protections 1, 2, 4). Adversarial review after landing.

**Scope contract.** `internal/gateway/**` only, plus `go.mod`/`go.sum` if you add a dependency (prefer
stdlib; `golang.org/x/time/rate` is acceptable). The seam packages `internal/keys`, `internal/usage`,
`internal/upstream`, `internal/product` are read-only for you; if a seam is wrong, stop and contest with
the exact symbol.

**Non-goals.** No CLI, no file store, no upstream detection (003). No TLS. No per-route limits. No
persistence of counters. No retries against the upstream.

**Handoff.** Worktree branch `t002-gateway`, rebased on `main`, checks printed green. Report appended
under `## Report` (verified / not verified / judgment calls / anything the PM should re-price). Push.
Do not merge.

## Background (hypotheses)

- llama.cpp `llama-server` b9553 is running locally at `http://127.0.0.1:18080` with Gemma 4 E2B,
  `-np 2`, ctx 8192, for manual checks (`/props`, `/tokenize`, `/v1/chat/completions`). It emits
  `reasoning_content` in deltas. vLLM 0.25 is reachable via `ssh -N -L 8010:127.0.0.1:8010 max@max-ws.lab`
  (read-only; model `entropy-v2-gemma4-12b-w4a16-group128`, 2 seqs, ctx 8192) — do not modify anything
  on that machine. Neither is required for the unit tests.
- `httputil.ReverseProxy` with `FlushInterval: -1` flushes immediately; you may instead hand-roll the
  proxy with `io.Copy` over a flusher if it reads cleaner for SSE + usage parsing. Either is fine.
- Usage in streams arrives as a final chunk with `usage` set and empty `choices` (OpenAI convention;
  llama.cpp and vLLM both follow it with `include_usage`).
- `Retry-After` for rate limits: seconds until the bucket/window admits one more request, minimum 1.

## Log

- 2026-09-02 02:30 ACK. Base f171164 (main), lane t002-gateway. Read BELIEFS, ARCHITECTURE, ticket, seams. Seams sufficient; no contest.
- 2026-09-02 02:35 Design: hand-rolled proxy (no httputil) for SSE flush + usage parse + cancel; RPM and TPM share one sliding 60 s log per key (one mechanism, exact Retry-After); stdlib only, go.mod untouched.
- 2026-09-02 02:35 Judgment: contract code table lacks 400/404 rows; adding `invalid_request` (malformed JSON) and `not_found`. Flagged for PM.
- 2026-09-02 02:35 Observed: local llama-server /props reports n_ctx 4096 per slot (ticket says 8192 = -c over -np 2). Not my concern; noted for 003/MEASURE. (003 later documented the same in docs/MEASURE.md.)
- 2026-09-02 02:32 First cut: 4 source files, 1011 source lines; `go build`/`go vet` clean.
- 2026-09-02 02:33 Tests: 28 cases + TestMain gate (every error code seen, secret absent from logs). Two test-side bugs fixed (RPM assumption; fake clock started 30 s before UTC midnight). `go test -race`: 28 passed / 0 failed / 1 skipped (opt-in live).
- 2026-09-02 02:34 Live check against llama-server 127.0.0.1:18080 (requests only): stream 200, gateway TTFT 63 ms, 176 tok/s, reasoning_content streamed, usage chunk parsed; 422 and 429 with real token counts. Added as `live_test.go` (skips unless BN_LIVE_UPSTREAM).
- 2026-09-02 02:36 Judgment, declared deviation: aborted stream without a usage chunk is charged by delta chunks seen, not 0 (promise 6 says 0) — stop button must not be free. One `if` to strike.
- 2026-09-02 02:38 origin/main moved (003 landed). Rebased; 003's `wire.go` calls `gateway.New(gateway.Config{…})` exactly as designed; added a compile-time assertion mirroring its `gatewayServer` interface.
- 2026-09-02 02:40 origin/main moved again (001 landed, then PM records ×2). Rebased three more times; each time full checks re-run and printed. `go build -tags wire ./...` OK on the final base: 003's real wiring compiles against this gateway with 001 present.
- 2026-09-02 02:42 FROZEN on base 818ef9c. Patch SHA-256 below.

## Report

**Branch** `t002-gateway`, rebased on `origin/main` `818ef9c` (001 and 003 already on main). Scope kept to `internal/gateway/**`; `go.mod`/`go.sum` untouched (stdlib only). Not merged.

### The core, shown working

1. **Streams flush immediately and die with the client.** `TestStreamFlushBeforeUpstreamFinishes`: fake engine emits 5 SSE events 50 ms apart; the first event is in the client's hands while the engine's `finished` flag is still false, first-event time is under half of total, and the client's bytes equal the engine's bytes exactly. `TestClientDisconnectCancelsUpstream`: the client closes after 3 events of a 10 s stream; the engine's `r.Context().Done()` fires; the usage event is `Status:200 Code:client_closed CompletionTokens:3..10`, and the key's in-flight slot is back to 0.
   Live against the local llama-server (Gemma 4 E2B, `BN_LIVE_UPSTREAM=http://127.0.0.1:18080 go test -run Live -v ./internal/gateway/`):
   ```
   upstream: {Kind:llama.cpp URL:http://127.0.0.1:18080 Healthy:true ModelContext:4096 Slots:2 Models:[gemma-4-E2B-it-Q4_K_M.gguf]}
   stream: status 200, ttft 87.285083ms, total 766.708166ms, 117 delta chunks, usage chunk "{PromptTokens:27 CompletionTokens:120}"
   reasoning_content (525 bytes): "1.  **Analyze the request:** The user wants to know why the sky is blue, ..."
   usage event: {… Status:200 Code: Stream:true PromptTokens:27 CompletionTokens:120 QueuedMS:0 TTFTMS:63 TotalMS:766}
   tokens/s (completion tokens / (total - ttft)): 176.6
   non-stream: 200 {"choices":[{"finish_reason":"length",…"reasoning_content":"Thinking Process:…   → event PromptTokens:19 CompletionTokens:20 TTFTMS:137 TotalMS:137
   long prompt with max_context 64: 422 {"error":{"message":"prompt is 202 tokens but only 32 fit: context 64 minus max_tokens 32","type":"invalid_request_error","code":"context_too_long"}}
   second request with rpm 1: 429 Retry-After=60 {"error":{"message":"rate limit: 1 requests per minute; 2 used","type":"rate_limit_error","code":"rate_limited"}}
   ```
   (TTFT 63 ms at the gateway vs 87 ms at the test client: the gateway stamps the first byte it writes — the role chunk — the client stamps the first content delta.)
2. **Every error code, status, type, and the Retry-After rule.** `TestMain` fails the package unless every row of `codeTable` was seen in a real response, and unless the test secret is absent from every captured log line. Output: `all 14 error codes exercised; 9 log lines captured, secret absent`. The harness also fails any test whose response body contains the secret.
3. **Limits from real usage.** `TestTPMAndDailyAfterRealUsage`: two responses reporting 4+4 tokens, then the third request is `429 rate_limited` ("16 used") for TPM 10, and `429 budget_exhausted` with Retry-After = seconds to UTC midnight for a 10-token day; `Counters` agrees (`TPMUsed:16 TodayTokens:16 RPMUsed:2 InFlight:0`). `TestGlobalQueueTimeout`: Slots=1, a second key waits 150 ms then gets `503 queue_timeout` with `queued_ms` recorded. `TestPerKeyConcurrency`: immediate `429 concurrency_limited`, Retry-After 1, `Queue() = (1, 0)`.
4. **003 wires it blind.** `cmd/bunny-network/wire.go` (on main, behind `-tags wire`) calls `gateway.New(gateway.Config{Slots, QueueTimeout, RequestTimeout, MaxBody, LogPrompts, HostName, RelayRegion}, up, store, rec, logf)` and uses `Serve`/`ServeDev`/`Shutdown`/`usage.Snapshot`. `go build -tags wire ./...` → OK on this branch. `gateway.go` carries a compile-time assertion mirroring that interface.

### Exported surface

```go
type Config struct { Slots int; QueueTimeout, RequestTimeout time.Duration; MaxBody int64; LogPrompts bool; HostName string; RelayRegion func() string }
func New(cfg Config, up upstream.Upstream, store keys.Store, rec usage.Recorder, logf func(string, ...any)) *Gateway   // zero values → Slots 1, 30 s, 300 s, 4 MiB
func (g *Gateway) Handler() http.Handler
func (g *Gateway) Serve(l net.Listener) error         // blocks; nil after Shutdown; http.ErrServerClosed if called after Shutdown
func (g *Gateway) ServeDev(addr string) error         // 127.0.0.0/8, ::1, or "localhost" only; refuses before listening; CORS + preflight; blocks
func (g *Gateway) Shutdown(ctx context.Context) error // drains until ctx expires, then force-closes what is left (one call = "drain up to 10 s")
// usage.Snapshot: Counters(keyID), AllCounters(), Queue() (inFlight, waiting)
// Code constants: gateway.CodeInvalidKey … CodeNotFound (the error table)
```

### Verification (printed, on base 818ef9c)

- `go build ./...` OK · `go vet ./...` OK · `go test ./...`: 9 packages ok (cmd/bunny-network, admin, gateway, invite, keys, tunnel, upstream, usage, web/wasm), 0 failed.
- `go test -race -count=1 -v ./internal/gateway/`: **28 passed / 0 failed / 1 skipped** (the opt-in live test), plus the TestMain gate line above.
- `go build -tags wire ./...` OK; `go vet -tags wire ./cmd/...` OK.
- Live run above: **1 passed** with `BN_LIVE_UPSTREAM` set (sends requests only; the shared llama-server was not restarted or reconfigured).
- `gofmt -l internal/gateway/` empty.

### Edge awareness

Body: declared length over cap → 413 without reading a byte; chunked over cap → 413 mid-read; non-object JSON → 400 · Numbers round-trip as `json.Number` (`seed 12345678901234567890`, `2.50` preserved), unknown fields untouched · `max_tokens` −1/absent/over → clamped; `max_completion_tokens` honoured; no clamp configured → nothing invented · Context: min of key and engine ignoring zeros; both zero → no check; tokenizer failure → `ceil(len/4)` estimate, never a rejection · Auth: scheme case-insensitive, `Bearer` with no token → 401, unexpected status string → 403 `key_paused` (fail closed), store error → 503 + Retry-After 1, `Lookup` on every request (revoke bites on the next call) · Upstream: engine address never in a friend-facing message, friend's `Authorization` never forwarded, engine 4xx/5xx → 502 with status + 512-byte snippet, non-JSON non-stream → 502, hang → RequestTimeout → 502 and the upstream request is cancelled, mid-stream failure → `data: {"error":…}` SSE event with event `Status:200 Code:upstream_error` · Failures before a 2xx charge no tokens and leak no slots (asserted) · `Info().Healthy=false` → 503 `upstream_down`, Retry-After 10, no RPM consumed · Serve after Shutdown → `ErrServerClosed`; dev CORS never leaks into the tunnel handler; ServeDev refuses `0.0.0.0`, `:port`, LAN IPs, hostnames; accepts `127.0.0.1`, `localhost`, `[::1]`.

### Judgment calls

1. **RPM = sliding 60 s log, shared with TPM.** Not a token bucket or fixed window: exact "≤ N in any 60 s", exact Retry-After (when the oldest admission leaves the window), no ticker, and `/me`'s `rpm_used` is the very number the limiter uses — the friend's usage bar and the limiter never disagree. Rejected requests are not logged. One mechanism for two limits (concepts: 5 of 6).
2. **TPM/daily admission includes the pre-check prompt count** (`used + prompt > limit` → 429, numbers in the message), so the limit is a ceiling for prompts; completions can overshoot by at most `max_output_tokens`.
3. **Declared deviation from promise 6's fallback:** a stream that ends without a usage chunk (friend hit stop, or the engine omitted it) is charged completion = number of delta chunks seen, not 0. With 0, the stop button makes every aborted stream free — against "Host stays in control". One token per chunk holds for llama.cpp and vLLM. To strike: `proxy.go` `pipeStream`, the `if !sawUsage` line.
4. `stream_options.include_usage` is forced `true` (not only injected when absent): accounting needs it; the only friend-visible effect is the final usage chunk 004 already handles.
5. **Two error-table rows the contract does not list:** `invalid_request` (400, malformed JSON) and `not_found` (404). The contract's code list has no 400/404 entry and says "anything else → 404 in error format". `client_closed` (499) exists only in usage events, never on the wire. PM to bless or rename.
6. Key-store error → 503 `upstream_down` + Retry-After 1 with the message "the host's key store is unavailable": closest existing row; a new code would be more precise. PM's call.
7. Engine 4xx → 502 `upstream_error` (every friend-facing body must be our shape; llama.cpp and vLLM error shapes differ); the engine's own message rides inside ours.
8. `/me` and `/v1/models` are authenticated but not rate-limited (004 polls `/me` after every request; both are cheap). They do record usage events, as `usage.Event.Endpoint`'s comment lists them.
9. Retry-After: `rate_limited` exact window math (min 1); `concurrency_limited` 1; `queue_timeout` 5; `upstream_down` 10 (003 polls every 10 s); `budget_exhausted` seconds to UTC midnight.
10. `RequestTimeout` covers the upstream call including the whole stream; a stream longer than it is cut with an SSE error event. Queue wait is separate (`QueueTimeout`).
11. Per-key admission (concurrency, RPM, TPM, daily) happens before taking a global queue position, so one friend's burst never occupies the queue for others. `Slots ≤ 0` → 1, never unbounded.
12. `/v1/models` proxies the engine's live list (filtered by the allowlist) rather than `Info().Models`, so the picker never lags the engine.

### Not verified

- Through 001's real tunnel `Listener()`: only `net.Listen` + `httptest` here (005 integration).
- vLLM: not exercised live (llama.cpp only). SSE and usage conventions are the same per 003's probe.
- 004's one-connection-per-request `Connection: close` client: standard net/http behaviour, not specifically tested.
- Memory of the per-key log under sustained load: bounded by design (pruned on every touch to 60 s of traffic), not load-tested.

### Known v1 limitations (documented in code)

- Counters live in memory: a restart resets RPM/TPM/daily (contract gives counters no persistence).
- The context check is reject-only, as promise 4 states; shrinking `max_tokens` to fit would be friendlier (see re-price).
- Chunk-count fallback (call 3) overestimates for engines that emit multi-token chunks.

### Candidates for the PM (not done; one line each)

- **Re-price:** context handling — shrink `max_tokens` to `effective − prompt` when the prompt alone fits, reject only when it does not. Friendlier for long conversations; small change in `checkContext`.
- **Adjacent, 003/005:** `serve.go` passes `Slots: up.Info().Slots` at start; if the engine is down at startup that is 0 → gateway uses 1 and never widens after `Refresh`. Either 003 delays `New` until a healthy `Refresh`, or 002 grows a `SetSlots` (not added: unbudgeted concept).
- Message grammar: "1 requests per minute". Trivial.
- Ticket Background said ctx 8192 for the local engine; it is 4096 per slot (003 already corrected `docs/MEASURE.md`).

### Freeze

- **Base:** `818ef9c` (`origin/main` at freeze; contains 001 and 003). **Lane:** `t002-gateway`. Code commit: `5470ded`; this report is a docs-only commit on top.
- **Patch SHA-256** (`git diff 818ef9c..HEAD -- internal/gateway | shasum -a 256`): `58c976236ea58a043c82f374dd483f67960a7a151fae5202ebe2892632503df7`
- **Source (tests excluded):** 1210 raw lines / 1016 non-blank non-comment — `errors.go` 115, `limits.go` 204, `gateway.go` 343, `proxy.go` 548. Budget ≤ 2000 (size 5): 61 %.
- **Tests:** 1556 lines — `fakes_test.go` 466, `gateway_test.go` 904, `live_test.go` 186. 28 tests + TestMain gate + 1 opt-in live.
- **Concepts:** 5 of 6 — sliding window (RPM + TPM), daily budget, global queue, per-key concurrency, error code table. Token bucket not used (folded into the sliding window).
- **Dependencies added:** none. `go.mod`/`go.sum` untouched. Files outside `internal/gateway/**`: only this ticket file.
- **Production-touching actions:** none. The shared llama-server received test requests only.

## Ruling (PM, 2026-09-02 04:20)

**Landed** on main (ff of `3141e76`), wiring flipped in the same landing: `wire_stub.go` deleted, `wire`
tag dropped, `go mod tidy`; build/vet/test green and three cross-compiles OK, printed.
- **Blessed:** `invalid_request` (400) and `not_found` (404) join the contract's error table.
- **Blessed deviation:** an aborted stream with no usage chunk is charged by the deltas seen. The stop
  button is not free; under-charging by the missing final chunk is acceptable.
- **To 005:** shrink-to-fit `max_tokens` when the prompt fits but prompt + max_tokens exceeds the
  effective context (reject only when the prompt alone does not fit) — small, friendlier, and what
  llama.cpp does itself. And `Slots` fixed at `New` is wrong when the engine is down at startup: 003's
  serve must re-apply slots after the first successful `Refresh` (gateway gains `SetSlots`).
- Adversarial review dispatched post-landing (auth/secrets, streaming proxy, limits/queue, contract
  shapes, resource safety).
