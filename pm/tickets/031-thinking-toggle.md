---
id: 031
title: Web app — toggle thinking on/off per chat
kind: normal
size: 1
status: queued
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

## Report
