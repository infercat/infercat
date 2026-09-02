---
id: 016
title: Host — seed daily counters from usage.jsonl at start; invite line after the QR
kind: normal
size: 1
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 016 — Host: durable daily counters, invite line placement

## Binding

**Why.** Both friend personas saw their usage meter snap to zero after the host restarted ("I'd used
5.8k tokens a minute earlier"), and one host persona lost the invite line under a 30-line QR.

**Promises.**
1. At gateway start, per-key `today` counters (and `last_seen`) are seeded from `usage.jsonl` since UTC
   midnight (one `Aggregate` call; `docs/DESIGN.md` §4 item 5). Test: record usage, restart the gateway
   with the same recorder file, `/me.usage.today_tokens` equals the pre-restart value.
2. `keys add` / `keys rotate` print the invite (or link) line again **after** the QR, so the last thing
   on screen is the thing to copy; `--no-qr`/`--json`/non-TTY behaviour unchanged.
3. `go test ./...` printed; a before/after of the `keys add` output in the report.

**Size 1** (≤150 source lines). Concept budget 0. **Normal**.

**Scope contract.** `internal/gateway/limits.go` (+ `gateway.go` for the seed call and a `Config`
field or constructor argument for the aggregate source — keep it to what promise 1 needs; ticket 014
is concurrently making a one-row change in `request.go`), `cmd/bunny-network/serve.go` (wire the
seed), `cmd/bunny-network/keys.go` (invite line), tests.

## Log

## Report
