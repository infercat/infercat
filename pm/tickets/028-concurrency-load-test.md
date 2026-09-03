---
id: 028
title: Concurrency load test — 30 friends against real engines; publish the limits with numbers
kind: investigation
size: 2
status: queued
updated: 2026-09-03
release: demo-1
---

# 028 — Concurrency under real load

**Why.** Founder (2026-09-03): the engine's concurrency must be (1) reliable and (2) taken into account
by the host. The gateway's admission, per-key concurrency, FIFO slot queue, bounded waiting set,
keepalives, and settle table are designed for exactly this and are proven against fake engines under
`-race` (I1/I5/I7/I8, 019/021). What is NOT proven: the interaction with a real engine under sustained
load from many clients. This ticket measures it and turns the result into published limits.

**Binding.**
1. A script `hack/load.sh` (or Go program under `hack/load/`) that runs N concurrent simulated friends,
   each with its own key, each sending streamed chats in a loop with realistic prompts, for M minutes,
   against a running host; records per request: key, status, code, queued_ms, ttft_ms, tok/s,
   completion; samples `status` (in-flight/waiting) and the engine's memory every second.
2. Runs: N = 2, 6, 12, 30 against (a) the laptop llama-server (2 slots) and (b) the workstation vLLM
   over the read-only ssh forward (`--slots` set to its real max-num-seqs; read-only means requests
   only). Also the pathological cases: a friend who never reads their stream; a friend who sends 4 MiB
   bodies; a friend with `max_concurrent 3` bursting; the engine restarted mid-run.
3. Named failure modes to look for: the engine refusing an admitted request (llama.cpp "no slot
   available" → must surface as queue/busy, never as a raw 502); llama.cpp's shared context budget
   across slots (`-c` divided by `-np`) causing `context_too_long` at lower prompt sizes than `/me`
   advertises; memory growth on the host binary; queue starvation; any request that neither completes
   nor errors within its deadlines.
4. Deliverable: `docs/MEASURE.md` gains a "Concurrency" section with the numbers and the observed
   behaviour at each N; `docs/LIMITS.md` (new, one page) states what the host guarantees in plain words
   ("with an engine that serves S requests at once, at most S run, up to 2S wait ≤30 s, the rest are
   told to retry — per friend at most one at a time by default"); any defect found becomes its own
   ticket with a repro.
5. Recommendation for the public demo engine (vLLM vs llama.cpp) with the slot setting to use.

**Size 2**. Concept budget 0. **Scope:** `hack/**`, `docs/**`; no product code (defects go to tickets).

## Log

## Report
