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
10. **Bounded waiting queue** (added by the PM from the second-model review, 2026-09-02). gateway.go ~:187:
   today an unlimited number of admitted requests can wait up to QueueTimeout for a global slot, each
   holding its body and a goroutine. Cap the waiting set at max(2, 2×Slots); overflow gets an immediate
   503 `queue_timeout` with Retry-After (reuse the code, add a distinct message "host is busy; N requests
   already waiting"). Test: Slots=1, five concurrent → one running, two waiting, two rejected immediately.
11. **`/me` discloses prompt logging** (added by the PM, 2026-09-02). `/me` gains `host.log_prompts: bool`
   from `cfg.LogPrompts` (the PM updates the docs/ARCHITECTURE.md `/me` line). The web client (007) shows
   a disclosure when it is true. Add the field and a test.

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

## Ruling (design) — PM, founder ruling "fix classes, not instances" (2026-09-02, mid-slice)

Do NOT satisfy promises 1–11 as eleven local patches. Restructure request handling in internal/gateway as
ONE explicit pipeline with a request record that owns every resource and has exactly one exit:
- `type request struct` created at authenticate(): key, per-key slot, global slot, deadlines, usage event,
  reserved tokens, upstream response. One deferred `finish()` releases everything and records usage — the
  only path out, on success, error, panic, abort, and timeout.
- Stage order fixed in one function, top to bottom: authenticate → admitKey (RPM + per-key concurrency) →
  readBody (read deadline) → normalize (ONE function: strip engine-override aliases via a documented
  denylist, fill model, clamp max_tokens including 005's shrink-to-fit, inject stream_options) → count →
  checkBudgets (TPM/daily/context — RESERVE the estimate here, settle the actual in finish) → acquireSlot
  (bounded wait) → upstream call (no redirects) → relay (write deadline renewed per event) → finish.
- Rewrite proxy.go if the current shape fights this; delete code the pipeline makes redundant. Size budget
  rises to 5 (≤2000 source lines net) if needed — say so in the report with the accounting.
- The nine-plus-two promises remain the acceptance criteria and each still gets its named test; they should
  fall out of the structure. Keep 005's landed shrink-to-fit and SetSlots semantics intact inside
  normalize/acquireSlot.

## Log

- 2026-09-02 12:05 ACK. Base 68e6469 (main), lane t006-gateway-hardening. Read BELIEFS, ARCHITECTURE, 006, 002 (report + review), gateway sources and tests. Baseline `go test -race ./internal/gateway/` ok. origin/main unchanged; local t005-integration has no gateway diff yet. No contest so far: all nine promises fit inside internal/gateway/** without seam changes.
- 2026-09-02 13:05 005 landed (aa68524). Rebased mid-slice on the PM's word: two textual conflicts (Gateway struct fields; prepareChat's context call → 005's `fitContext`). Kept SetSlots and shrink-to-fit intact; the promise-10 waiting cap became an atomic that SetSlots updates. `go test -race`: 40 passed / 0 failed / 1 skipped.
- 2026-09-02 13:10 Design ruling received (pipeline + request record, one exit). Restructuring on top of the green rebased commit rather than in place, so the fallback stays coherent. Judgment: shrink-to-fit needs the token count, and count comes after normalize in the ruled order, so `fitContext` (005's code, unchanged) runs as the context step of checkBudgets; normalize does strip / model / key clamp / stream_options. Reservation = the prompt estimate (prompt + max_tokens would reject a 10-token TPM key's first request and contradict 002's "TPM is a ceiling for prompts"); settled to the engine's actual in finish.
- 2026-09-02 12:40 PM added promises 10 (bounded waiting set, max(2, 2×Slots)) and 11 (`/me` host.log_prompts) mid-slice; budget unchanged. Both inside internal/gateway/**, no seam change: no contest. 10 is a counter check in `acquire` (Add-then-compare, exact under a race); 11 is one field in meResponse plus TestMeShape's key list.
- 2026-09-02 12:20 Design. (1) Denylist from the live llama.cpp README (fetched; POST /completion options + OAI-compat extras) grouped as length aliases / multipliers (`n`, `n_cmpl`, `best_of`, `use_beam_search` — vLLM runs n sequences for one slot) / `n_probs` / slot-cache-context / `lora`, plus non-numeric `max_tokens`/`max_completion_tokens` (could shadow the clamp); `stream_options` left alone (include_usage already forced) and `cache_prompt` left per ticket. (2) Limiter split: `admit` (concurrency+RPM, takes the slot) → body → tokenize → `checkTokens` (TPM/daily) → global slot; new `abort` un-counts a pre-queue rejection so "rejections do not count" and the 002 tests survive the reorder. (3) Read deadline armed at handler entry for any request with a body — that also bounds net/http's post-handler discard for 401s — and cleared after a full read, because net/http's background read turns an expired read deadline into a context cancel mid-stream. (4) Write deadline via ResponseController, re-armed per line; overall bound stays RequestTimeout (absolute ctx) + one write deadline. Test-only knobs are unexported Gateway fields, not Config (concept budget).
- 2026-09-02 13:40 Restructured: `request.go` (record + stage order + one exit), `proxy.go` rewritten to the engine-facing half, `gateway.go` trimmed, limiter gains reserve/settle. Every promise's test unchanged and passing on the new shape; added `TestPanicLeavesThroughFinish`. Live check through the pipeline printed. origin/main moved to 87e4dc5 (PM records only); rebased clean.
- 2026-09-02 13:50 FROZEN on base 87e4dc5. Patch SHA-256 below.

## Report

**Branch** `t006-gateway-hardening`, rebased on `origin/main` `87e4dc5` (005 and the founder's rulings already on main). Scope `internal/gateway/**` plus this ticket file; `go.mod`/`go.sum` untouched (stdlib only). Not merged. **The restructure took the size-5 lane the design ruling offered** — accounting at the end.

### The core, shown working

1. **One pipeline, one exit.** `request.go:95` is the stage order, top to bottom, exactly as ruled: `checkHealth → admitKey → readBody → normalize → count → checkBudgets → acquireSlot → callUpstream → relay`; `request.go:31` is the record that owns every resource; `request.go:303` is the only way out (deferred by `serveHTTP`) and releases in reverse order — response body, upstream context, global slot, key admission (settling the reservation to the engine's usage), body buffer — then records the event. `TestPanicLeavesThroughFinish`: a tokenize that panics with the per-key slot, a reservation and a 4 MiB-class body held leaves `InFlight 0, RPMUsed 0, TPMUsed 0, bodies 0`, records the event with the key id, and the next request from that key is a 200.
2. **The alias bypass is closed, proven on the real engine** (requests only, shared llama-server `127.0.0.1:18080`, Gemma 4 E2B):
   ```
   direct to the engine, max_tokens 8 + n_predict 40:   usage {'completion_tokens': 40, 'prompt_tokens': 25} finish_reason: length
   through the gateway (BN_LIVE_UPSTREAM live test):    max_tokens 8 + n_predict 40 + ignore_eos → 200, completion_tokens 8
   ```
   `TestOverrideKeysStripped`: 16 override keys sent at once, none reaches the upstream, `max_tokens` = the key's cap, 13 sampling/shape keys pass through byte-exact, the host's log names what was removed, embeddings go through the same strip.
3. **Admission before body.** `TestAdmissionBeforeBody`: 50 concurrent 4 MiB bodies from one key with `max_concurrent 2` → 48 immediate `429 concurrency_limited` recorded server-side while exactly **2 bodies are buffered** (`Gateway.bodies`), 2 tokenize calls in flight at most, 2 engine requests total, and afterwards `InFlight 0, RPMUsed 2` (the 48 rejections un-counted).
4. **Deadlines free slots.** `TestStalledReaderFreesSlots`: a client reads two 256 KiB events of a 16 MiB stream and stops; within the 300 ms write deadline the global slot and the per-key slot are back to 0, the upstream sees cancellation, the event is `200 client_closed`, and the next request goes straight through. `TestBodyReadDeadline`: a valid key trickling a body gets `400 invalid_request "request body was not received within 200ms"` and leaks nothing; an unauthenticated stalled body gets its 401 and the connection is **closed** within the deadline (net/http's post-handler discard is bounded too); a 500 ms stream under a 200 ms read deadline is **not** cut (the deadline is cleared once the body is in hand — see judgment 3).
5. **Every promise, one named test** (file:line of the fix): 1 alias → `proxy.go:53`/`proxy.go:71` applied in `request.go:202` · 2 order → `request.go:95`, `request.go:158`, `limits.go:118`, `limits.go:200` · 3 write deadline → `request.go:369`, per line in `proxy.go:490` · 4 read deadline → `gateway.go:256`, cleared/stalled in `request.go:169` · 5 models metered → `proxy.go:256` · 6 no redirects → `proxy.go:363` · 7 audit truth → `request.go:121` (key on the record before the status verdict; `noEvent` for 401) and `request.go:303` (Endpoint cap 64) · 8 upstream 4xx → `proxy.go:416` · 10 bounded waiting set → `gateway.go:222`, `gateway.go:116` (follows `SetSlots`) · 11 `/me host.log_prompts` → `proxy.go:317` · reservation → `limits.go:150`.
   Tests: `TestOverrideKeysStripped`, `TestAdmissionBeforeBody` + `TestPreQueueRejectionIsUncounted`, `TestStalledReaderFreesSlots`, `TestBodyReadDeadline`, `TestModelsMetered`, `TestUpstreamRedirectNotFollowed`, `TestAuditTruth`, `TestUpstream4xxIsTheFriends`, `TestWaitingQueueBounded`, `TestMeLogPromptsDisclosure`, `TestPanicLeavesThroughFinish` (all in `hardening_test.go`).

### Verification (printed, on base 87e4dc5)

- `go test -race -count=1 -v ./internal/gateway/`: **41 passed / 0 failed / 1 skipped** (the opt-in live test); TestMain gate: `all 14 error codes exercised; 13 log lines captured, secret absent`. Stable under `-race -count=3` (whole package).
- `BN_LIVE_UPSTREAM=http://127.0.0.1:18080 go test -race -run TestLiveLlamaCPP -v`: **1 passed** — stream 200, TTFT 80 ms, 170 tok/s, `n_predict` case above, 422 and 429 with real counts. Requests only; the shared engine was not restarted or reconfigured.
- `go build ./...` OK · `go vet ./...` OK · `go test ./...`: 9 packages ok, 0 failed. `gofmt -l internal/gateway/` empty.
- The 28 tests of 002 and the 2 of 005 pass unchanged in substance; four were edited to the new contract and are declared below.

### Edge awareness

Denylist: non-numeric `max_tokens`/`max_completion_tokens` removed so they cannot shadow the clamp; `stream_options` kept (include_usage is forced); embeddings stripped too · Admission: pre-queue rejections (bad body, model, context, TPM/daily) hand the slot back un-counted, queue timeout and everything after counts; reservations are the prompt estimate and show in `/me`'s `tpm_used`/`today_tokens` while in flight · Read deadline: armed only when a body is coming (`ContentLength != 0`, chunked included), so GETs and long streams are untouched; a 413 leaves it armed for net/http's discard · Write deadline: also arms error responses and the non-stream 64 MiB write; a flush error counts like a write error · Redirect: 302 → 502 with "HTTP 302", zero requests to the target, no tokens charged · 4xx map: only 400/422; 401/404/5xx/3xx stay 502; the engine's `error.message` (llama.cpp) or `message` (vLLM) is lifted out of the JSON, else the 512-byte snippet · Audit: paused/revoked events carry `key_id` and move `last_seen`; a keyed 5000-byte 404 path is recorded as 64 bytes · Waiting set: the refusal message carries the count; `SetSlots` re-derives the cap · Panic: everything released, event recorded.

### Judgment calls (declared)

1. **Denylist scope.** Beyond the ticket's list I strip the output multipliers `n`, `n_cmpl`, `best_of`, `use_beam_search` (vLLM runs *n* sequences behind one slot and one clamp), vLLM's `min_tokens`/`priority`, and Ollama's `num_*` names; stripped silently, not rejected (a friend asking `n: 3` gets one choice). Left alone on purpose: `cache_prompt` (ticket), `stream_options`, `logprobs`/`top_logprobs` (standard; a response-size lever only — see re-price), `t_max_predict_ms` (only shortens).
2. **Shrink-to-fit lives in `checkBudgets`, not `normalize`.** It needs the token count, and the ruled order puts count after normalize. `fitContext` is 005's code unchanged; `normalize` does strip / model / key clamp / stream_options. One behaviour change: embeddings now get the "prompt must fit the context" rule too (422 instead of whatever the engine said).
3. **Read deadline is cleared after the body is read.** net/http starts a background read at body EOF; an expired read deadline there is reported as a client disconnect and cancels the request context — a 30 s deadline would have cut every stream longer than 30 s. The long-stream case in `TestBodyReadDeadline` is the regression guard. A stalled body is `400 invalid_request` (no 408 concept) and the response is still written although net/http has cancelled the context (`stalled` flag).
4. **A stopped reader is recorded as `client_closed`** (200 + code in the event), not a new code.
5. **7b: both halves.** 401s record nothing (keyless attacker cannot write to usage.jsonl) *and* `Endpoint` is capped at 64 bytes for every event (a keyed friend's 404s cannot inflate lines). The ticket said pick one; they bound different attackers, so I did both — one line each.
6. **`/v1/models` consumes RPM** and a failed list call still counts (`TestRPM` now asserts 429 on the list; `/me` stays unmetered).
7. **Reservation = the prompt estimate**, settled to the engine's usage in `finish`. Reserving prompt + max_tokens would reject a 10-token-TPM key's first request and contradict 002's "TPM is a ceiling for prompts". The counters include live reservations so `/me` and the limiter still agree.
8. **Waiting-set overflow** reuses `queue_timeout` with Retry-After 5 and the message "the host's engine is busy; N requests already waiting".
9. **Test knobs are unexported fields** (`readTimeout`, `writeTimeout`), not Config: no new concepts. `Gateway.bodies` is three lines of production instrumentation that exist for the promise-2 proof.
10. **Existing tests edited:** `TestRPM` (models now 429), `TestAuthCodes` (no 401 events, paused/revoked carry the key), `TestMeShape` (`log_prompts` key), `TestLimiterWindowsWithFakeClock` (limiter API: `admitAll` helper, `release(id, reserved, tokens)`).

### Not verified

- vLLM live (llama.cpp only); the 4xx shapes for vLLM are from its documented error object.
- The write-deadline test overruns loopback socket buffers with 16 MiB; passed ×3 on macOS, buffer sizes on Linux differ but are far below 16 MiB.
- Through 001's real tunnel listener: httptest only. The redirect test uses a same-host `Location`; cross-host is the same client setting.

### Candidates for the PM (not done)

- `top_logprobs` is unbounded on llama.cpp (maps to `n_probs`): response size per token scales with it; a clamp to OpenAI's 20 would be one line if wanted.
- Rejection events from a keyed friend (404/400, not rate limited) can still grow usage.jsonl at request rate; a per-key cap on recorded rejections is a small follow-up.
- An engine 429 (vLLM under pressure) is reported as 502; 503 `upstream_down` with Retry-After would read truer.
- A 408 code for stalled bodies, if the contract wants the truth on the wire.

### Freeze

- **Base:** `87e4dc5` (`origin/main` at freeze). **Lane:** `t006-gateway-hardening`. Code commits `cbe51f4` (promises as fixes, rebased over 005) and `dac9d87` (the pipeline restructure); this report is a docs-only commit on top.
- **Patch SHA-256** (`git diff 87e4dc5..HEAD -- internal/gateway | shasum -a 256`): `70efc2db8e66cdc0d270007299bee1c1f652f987db0759ad5e3913fd22dfbbe9`
- **Source diff** (tests excluded): +643 / −337 lines — `gateway.go` +36/−101, `limits.go` +58/−18, `proxy.go` +199/−218, `request.go` +350 (new), `errors.go` untouched. **Net source now 1591 raw / 1266 non-blank-non-comment vs 1259 / 1055 on main: +211.** The ticket's size-3 ceiling (≤900 diff lines) is exceeded by the ruled rewrite (980 changed lines); under the ruling's size-5 lane (≤2000 net) it is 11 %.
- **Tests:** +639 / −31 — `hardening_test.go` 572 (new), `fakes_test.go` +55, `gateway_test.go` +69/−31, `live_test.go` +10. 41 tests + TestMain gate + 1 opt-in live.
- **Concepts:** 1 of 1 — the override-key denylist. `request`/`endpoint` are internal structure; `host.log_prompts` is the PM's contract addition (promise 11). No new error codes, flags, config keys, or files.
- **Dependencies added:** none. Files outside `internal/gateway/**`: only this ticket file.
- **Production-touching actions:** none. The shared llama-server received test requests only; nothing was restarted, no secret touched.
