---
id: 002
title: Gateway — auth, per-key limits, queue, clamps, streaming proxy, usage
kind: sensitive
size: 5
status: dispatched
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

## Report

## Log

- 2026-09-02 02:30 ACK. Base f171164 (main), lane t002-gateway. Read BELIEFS, ARCHITECTURE, ticket, seams. Seams sufficient; no contest.
- 2026-09-02 02:35 Design: hand-rolled proxy (no httputil) for SSE flush + usage parse + cancel; RPM and TPM share one sliding 60 s log per key (one mechanism, exact Retry-After); stdlib only, go.mod untouched.
- 2026-09-02 02:35 Judgment: contract code table lacks 400/404 rows; adding `invalid_request` (malformed JSON) and `not_found`. Flagged for PM.
- 2026-09-02 02:35 Observed: local llama-server /props reports n_ctx 4096 per slot (ticket says 8192 = -c over -np 2). Not my concern; noted for 003/MEASURE.
