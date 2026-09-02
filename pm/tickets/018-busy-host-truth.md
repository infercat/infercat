---
id: 018
title: Busy host is not an asleep host — early headers and queued keepalives on streaming requests
kind: sensitive
size: 2
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 018 — Busy host truth

## Binding

**Why.** After 014, a friend whose request gets no bytes for 15 s is told the host "didn't answer — it's
probably asleep or offline". A host that is merely busy (all slots taken; the request is waiting in the
FIFO queue for up to 30 s) produces exactly the same silence, so the app now states something false in
the most common launch-day situation: several strangers at once. "Surfaces tell the truth" (BELIEFS).

**Promises.**
1. **Gateway.** For a streaming chat request (`stream: true`) that is admitted and must wait for a slot,
   the gateway writes the response head at once — `200`, `Content-Type: text/event-stream`, the usual
   no-buffering headers — and then an SSE comment line `: queued` immediately and every 5 s while
   waiting (comments are legal SSE and invisible to compliant parsers). When the slot arrives, the
   normal relay follows on the same response. If the queue times out, the request ends with a normal
   SSE error event `data: {"error":{"code":"queue_timeout","message":…}}` followed by `[DONE]`
   (the client already treats error events as terminal, 007). Non-streaming requests keep today's
   503. The settle table is unchanged (queue timeout stays a counted, uncharged row); `Retry-After`
   moves into the error event as `retry_after` seconds since headers are already sent.
2. **Client.** The message lifecycle distinguishes three silences: no response head by 15 s →
   "<host> didn't answer…" (014's copy, `degraded(engine)`); response head received but no token yet →
   the pending line reads "Waiting for a free slot on <host>…" and the session stays `connected`;
   a `queue_timeout` error event → "<host> is busy — every slot was taken for 30 s. Try again in
   N s." with the countdown, message kept. The `: queued` comments reset the client's first-token
   wait so a queued request is never called asleep.
3. **Evidence.** Gateway tests: head + first `: queued` within 100 ms of admission with Slots=1 and one
   request already running; a comment every 5 s (fake clock or 6 s wait); queue timeout as an SSE error
   with `[DONE]`; non-stream path unchanged. Client vitests for the three silences over the stream event
   source; a Playwright screenshot of "Waiting for a free slot…" against the real host with
   `--slots 1` and a second key holding the slot. `go test -race ./internal/gateway/` and the web checks
   printed.

**Size 2** (≤400 source lines). Concept budget 0 (a comment line and one more message-pending reason,
which is a rendering of existing state). **Sensitive** (streaming relay path).

**Scope contract.** `internal/gateway/request.go`, `proxy.go`, `queue.go` (relay/queue only), their
tests; `web/src/stream.ts`, `web/src/session.ts` or the message reducer, the UI line that renders the
pending state, tests, one screenshot. `docs/ARCHITECTURE.md` is the PM's — list the contract lines
changed.

## Background

- The pipeline (`request.go`): `acquireSlot` waits before `callUpstream`; the head is written by
  `relay` today. Promise 1 moves the head (for streaming requests only) to the moment `acquireSlot`
  starts waiting; a request that gets a slot immediately behaves exactly as today.
- Client: `web/src/stream.ts` (event source), 014's pending turn and 15 s bound (`api.ts`/`Chat.tsx`).
- Ports for any local run: 6620–6629 (valid TCP range). Shared llama-server 127.0.0.1:18080, requests only.

## Log

## Report
