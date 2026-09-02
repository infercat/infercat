---
id: 008
title: Architecture review and debt inventory after demo-1 (investigation → docs/DESIGN.md)
kind: investigation
size: 3
status: dispatched
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
