---
id: 031
title: Web app — toggle thinking on/off per chat
kind: normal
size: 1
status: dispatched
updated: 2026-09-03
release: demo-1
---

# 031 — Thinking on/off

**Why.** Founder nit (2026-09-03): the web app should let a friend turn the model's thinking off (faster,
shorter, cheaper against their token budget) or on (better answers on hard questions). Gemma 4 and
DeepSeek-class models think by default; a friend asking "what's the capital of France" pays for 150
words of reasoning today.

**Promises.**
1. A "Thinking" control in Settings (default: model's default), stored per host scope like the other
   settings; sent as the engine's own switch through the gateway, which already passes reasoning fields
   through: llama.cpp `reasoning_budget` (0 = off; -1 = unrestricted) and, for vLLM/OpenAI-style engines,
   `chat_template_kwargs.enable_thinking` / `reasoning_effort` — pick per `/me.host.upstream.kind` and
   verify each against the running engine (llama-server b9553 exposes `--reasoning-budget`; confirm the
   per-request field name in its README rather than assume).
2. Truthful surface: the control appears only once the model has actually produced `reasoning_content`
   in this chat (or `/me` can say the model thinks); until then it reads "Thinking: model default". When
   off, the Thinking block does not appear and the footer shows the tokens saved vs the previous turn if
   both are known.
3. The gateway strips nothing new; if a field is on the engine-override denylist (006), contest.
4. Evidence: vitest for the request builder per engine kind; a real-host run showing a thinking-off
   reply with no reasoning block and a shorter completion; screenshot of the setting.

**Size 1** (≤150 lines). Concept budget 0 (a setting). **Scope:** `web/src/**` (settings, request
builder, Message.tsx); read-only in `internal/gateway`.

## Log

- **2026-09-03 03:22 ACK.** Base `3eb9033` (public main; the worktree was at `c2723b8`, fast-forwarded). Lane
  `t031-web-footer`, shared with 032. Scope `web/src/**`, gateway read-only.
- **03:24 Engine verification (requests only), before any edit.**
  - **llama-server b9553** (`/props`: `build_info b9553-9e3b928fd`, Gemma 4 E2B, 2 slots). Its README
    lists `--reasoning-budget N` as a **server flag**; the per-request options section names only
    `chat_template_kwargs` ("e.g. `{"enable_thinking": false}`"), `reasoning_format` and `reasoning_control`
    — there is no per-request `reasoning_budget`. Live: `reasoning_budget: 0` in the body is ignored
    (529 reasoning chars, 134 deltas, same as default); `chat_template_kwargs.enable_thinking=false` →
    **0 reasoning chars, 7 deltas, generation 35 ms vs 800 ms**, same answer. The Gemma 4 template reads
    `enable_thinking` (`set enable_thinking = enable_thinking | default(false)` … `<|channel>thought`).
    So the ticket's llama.cpp field is corrected: one switch, `chat_template_kwargs.enable_thinking`.
  - **vLLM 0.25.0** over the read-only tunnel (`entropy-v2-gemma4-12b-w4a16-group128`, no reasoning
    parser on that server): never sends `reasoning_content`; default = `enable_thinking=false` =
    `reasoning_effort=none` (8 completion tokens); `enable_thinking=true` and `reasoning_effort=low` leak
    a literal `thought\n` into content (11 tokens). Thinking-off is a no-op there and thinking-on is
    mildly harmful, which is exactly why the control stays hidden until the model has actually thought.
  - **Ollama / LM Studio**: not running here; unverified. They get the same field (ignored if unknown);
    the Thinking block still shows what really happened.
  - **Gateway**: `chat_template_kwargs` is on the pass-through list (`internal/gateway/proxy.go:49`) and
    not in `overrideKeys`; nothing to contest on promise 3.
  - `/me` carries no "this model thinks" field, so the control's only evidence is `reasoning_content`
    seen in this chat — or a setting already in force (otherwise a friend who turned thinking off could
    never turn it back on in a new chat).

## Report
