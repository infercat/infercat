---
id: 006
title: Gateway hardening — admission order, timeouts, alias bypass, metering gaps (from 002 review)
kind: sensitive
size: 3
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 006 — Gateway hardening

## Binding

**Why.** The adversarial review of 002 (5 lenses × 2 refuters, 55 agents) confirmed nine defects in
what a friend holding an invite can do to the host's machine and engine (Protection 4) and to the
host's audit log. The demo launches publicly; strangers will hold invites. Each fix is small.

**Promises (each with a targeted test; file:line anchors are from the review at commit `3141e76`).**
1. **Alias bypass.** `clampMaxTokens` (proxy.go:139-165) looks only at `max_tokens`/`max_completion_tokens`;
   llama.cpp honours `n_predict` and the review proved a 100k `n_predict` sails through. Fix: a denylist of
   generation-override keys removed from every proxied body — at least `n_predict`, `max_new_tokens`,
   `n_keep`, `ignore_eos`, `cache_prompt`? (no: harmless), `n_probs`, `logit_bias`? (keep), `stream_options`
   (gateway-owned), `slot_id`, `id_slot`, `lora`, `n_ctx`? — decide by reading llama.cpp's server README
   for keys that change cost or slot behaviour; document the list in a comment and test that `n_predict`
   is stripped and `max_tokens` is set.
2. **Admission before body.** Today `readBody` → `decodeObject` → `countTokens` (a real engine `/tokenize`
   call) all run before `lim.admit` (proxy.go:196-217 vs :383). Reorder: after auth, run RPM + per-key
   concurrency admission first (cheap, in-memory), then read the body under that per-key slot, then
   tokenize, then TPM/daily/context checks, then the global slot. A burst from one key is then bounded by
   its `max_concurrent` (default 1) in memory and in engine tokenize calls. Test: N=50 concurrent 4 MiB
   bodies from one key → at most `max_concurrent` bodies buffered at once (instrument with a counter).
3. **Write deadline.** `pipeStream` (proxy.go:493-548) writes and flushes with no deadline; a client that
   stops reading pins a global slot, a per-key slot, and a goroutine indefinitely; `RequestTimeout` only
   covers the upstream request context. Fix: `http.ResponseController.SetWriteDeadline(now+60s)` before
   each write/flush (renewed per event), plus an overall per-request deadline that also releases slots.
   Test: fake client reads two events then stalls → slots released within the deadline.
4. **Read deadline.** `gateway.go:130-135` sets ReadHeaderTimeout and IdleTimeout but no body read bound;
   a stalled body pins a goroutine, reachable without a valid key (auth happens after headers, body after
   auth — verify the order and bound the body read with `SetReadDeadline` at e.g. 30 s).
5. **Meter `/v1/models`.** proxy.go:254 fans out to the engine with no RPM, no per-key concurrency, no
   global slot. Apply RPM + per-key concurrency (not the global slot).
6. **No redirects upstream.** proxy.go:352 uses a default `http.Client` that follows redirects and would
   stamp the host's upstream API key onto the redirect target. `CheckRedirect` returns
   `http.ErrUseLastResponse`.
7. **Audit truth.** (a) paused/revoked rejections record `key_id:""` and never touch `last_seen`
   (gateway.go:226-232 returns before the assignment although the key is in hand) — set both. (b)
   unauthenticated requests record an unbounded attacker-controlled `endpoint` string into usage.jsonl
   (gateway.go:222) — do not record usage events for 401s at all (they are noise the host cannot act on),
   or truncate `Endpoint` to 64 bytes; pick one and document.
8. **Upstream 4xx.** proxy.go:417 reports an engine 4xx caused by the friend's own request as 502
   `upstream_error`, telling their retry logic the host is broken. Map upstream 400/422 to 400
   `invalid_request` carrying the upstream message; keep 5xx as 502.
9. **Evidence.** `go test -race ./internal/gateway/` printed; each promise has a named test; the existing
   28 tests still pass; a short note per promise in the Report with the file:line of the fix.

**Size 3** (≤900 source lines; expect ~300). Concept budget 1: the alias denylist. **Sensitive**.

**Scope contract.** `internal/gateway/**` only. Ticket 005 concurrently edits `internal/gateway` for its
fixes 10c (shrink-to-fit `max_tokens`) and 10d (`SetSlots`): do NOT implement or touch those; when 005
lands the PM will tell you to rebase and resolve textual conflicts in `proxy.go`. Contest if a fix
needs a seam change.

**Backlog (declined for demo-1, recorded here):** TPM/daily are checked not reserved (overshoot bounded
by `max_concurrent`, default 1) · aborted non-stream requests charged zero (client always streams) ·
missing `prompt_tokens` in a nonconforming usage object charges zero · SSE single-line size unbounded
(engine-controlled) · store error text printed verbatim (contains no secret).

## Background

- Review journal: `~/.claude/projects/.../subagents/workflows/wf_fe3b561c-b59/journal.jsonl`; the
  refuters reproduced every confirmed item with scratch modules under the job tmp dir.
- Shared llama-server at `127.0.0.1:18080` (requests only). Its `/tokenize` is what promise 2 protects.

## Log

## Report
