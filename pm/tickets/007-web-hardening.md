---
id: 007
title: Web client hardening — truthful stream ends, session leaks, degraded states, host-scoped storage (from second-model review)
kind: normal
size: 5
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 007 — Web client hardening

## Binding

**Why.** A second-model review (gpt-5.6-sol, four lenses over main `5197966`) found the web client
lies in exactly the moments the launch audience will test: a stream that dies mid-answer looks
finished, a dead host keeps a healthy status pill, and a revoked key is swallowed. "Surfaces tell the
truth" is a taste rule in BELIEFS.md. Everything here is confirmed by reading the code; items marked
(×2) or (×3) were found independently by that many lenses.

**Promises (each with a vitest where the logic is testable; screenshots for visible states).**
1. **Truthful stream ends (×3).** `api.ts` stream parser: an SSE payload with an `error` member throws
   `GatewayError` with its code; EOF without `[DONE]` (and without a usage-only final chunk) rejects as
   a connection failure. The assistant message keeps its partial text and gets a terminal `status`:
   `complete | stopped | interrupted | no_answer`; the UI renders `interrupted` and `no_answer`
   visibly (short line under the message, not a banner) and excludes interrupted turns from the next
   request's context unless the user continues them.
2. **Reasoning-only end state.** When a reply ends with only `reasoning_content` (Gemma with a small
   cap does this) or Stop is pressed during thinking, the Thinking block collapses with a one-line
   explanation and the message gets `no_answer`/`stopped`; never an empty assistant row.
3. **No leaked sessions (×3).** `Connect.tsx`: the provisional transport is closed on `/me` failure,
   on unmount, and when a newer connect attempt supersedes it (attempt id guard). Test: `/me` rejects →
   `close()` called exactly once.
4. **Degraded states (×3).** Ping failure replaces the path pill with `path unknown · last 32 ms 2 min
   ago` (or similar) rather than the stale value; `/me` with `upstream.healthy=false` shows the engine
   offline state in the header; a periodic `/me` (every 60 s) while models are empty (LM Studio with
   nothing loaded) so a model loaded later appears without reconnecting.
5. **Revoked mid-session (×2).** `refreshMe` errors with `invalid_key|key_paused|key_revoked` return
   the user to Connect with the mapped reason; transient refresh errors keep the last snapshot.
6. **Abort during dial (×2).** `transport/tunnel/index.ts`: an `AbortSignal` already aborted or aborted
   while `dial()` is pending rejects immediately with `AbortError`; a connection that resolves after
   abort is closed.
7. **Host-scoped storage (×3).** Conversations and settings are namespaced by a non-secret identity
   `tunnelAddr + key.id`; connecting to a different host starts with that host's own (empty) list and
   its own settings; a persisted model not in the new host's list is reset.
8. **Retry countdown** uses an absolute deadline and `Date.now()`, re-derived on `visibilitychange`;
   values over 10 minutes render as a duration; missing `Retry-After` defaults to 5 s.
9. **IME safety.** Enter during composition (`isComposing` or keyCode 229) never sends, in both the
   composer and the invite field.
10. **Two tabs.** Use the Web Locks API: the tab holding the lock uses the persisted tunnel identity;
    other tabs connect with an ephemeral identity without overwriting the stored key. (Cheap variant;
    full cross-tab merge is backlog.)
11. **Small truths.** Per-code copy is shown (not replaced by the gateway's diagnostic; show both);
    add `invalid_request` and `not_found` entries; SSE framing handles CRLF split across reads;
    `Content-Encoding` compared case-insensitively in `wasm.ts`; wasm asset fetches have a 60 s timeout
    and a working Retry; the remembered-invite failure copy mentions a rotated invite and offers
    "Forget this invite".
12. **Privacy copy is conditional.** `/me.host.log_prompts` (added by ticket 006 promise 11) drives the
    Connect screen line: when true, an unmistakable disclosure replaces the "never what you wrote"
    sentence, before the first message.
13. **Evidence.** `pnpm typecheck && pnpm test && pnpm build` printed; screenshots of the interrupted,
    no-answer, degraded-path, engine-offline, and log-prompts-disclosure states via the existing
    Playwright runner + fake gateway (extend `web/dev/fake-gateway.ts` with an `error-mid-stream` and
    `eof-no-done` mode and a `log_prompts` flag).

**Size 5** (≤2000 TS/TSX source lines of change; expect ~600). Concept budget 2: message terminal
status, host-scoped storage namespace. **Normal**; experience review follows.

**Scope contract.** `web/**` except `web/wasm/**`. Ticket 005 concurrently makes one web change (10e:
do not send `max_tokens` unless set) — expect a trivial rebase conflict in the request builder.

**Backlog (declined for demo-1):** storage quota handling and cross-tab merge · 103 informational
responses · strict hex chunk-size and Content-Length grammar · refresh-during-generation checkpointing
(persist the user turn before I/O is in scope if it falls out of promise 1 cheaply; otherwise backlog).

## Background

- Review output: `/Users/yuanpingsong/.claude/jobs/12b4a99c/tmp/review-integrated.out` (JSON; four
  lenses, file:line anchors, suggested fixes). Reviewer notes are hypotheses; verify against the code.
- `web/dev/fake-bunny-tunnel.ts`, `fake-gateway.ts`, and the Playwright runner exist from 004.

## Log

## Report
