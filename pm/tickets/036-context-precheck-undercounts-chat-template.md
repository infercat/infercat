---
id: 036
title: Context pre-check undercounts the chat-template prompt, so a near-ceiling request is rejected by the engine as a raw 400 instead of the gateway's 422 context_too_long
kind: defect
size: 3
status: dispatched
updated: 2026-09-03
release: demo-1
found_by: 028 (load test, vLLM)
severity: high
component: internal/gateway (fitContext / count), internal/upstream (CountTokens)
---

# 034 — the context pre-check is looser than the engine's, near the ceiling

**Found.** Ticket 028, N=2 against workstation vLLM (`entropy-v2-gemma4-12b-w4a16-group128`,
`max_model_len 8192`, 2 slots). Of 111 chats, **51 came back `400 invalid_request`** carrying vLLM's
own message, alongside 11 clean `422 context_too_long`. Every 400 had a gateway-counted prompt of
exactly **8152 tokens — under the 8192 the gateway advertises** — so `fitContext` admitted it, shrank
`max_tokens` to fit 8192, and sent it; vLLM then rejected it. (The friend's client retried the same
turn on a 400, so one stuck friend produced all 51.)

**Root cause (measured).** The gateway counts the prompt with `CountTokens`, which posts the raw
concatenated message text to the engine's `/tokenize` (`internal/upstream/kinds.go`). vLLM's chat
endpoint counts the **chat-templated** prompt, which is larger — measured here:

```
POST /tokenize {"prompt": <text>}                         → count 7000   (raw, what the gateway uses)
POST /tokenize {"messages":[…], "add_generation_prompt":true} → count 7013   (+13, the template overhead)
POST /v1/chat/completions {"messages":[…], "max_tokens":100} with a raw-8150 prompt →
  HTTP 400 {"message":"This model's maximum context length is 8192 tokens. However, you requested 100
  output tokens and your prompt contains at least 8093 input tokens, for a total of at least 8193
  tokens. Please reduce…", "type":"BadRequestError","code":400}
```

So at prompt≈8152 the gateway computes headroom `8192 − 8152 = 40`, shrinks `max_tokens` to ≤40, and
sends; vLLM sees prompt = 8152 + template overhead and `prompt + max_tokens > 8192`, and rejects. The
gap is the chat-template tokens the pre-check never counted. llama.cpp did not show it only because its
context (32768) sat far above the mix; the same undercount would bite any engine at its own ceiling.

**Two defects, one cause.**
1. **The pre-check undercounts.** `fitContext` (`internal/gateway/proxy.go`) trusts a prompt count that
   is smaller than what the chat endpoint enforces, so shrink-to-fit leaves no real margin and the
   engine rejects a request the gateway meant to make fit.
2. **The rejection the friend sees is untrue to its cause.** `upstreamStatusErr`
   (`internal/gateway/proxy.go`) maps every engine 400/422 to `400 invalid_request` with the engine's
   raw message. A context overflow is exactly the gateway's own `422 context_too_long` case, but the
   friend gets a generic 400 quoting vLLM internals ("This model's maximum context length is 8192
   tokens…") — leaking the engine's shape (near Protection 1's spirit) and, because the web app's copy
   for `invalid_request` differs from `context_too_long`, never telling the friend to shorten. Their
   client retries the same over-long turn.

**Repro.** `bunny-network serve --upstream <vLLM 8192> --dev-listen 127.0.0.1:9090`; mint a key; send a
chat whose raw `/tokenize` count is ~8150 with `max_tokens` 100. Gateway admits (prompt < 8192), vLLM
returns 400; the friend sees `invalid_request`, not `context_too_long`. Full run:
`R5-vllm-ours-n2` (028), 51/111 requests.

**Fix options (PM sizing).**
1. **Count what the engine will count.** For engines with a chat-template tokenize (vLLM's
   `messages`+`add_generation_prompt` form; llama.cpp's `/apply-template` then `/tokenize`), count the
   templated prompt in `CountTokens` for chat requests. Exact; costs one extra shape in the upstream
   adapters.
2. **Keep a margin.** Subtract a template-overhead budget (measured ~1% or a small constant) from the
   effective context in `fitContext` so shrink-to-fit lands under the engine's real ceiling. Cheap,
   approximate; a wrong margin still leaks the raw 400.
3. **Classify the engine's context rejection.** In `upstreamStatusErr`, when the engine's 400/422
   message matches a context-length overflow, return `422 context_too_long` with the gateway's own
   sentence instead of the engine's. Fixes the friend's copy even when (1)/(2) miss; pairs with either.

**Recommendation.** (1) for the pre-check + (3) as the honest fallback: the friend always sees "your
message is too long for this host's model" and the meter/retry logic treats it as such. Add a fixture:
a prompt whose raw count is under the context but whose templated count is over → the gateway answers
`422 context_too_long` before calling the engine, and never a raw engine 400.

## Log
- 2026-09-03 — filed from 028 with the measured token gap and vLLM's own message.
- 2026-09-03 05:19 EDT — ACK. Lane `t035-defects` on main `a69ec2a` (one lane with 035). Verified against both running engines (requests only): **llama.cpp b9553** at 127.0.0.1:18080 — `/tokenize` with `messages` answers `{"tokens":[]}` (not supported at this build), so the exact path is `/apply-template` → `/tokenize {content, add_special:true}`: raw text 12 tokens, templated 25, with `add_special` **26** = the chat endpoint's own `usage.prompt_tokens` **26**. Its overflow answer is `400 {"error":{"code":400,"message":"request (36017 tokens) exceeds the available context size (32768 tokens), try increasing it","type":"exceed_context_size_error","n_prompt_tokens":36017,"n_ctx":32768}}`. **vLLM 0.25.0** over the read-only forward (`ssh -N -L 8010:127.0.0.1:8010 max@max-ws.lab`, pid in `…/bn035-data/ssh-fwd.pid`) — `/tokenize {model, messages}` (add_generation_prompt defaults true): raw 12, templated **29** = chat `usage.prompt_tokens` **29**; overflow answers "This model's maximum context length is 8192 tokens. However, you requested 8 output tokens and your prompt contains at least 8185 input tokens…" (`type BadRequestError`, same for stream). A `max_tokens=9000 cannot be greater than max_model_len…` 400 is a different defect (max_tokens over the whole context; the gateway's clamp prevents it) and stays `invalid_request`. Plan: `Engine.CountTokens` gains the messages argument (one seam method, messages-aware — the authorized change); the estimate adds 4/message + 16; `upstreamStatusErr` maps the two engines' overflow shapes to 422 `context_too_long` with the gateway's own sentence and the engine's in the host log.
- 2026-09-03 05:28 EDT — Fix. Seam: `Engine.CountTokens(ctx, text, messages []byte)` — the one method, messages-aware; `messages` is the chat request's array as JSON (nil for embeddings), marshalled once in `normalize` onto `normalized.messages`. Adapter (`internal/upstream/kinds.go`): llama.cpp `/apply-template` then `/tokenize {content, add_special:true}`; vLLM `/tokenize {model, messages, add_generation_prompt}`; every other kind, and any failed exact call, `EstimateTokens(text) + TemplateAllowance(messages)` (4 a message + 16). Gateway: `count` passes the messages; `upstreamMessage` now also returns the engine's error type; `upstreamStatusErr` became a method and maps `contextOverflow(typ, msg)` — llama.cpp's type `exceed_context_size_error` / "exceeds the available context size", vLLM's "maximum context length is" — to 422 `context_too_long` in the gateway's words, the engine's sentence in the host log; `effContext()` factored out of `fitContext` for the message.
- 2026-09-03 05:29 EDT — Fixtures: `TestContextPrecheckCountsTheChatTemplate` (fake template 4/message + 16; a system prompt and 20 turns; context 300: 200 words fill it with the floor, 201 → 422 "prompt is 301 tokens but the context is 300" with the engine never asked, 170 + 40 shrinks to 30 not 90; the count was given the messages, embeddings were not); `TestUpstream4xxIsTheFriends` extended with both engines' real 400 shapes, stream and not → 422 in the gateway's sentence, TPM charged 0, and vLLM's `max_tokens … max_model_len` 400 still `invalid_request`; `TestCountTokensUnderTheChatTemplate` (llama.cpp and vLLM fakes gained the template surface; exact +9 on both, ollama/lmstudio estimate +24); `TestTokenizeFailureFallsBackToTheEstimate` gained the chat row. Each fails with its fix removed (printed in the report). One adaptation: `TestContextTooLongBoundary`'s estimate path used a 10-token context; a one-message chat's estimate now carries the 20-token allowance, so it is 30.
- 2026-09-03 05:30 EDT — Live, own hosts: C on 6871 (`…/bn035-data/c`, `--upstream http://127.0.0.1:8010 --slots 2`, the read-only forward to the workstation vLLM) and A on 6870 (llama.cpp 18080). Results in the report. Hosts and the forward stopped.
- 2026-09-03 05:33 EDT — Freeze: rebased on `origin/main` (still `a69ec2a`); checks printed in the report.

## Report

**Core 1 — the pre-check counts what the engine will count.** Against the two real engines, the gateway's count of a chat is now the number the chat endpoint enforces (`usage.prompt_tokens`): llama.cpp 26 = 26 and vLLM 29 = 29 for the same two messages (bare text: 12), and live through own hosts:

```
# vLLM (8192), own host C on 6871 — 036's own case, a prompt whose raw count is 8152 (R5's 51 rejected requests):
[036 case, raw 8152]  raw 8152 · templated 8163 · max_tokens 100 → HTTP 200 in 4.2s:
    {"usage": {"prompt_tokens": 8163, "completion_tokens": 29, "total_tokens": 8192}, "finish_reason": "length"}
# the gateway counted 8163, shrank max_tokens to 29, and vLLM served it — the context filled to the token.
[templated 8198]      raw 8187 · templated 8198 · max_tokens 100 → HTTP 422 in 0.0s:
    {"message": "prompt is 8198 tokens but the context is 8192", "code": "context_too_long"}      (host: 8198→0 tok 19ms 422; engine never asked)
# llama.cpp (32768/slot), own host A on 6870:
[templated 32780]     raw 32766 · templated 32780 · max_tokens 64 → HTTP 422 in 0.0s:
    {"message": "prompt is 32780 tokens but the context is 32768", "code": "context_too_long"}    (usage.jsonl: status 422, prompt_tokens 32780)
[two short messages]  raw 7 · templated 22 → HTTP 200: usage.prompt_tokens 22                      (pre-check 22 = engine 22)
```

**Core 2 — an engine 400 that is a context overflow is 422 `context_too_long`, never `invalid_request`.** Live, the boundary the floor of 16 output tokens overshoots by design (010 judgment 9):

```
[templated 8192]      raw 8181 · templated 8192 · max_tokens 100 → HTTP 422 in 0.0s:
    {"message": "the prompt (8192 tokens) leaves no room in the model's context (8192) for a reply; shorten the conversation", "code": "context_too_long"}
host log: gateway: the engine rejected a prompt the pre-check counted at 8192 tokens as over its context: This model's maximum
          context length is 8192 tokens. However, you requested 16 output tokens and your prompt contains at least 8177 input tokens…
```
The friend's client sees the code it already has copy for and does not retry; the engine's sentence stays on the host.

**The fixtures bite** (each run with its fix removed, then restored):
```
mapping off:   --- FAIL: TestUpstream4xxIsTheFriends   want 422 invalid_request_error/context_too_long, got 400 …/invalid_request
messages off:  --- FAIL: TestContextPrecheckCountsTheChatTemplate   200 words + 100 of template must fill the context: 200 … max_tokens=40
template off:  --- FAIL: TestCountTokensUnderTheChatTemplate   chat count = 5, want 14 (text 5)  (×4 engines)
```

**Contract lines that changed** (docs/ARCHITECTURE.md is the PM's; not edited): the Engine seam reads `CountTokens(ctx, text, messages []byte)`; "Exact token counts via /tokenize for llama.cpp and vLLM; others estimate ceil(chars/4)" gains "a chat is counted under the engine's chat template (llama.cpp `/apply-template` + `/tokenize add_special`; vLLM `/tokenize` with `messages`); an estimate adds 4 tokens a message + 16"; and in the chat route, "An engine 400/422 caused by the request maps to 400 invalid_request with the engine's message" gains "except a context overflow, which is 422 context_too_long in the gateway's words".

**Judgment calls (declared).** (1) The floor of 16 at the ceiling stays as 010 ruled: at prompt = context the engine now rejects and the friend gets the mapped 422 (live above); having the pre-check refuse `prompt + 16 > context` instead is a one-line candidate, not taken. (2) vLLM's `max_tokens=N cannot be greater than max_model_len` 400 stays `invalid_request` (fixture): it is about `max_tokens` alone and cannot reach the engine through the clamp while the context is known. (3) The mapped 422's sentence carries the pre-check's count and the context, not the engine's numbers — vLLM's "at least 8177" is its own truncated figure. (4) Settle rows untouched: the mapped 422 is the engine's row (counted, charged 0; asserted), the pre-check's 422 stays Rejected. (5) `messages` is forwarded verbatim (`json.RawMessage`), so the engine templates exactly the array the chat endpoint will see; the text stays the estimate's input and `LogPrompts`' record. (6) A failed `/apply-template`, an empty rendering, or an engine of unknown kind degrades to estimate + allowance — no new error path, no 5xx (the existing rule).

**Edges seen.** Embeddings pass nil messages and count as before; a body without `messages` sets none; an unknown context (0) gives the mapped 422 a sentence without numbers; the engine's 400 on a stream arrives before the head, so it is a plain 422 response; the older vLLM top-level `{"message","type"}` error shape parses too; `TemplateAllowance` of nil or a non-array is 0.

**Verification.** `go build ./... && go vet ./... && go test ./...` on the rebased lane (`a69ec2a`, origin/main unmoved): build 0, vet 0, every package ok — **161 passed / 0 failed / 2 skipped** top-level (the pre-existing opt-in live tests), **109 subtests passed / 0 failed**. `go test -race -count=1 ./internal/gateway/` ok (18.9 s). `gofmt -l` clean on every file touched. Manual: the live runs above (`…/bn035-data/{a,c}/live-036.out`).

**Accounting** (raw added / deleted from `git diff a69ec2a..HEAD`, this ticket's files):

| Bucket | Budget | Measured |
|---|---|---|
| Go source: `internal/upstream/kinds.go` +42/−15, `internal/upstream/upstream.go` +4/−2, `internal/gateway/proxy.go` +53/−19, `internal/gateway/request.go` +3/−2 | ≤250 | **+102 / −38 = 140 raw** (31 of the added lines are comment) |
| Go tests: `internal/upstream/upstream_test.go` +92/−9, `internal/gateway/gateway_test.go` +59/−2, `hardening_test.go` +25, `fakes_test.go` +15/−4, `live_test.go` +1/−1 | — | +192 / −16 |
| Concepts | 0 | **0** — no new error code (`context_too_long` reused), flag, config key, route or dependency; `TemplateAllowance` is a function on the existing seam |

**Declared.** Live actions: own hosts on 6870 and 6871 (data dirs `…/tmp/bn035-data/{a,c}`, keys `friend` minted, invites 0600), both stopped; the read-only ssh forward `-L 8010` to `max-ws.lab` started and stopped; requests only to the shared llama-server and the workstation vLLM — one served 8163-token prefill on the workstation (the 036 reproduction, 4.2 s), everything else refused before prefill. No secrets, no dotenvx. Nothing else started, stopped or reconfigured. Intended, not a bug: `TestContextTooLongBoundary`'s estimate row moved from a 10- to a 30-token context (a chat's estimate now carries the allowance).

**Adjacent, not fixed.** (1) The floor-at-ceiling candidate above. (2) The web client's copy for `invalid_request` ("The host could not read that request") has no shortening hint; with the mapping it no longer needs one for this case. (3) `hack/load`'s friend retries a 400 on the same turn — instrument behaviour, unchanged.

**Freeze.** Base `a69ec2a` (`origin/main` at freeze, unmoved since dispatch) · lane `t035-defects`, code commit `e52f563` · patch SHA-256 (`git diff a69ec2a..HEAD -- internal/upstream internal/gateway | shasum -a 256`): `31c39ab85b77a1fd84a3b3b421a6f559242c8b42256378a42606276b02cd8c76` · source 140 raw of ≤250 · concepts 0 of 0 · contests 0 · bought beyond the ticket: nothing.
