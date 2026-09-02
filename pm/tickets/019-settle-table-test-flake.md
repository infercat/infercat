---
id: 019
title: TestI6SettleTable is load-sensitive — make it deterministic
kind: normal
size: 1
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 019 — Settle-table test flake

**Why.** In the fresh-clone launch gate (main `647f62b`), `go test -count=1 -v ./internal/gateway/`
reported `--- FAIL: TestI6SettleTable (0.98s)` once while the machine was under load (two Playwright
browsers and two hosts running for the experience pass). Alone, ×3, and under `-race` ×3 it passes;
four more full-package verbose runs pass. A test that fails under load is a defect in the test
(family playbook: zero flaky exemptions).

**Promises.**
1. Find the real-time dependency in `TestI6SettleTable` (candidates: the "Cut: stream, engine idle"
   and "EngineErr: first byte" rows, which exercise deadlines; any `time.Sleep`-based ordering; the
   `QueueLost: timed out` row's QueueTimeout) and remove it: inject the clock/deadlines the gateway
   already parameterises, or make the assertion wait on the event it needs rather than on elapsed time.
2. Evidence: `go test -count=25 -run TestI6SettleTable ./internal/gateway/` green, and the same while a
   CPU hog runs (`yes > /dev/null` ×4 in the background, killed after); `-race` green. Print both.
3. Sweep the other invariant tests (I1, I5, I7, I8) for the same pattern; fix any that share it within
   the budget, else list them.

**Size 1** (≤150 lines, tests only). Concept budget 0. **Scope:** `internal/gateway/*_test.go`; if the
fix needs a non-test seam (an injectable clock), contest with the symbol.

## Log

## Report
