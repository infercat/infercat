---
id: 036
title: Context pre-check undercounts the chat-template prompt, so a near-ceiling request is rejected by the engine as a raw 400 instead of the gateway's 422 context_too_long
kind: defect
size: 3
status: draft
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
