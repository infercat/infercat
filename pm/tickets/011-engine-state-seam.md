---
id: 011
title: Engine state (Unknown kind, Health) and the three-method Engine seam (DESIGN §3)
kind: sensitive
size: 2
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 011 — Engine state and seam

## Binding

**Why.** `docs/DESIGN.md` §3: the engine was modelled as a value read once; 005 patched it with a
`sniffed` flag and `defaultSlots`, and 006/010 read slots through plumbing. The engine is a state
(`Unknown → identified`, `Health{OK, Since, Err}`) that every consumer reads at decision time, behind a
seam so small the engine's URL cannot leak into the gateway.

**Promises.** Implement §3.2–3.5 as written: `Kind == Unknown` replaces `sniffed` and `defaultSlots`;
`Health` replaces `Healthy`; `Detect` returns the first candidate that reaches OK; the `Engine`
interface (`Info`, `CountTokens`, `Do`) replaces `BaseURL`/`Transport` in the gateway's imports; the
redirect guard, bearer, first-byte timeout and `probeTimeout` live in the one `http.Client` the engine
owns; `countTimeout` deleted; banner/`status`/`/me` print "not identified yet / NOT ANSWERING for Ns"
and `kind:"unknown"`. Evidence: E1–E5 (E4 is a compile-time check: the gateway imports only `Engine`).

**Size 2** (≤400 source lines net). Concept budget 0 (+1 kind constant, −1 flag, −2 seam methods).
**Sensitive** (Protection 1 gains a structural form).

**Scope contract.** `internal/upstream/**` (including `upstream.go` — the PM releases the seam file to
this ticket), `internal/gateway/**` for the import change only, `cmd/bunny-network/serve.go`/`status.go`
for the banner/status words. Lands after 010 (same engineer, same worktree, rebased).

## Log

- 2026-09-02 12:10 ACK (PM's message: start 011 on the rebased 010 without waiting). Base 19e3598 (origin/main with 009) + 010's commits on the same lane. Read DESIGN §3, this ticket, internal/upstream/** and every seam consumer (gateway, cmd, admin, wasm, hack). No contest. One item outside the scope contract, declared up front: `status` cannot print "NOT ANSWERING for Ns" without the time, so `admin.Upstream` gains one field (`since`, when health last changed) — one line in internal/admin, filled by `buildStatus`.

## Report
