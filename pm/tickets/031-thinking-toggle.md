---
id: 031
title: Web app — toggle thinking on/off per chat
kind: normal
size: 1
status: landed
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
- **03:28 Built.** `thinkingFields()` and `tokensSaved()` in `stream.ts`, the setting on `Settings`,
  `thinking` on the reply, the sheet row and the footer marks. 5 vitests. Committed as `b6f9717` (rebased
  at the freeze to `4fe22ba`), 79 source lines.
- **03:33 First real-relay run** (`dev/footer-check.mjs`, host on 6840, preview 6841): the two 031
  scenarios held; the run died at the 032 queued scenario on a harness bug (the `--json` key output
  carries the invite, not the secret; fixed as busy-check does).
- **03:36 Second run: every promise held**, no console or page errors, exit 0. Evidence below.
- **03:41 The PM's session committed this lane's working tree mid-flight** (`1670a28`: the Reports as
  written so far, `footer-check.mjs`, the re-shot screenshots; source 0). **03:44** rebased onto
  `ebc1146` (033's ticket file only) → `4fe22ba` / `14218c3` / `6572d10`; the Verified and Freeze
  sections below land in the commit after. Fake-driven suite re-shot on 6842–6843: 101 screenshots,
  no console or page errors, no horizontal overflow at 360 px or 390 px with the longer footer.

## Report

### The core, shown working

Production bundle over the real New York relay against a real `bunny-network serve --slots 1` on the
shared llama-server b9553 (Gemma 4 E2B), started and stopped by `web/dev/footer-check.mjs` on ports
6840–6841 with keys it minted; printed:

```
SETTINGS before any reply   "Thinking · model default" · switch present: 0            31-settings-default.png
REPLY 1 (model default)     Thinking block: 1 · "29 tokens in · 137 out"
SETTINGS after that reply   switch present: 1 · Model default | On — better answers on hard questions |
                            Off — faster, shorter, fewer of your tokens                31-settings-thinking.png
REPLY 2 (thinking off)      Thinking blocks: 0 · "52 tokens in · 8 out · thinking off ·
                            129 fewer tokens than the previous reply"
                            host usage.jsonl completion_tokens 137 → 8                31-real-thinking-off.png
```

**What is sent, and why it is not what the ticket named.** The switch is one field for every engine
kind: `chat_template_kwargs: {"enable_thinking": false|true}`, absent for the model default. Verified
before coding (Log 03:24): on llama-server b9553 it turns Gemma 4's thinking off (0 reasoning chars, 7
deltas, 35 ms of generation instead of 800); the ticket's `reasoning_budget` is that server's
`--reasoning-budget` **flag**, not a request option — sent per request it changed nothing (529 reasoning
chars either way), so the request builder does not send it and the vitest says so. On the founder's vLLM
0.25 the field is accepted; that host's model never emits `reasoning_content` and `enable_thinking=true`
leaks a literal `thought\n` into content — so on that host the control never appears (nothing has been
seen) and nothing is sent, which is the truthful outcome.

**The control's evidence.** `/me` has no "this model thinks" field, so the switch appears once this
chat holds a reply with `reasoning_content`, or when a choice is already in force (a friend who turned
thinking off must be able to turn it back on in a new chat that, being off, never shows reasoning).
Until then the row reads "Thinking · model default" and says when the switch will appear.

### Edge awareness, one line

Handled: a settings object stored by a build that had no `thinking` (merged over the defaults); the
switch in a new chat after Off (a choice in force keeps it visible); an engine that thinks despite Off
(the block shows what happened, the footer says "thought despite thinking off", no saving is claimed);
a saving said only when both counts are known, the previous reply thought and the count went down; a
host whose model never thinks (no control, nothing sent).

### Judgment calls

- **One field, not a per-kind table.** Both engines I could reach take the same switch; a table with
  one row is a claim about engines I did not run. Ollama and LM Studio get the same field, which they
  ignore if unknown, and the Thinking block still shows what they did.
- **The reply records what it was asked** (`Message.thinking`), so the footer's "thinking off" survives
  a reload and a saving is compared like with like: completion tokens against the previous assistant
  reply, which must have thought.
- **The switch is per host scope**, beside model, system prompt and temperature, as the ticket asks —
  the evidence for showing it is per chat.

### Not verified

Ollama and LM Studio (not running here); a model whose template does not read `enable_thinking`
(Jinja ignores an unknown variable, so the model's default happens — the surface stays truthful, the
setting does nothing); a thinking *budget* (b9553 has none per request).

### Candidates (not fixed here)

- `/me.host.upstream` could say whether the loaded model's template reads `enable_thinking` — llama-server's
  `/props` chat template contains the variable — so the control could appear before the first reply.
  Gateway work, one field.

### Verified (printed, at the freeze commit on base `ebc1146`)

```
$ pnpm install --frozen-lockfile  → Done in 1.3s using pnpm v11.13.0                          exit 0
$ pnpm typecheck                  → tsc --noEmit, no output                                    exit 0
$ pnpm test                       → Test Files 10 passed (10) | Tests 252 passed (252)         exit 0
                                    0 failed, 0 skipped   (base 239; +5 for 031, +8 for 032)
$ pnpm lint                       → eslint ., no output                                        exit 0
$ pnpm build                      → index 231.40 kB (gzip 74.51); Chat 362.96 kB (gzip 111.24);
                                    css 13.76 kB                                               exit 0
$ node dev/footer-check.mjs       → every promise above held, and no console or page errors    exit 0
$ node dev/screenshots.mjs        → 101 screenshots (PROD=0); no console or page errors;
  (WEB_PORT=6842)                   no horizontal overflow at 360 px or 390 px                  exit 0
```

`package.json` and `pnpm-lock.yaml` unchanged: **no new dependencies.** Processes stopped: only the
host, preview, fake gateway, vite and browsers the harnesses started (ports 6840–6843). The shared
llama-server and the vLLM behind the read-only tunnel only received requests; the founder's demo host
(9091) and its data dir were not touched.

## Freeze

- **Base:** `3eb9033` at dispatch, rebased at the freeze onto `ebc1146` (public main; 033's ticket file
  only). **Lane:** `t031-web-footer`, shared with 032; pushed, not merged. **Commit:** `4fe22ba`.
- **Patch SHA-256:** of `git diff origin/main...HEAD --binary` at the lane's freeze commit, reported
  with the freeze message (recording it here would change it).

| Bucket | Budget | Measured (raw added / deleted, commit `4fe22ba`) | Verdict |
|---|---|---|---|
| Source TS/TSX (`web/src/**`, tests excluded) | ≤150 lines of change | +72 / −7 = **79** | inside |
| Web tests (`web/src/**/*.test.ts`) | not budgeted | +40 / −1 | 5 tests (239 → 244) |
| Stylesheet | not source | 0 | — |
| Screenshots | ≤500 KB each | 3 new `31-*` (largest 96 KB) | inside |
| Dependencies | none | **0 added**, lockfile unchanged | — |
| Gateway | read-only | 0 | — |

**Concepts: 0 budgeted, 0 used.** `Settings.thinking` is the setting the ticket names ("a setting");
`Message.thinking` is a property of the reply; `thinkingFields` and `tokensSaved` are derivations;
`Thinking` is a type alias. Rule against me on any of these.

## Ruling (PM, 2026-09-03 04:20)

**Landed** (in merge `f2a38ac`). Ticket premise corrected by the engineer and accepted: `reasoning_budget` is a server flag, not a request field; `chat_template_kwargs.enable_thinking` is the switch and works on Gemma 4 (137 → 8 tokens). vLLM host model never thinks; the control correctly never appears there.
