---
id: 008
title: Architecture review and debt inventory after demo-1 (investigation → docs/DESIGN.md)
kind: investigation
size: 3
status: landed
updated: 2026-09-02
release: demo-1
---

# 008 — Architecture review and debt inventory

## Binding

**Why.** Four review rounds (Claude adversarial on 001/002/003; second-model on integrated main) found
~30 confirmed defects. The founder's ruling: they cluster by cause, and each cause is a missing explicit
structure, not a missing check. Patching them one by one produces brittle software. This ticket designs
the structures, inventories the debt, and says what to delete — so the next work is designed, not listed.

**Deliverable.** `docs/DESIGN.md` on branch `t008-architecture` (no product code changes), containing:
1. **Gateway request pipeline.** The stage order, the request record and its single exit, where each
   deadline lives, reserve-then-settle accounting, the one normalization step with the engine-override
   denylist, the bounded slot queue. State the invariants a test can check (e.g. "every admitted request
   releases every resource exactly once on every exit"). Compare against `internal/gateway` as it stands
   on main and as ticket 006 is restructuring it (read `pm/tickets/006-*.md` and the branch if pushed).
2. **Web session and message state machines.** States, events, transitions, and which UI surface renders
   which state; the storage key. Compare against `web/src` on main and ticket 007's branch if pushed.
3. **Host upstream as a state.** unknown → probing → identified(kind, ctx, slots) → healthy | unhealthy,
   who observes changes (gateway slots, /me, status), and the interface the gateway should depend on.
4. **Debt inventory with disposition.** Every shim, workaround, duplicated check, and one-off flag in the
   tree (start with `cmd/bunny-network/main.go` `reorder()`, the per-site clamps in `proxy.go`, the ad hoc
   effects in `Chat.tsx`/`Connect.tsx`, the `keys.json` mtime throttle, the two-lane usage counters
   (in-memory vs JSONL)) — each with: keep / redesign (which section above) / delete, and why.
5. **Concept budget check.** List the product concepts the code now embodies against BELIEFS.md's
   "one concept per thing"; flag any that leaked in (states, options, error codes, config keys) and
   propose merges or deletions.
6. **Proposed tickets** (titles + one-paragraph binding each, priced 1/2/3/5) for the cleanup that should
   follow demo-1, in the order that reduces risk fastest. Nothing here is scheduled by you; the PM prices.
7. **What NOT to change**, with reasons — the parts of the design that the reviews showed are sound
   (e.g. tunnel exposure, invite format, key store hashing).

**Size 3** (investigation; the document, not code). Report appended to this ticket; the document is
the artifact.

**Scope contract.** `docs/DESIGN.md` and this ticket file only. Read everything; write nothing else.

## Background

- Review outputs: ticket files 001–003 (Claude review summaries at the end of each), ticket 002's and
  006's binding sections, the second-model review JSON at
  `/Users/yuanpingsong/.claude/jobs/12b4a99c/tmp/review-integrated.out`, and the workflow journals under
  `~/.claude/projects/-Users-yuanpingsong-Desktop-repos-2185Lab-kb/12b4a99c-7f66-4439-8d81-9c1d93c1cdee/subagents/workflows/*/journal.jsonl`
  (finder findings and refuter verdicts, with reproductions).
- PM's working opinion (test it, do not assume it): gateway = explicit pipeline with a request record;
  web = session reducer owning the transport + message lifecycle reducer; host = upstream as a state
  with observers; delete `reorder()` in favour of a parser that accepts flags after positionals.
- The founder's standard: simple, few concepts, few states, promises kept because there are few enough to
  keep. A great product gets simpler after release.

## Report

Engineer Claude Fable 5.1, 2026-09-02. Lane `t008-architecture`, fast-forwarded mid-slice from `6fbc63b` to
`5456e59` (origin/main; `ed3033f` = 006 landed) on the PM's word so §1 critiques the landed pipeline, not the
branch. Artifact: `docs/DESIGN.md` (commit `36564be`, 818 lines). No product code touched.

### Core

The founder's four-cause ruling holds under the code; the document names the missing structure per cause and
anchors every claim to `file:line` at `5456e59` (spot-checked 24 anchors by printing the lines). Seven sections
as bound. The load-bearing findings:

1. **Gateway (§1).** 006 delivered the record + one exit; what is still implicit is (a) the charge/count rule,
   split across `request.go:253, 316-318`, `proxy.go:441-444, 544-546` — proposed: an `outcome` on the record
   and one settle table (which closes both 006 backlog rows as rows, not patches); (b) the "queue" is a
   channel semaphore with three side counters (`gateway.go:58-63, 204-249`): not FIFO, over-admits on
   `SetSlots`, `status` numbers kept beside it — proposed: a FIFO slot queue reading `Info().Slots` and
   `SetSlots` deleted end to end; (c) `RequestTimeout` is still an absolute cap on a stream (`request.go:271`)
   — proposed: engine first-byte + idle deadlines, the flag/config key deleted. Invariant table I1–I10 says
   which tests exist (`hardening_test.go`) and which are missing (I5 ceiling, I6 outcome rows, I7 FIFO, I8 idle).
2. **Web (§2).** Four session states (the ruling's three loading states are one `connecting.step`), an event
   and transition table, one effect that closes any transport a transition drops, the message reducer over
   007's event source, storage keys, W1–W7 as vitests. With 007 on everything it has written; two asks recorded.
3. **Engine (§3).** `Unknown` kind replaces the `sniffed` flag; `Health{OK,Since,Err}`; readers not observers;
   a three-method `Engine` seam (`Info`, `CountTokens`, `Do`) so the engine URL never crosses into the gateway.
4. **Debt (§4)** 21 items with keep/redesign/delete and before/after; **concepts (§5)** every state, flag,
   config key, code, route argued (−3 flags, −3 config keys, −1 method, −2 seam methods, −4 mirror types,
   +1 kind, +1 internal enum); **tickets (§6)** six, priced 3/2/1/1/1 + a PM doc task; **not to change (§7)**.

Where the Background opinion is wrong (§0.3): "one exit" is a settle table not a return; a reducer cannot own
I/O (007's split is right) and three of the seven ruled states are one; "observers" should be readers and the
one push (`SetSlots`) deleted; `reorder()` goes, the replacement is stdlib `flag` in a loop.

### Read

BELIEFS (incl. the 006-landing changes), ARCHITECTURE, tickets 001–008 in full (bindings, logs, reports,
contests, rulings, review summaries), DECLINED, HANDOFF, MEASURE, README, the second-model JSON (39 findings,
all four lenses), 006's landed code and report/ruling on main, 007's working tree (`api.ts`, `storage.ts`,
`transport/index.ts`, ticket log + design ruling), and every file in `internal/{gateway,upstream,keys,usage,
tunnel,invite,admin,product}`, `cmd/bunny-network`, `web/wasm/main_js.go`, `web/src/**`, plus `web/dev`
heads, `vite.config.ts`, `Makefile`, `go.mod`. Test-name inventory across Go and vitest.

### Verified by running (printed)

- On `6fbc63b`: `go build ./...` exit 0 · `go vet ./...` exit 0 · `go test ./...` 11 packages ok, top-level
  **101 passed / 0 failed / 2 skipped** (the two opt-in live tests) · `-race` on gateway/keys/usage ok.
- On `5456e59` after the fast-forward: build 0 · vet 0 · **113 passed / 0 failed / 2 skipped** · gateway
  `-race` ok (9.5 s).
- Web, in a copy under `~/.claude/jobs/12b4a99c/tmp/t008-web` (no `node_modules` in the worktree):
  `pnpm install --frozen-lockfile --offline` 0 · `pnpm typecheck` 0 · `pnpm test` **5 files / 78 passed /
  0 failed / 0 skipped**.
- 24 cited anchors printed and matched (e.g. `request.go:253 q.queued = true`, `gateway.go:233 waiting cap`,
  `store.go:74 throttle`, `main.go:198 reorder`).

### Could not verify

- The workflow journals (`subagents/workflows/*/journal.jsonl`): a grep for verdict fields and "reserv"
  hit ugrep's complexity limit; I relied on the PM summaries appended to 001–003 and 006's binding instead.
- 007's branch has no commits; its direction is read from an uncommitted tree that may have moved since.
- No live engine or relay run: nothing here needed one, and the shared llama-server was not touched.
- The FIFO queue and the shrink-to-fit reservation are designs, not code; their invariants (I5, I7) are
  stated for a test, not yet proven by one.

Edge awareness, one line: 006's rebase conflict sat exactly in `acquire`/`SetSlots`, which §1.5 removes; the
"10-token TPM key" objection to reserving the worst case is answered by shrinking the reservation to fit
(floor 16) rather than rejecting; `/v1/models` is the one route that bypasses the stage list; `countTimeout`
is dead for tokenize but live for the model list; `--ephemeral` is what keeps `hostAddr`'s admin lookup alive.

### The three decisions I most want ruled

1. **Deadline ownership (§1.6).** Delete the absolute `RequestTimeout` (300 s over a whole stream; cuts a
   slow public host mid-answer) in favour of an engine first-byte bound (120 s) and an engine idle bound
   (60 s), keeping 006's read/write deadlines; delete `--request-timeout`/`--queue-timeout`/`--max-body` as
   flags and config keys (constants). Contract change to ARCHITECTURE §Concurrency & queue.
2. **Reservation = worst case, shrunk to fit (§1.4).** Reserve `prompt + max_tokens` against TPM and daily,
   shrinking `max_tokens` to what the window allows (floor 16) the way context already shrinks; settle to
   actual in `finish`. Makes the limits ceilings for tokens, not prompts; overrides 006 judgment 7. The
   settle table also decides that a waiting-set overflow does not count against RPM (006 counts it).
3. **FIFO slot queue with a live cap, `SetSlots` deleted (§1.5, §3.3).** Replaces the channel semaphore +
   three atomics + generation swap; the queue reads `Info().Slots` at acquire/release; `gatewayServer`
   loses a method and `refreshLoop` its slot plumbing (005 fix 10d becomes structural). Exact `status`
   numbers, FIFO fairness, no over-admission on resize.

Already ruled, one ask only: if 007 lands `degraded` as a state, keep it and add no more (§2.2).

### Freeze

- **Base:** `5456e59` (origin/main; fast-forwarded from the dispatched `6fbc63b`). **Lane:** `t008-architecture`.
  Artifact commit `36564be` (docs/DESIGN.md only); this report is a docs-only commit on top.
- **Patch SHA-256** (`git diff 5456e59..36564be -- docs/DESIGN.md | shasum -a 256`):
  `885e3336908b6fd1ce542997532c6c4b10d8b840c538a45287716416e4f03589`
- **Accounting:** product source 0 lines; tests 0; `docs/DESIGN.md` +818 lines (the deliverable); this ticket
  file (report). Files outside the scope contract: 0. Concepts added to the product: 0 (the document proposes
  net removals; nothing is implemented). Dependencies: none. Size 3 as priced.
- **Checks at freeze:** printed above (Go 113/0/2 on the base; web 78/0/0 in the tmp copy).
- **Production-touching actions:** none. No secrets, no dotenvx, no max-ws.lab, no engine restarts; the only
  writes outside the worktree were the web copy under the job tmp dir.

## Ruling (PM, 2026-09-02 15:10)

**Landed** (`docs/DESIGN.md` on main at `420d5a5`). All three decisions **accepted**:
1. Deadline ownership per §1.6: absolute `RequestTimeout` deleted; engine first-byte 120 s + engine idle
   60 s + client write 60 s; `--request-timeout`, `--queue-timeout`, `--max-body` become constants.
2. Reservation = `prompt + max_tokens`, shrunk to fit TPM/daily (floor 16), settled to actual; one settle
   table (§1.4) decides counted/charged; waiting-set overflow is not counted against RPM.
3. FIFO slot queue reading `Info().Slots` live; `SetSlots` deleted end to end.
Also ruled: §4 item 14 (usage events for POST routes only) accepted, after launch, in the concept trim;
`--ephemeral` stays (a real host use: a throwaway session); §2.2 `degraded` stays as 007 lands it, no
more states. §0.3's corrections to the PM's opinion are accepted as written and the Background is left
as the record of what was believed before the review.

**Tickets:** 010 (settle/queue/deadlines, size 3, before launch) → 011 (engine state + `Engine` seam,
size 2, before launch), one Fable engineer in sequence to avoid seam conflicts; 012 concept trim +
CLI/store cleanup and 013 007-follow-through drafted for after launch. `docs/ARCHITECTURE.md` v1 is the
PM's, written when 010/011 land (the contract changes with the code, not before).
