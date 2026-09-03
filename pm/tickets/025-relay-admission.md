---
id: 025
title: Relay admission — only registered host keys are relayed (tier 1), no metering
kind: sensitive
size: 2
status: draft
updated: 2026-09-03
release: after-launch
---

# 025 — Relay admission (after launch, before anyone has a reason to look for the address)

**Why.** Our self-hosted DERP relay sits on the public internet with a hostname; by default it relays
any valid key. Founder ruling: prevent free-tunnel abuse by **admission**, not metering — the relay
admits registered host identities; friends are admitted by reaching a present host (rendezvous), so a
stranger cannot forward through it. No per-byte accounting, ever (BELIEFS).

**Binding (to be priced when dispatched).**
1. The relay runs with verification enabled and consults a small admission service: "is this node
   key a registered host?" Cache admissions on the relay so the service being down never drops a
   running host.
2. `serve` registers the host identity with the admission service on first run (and re-registers on
   key regeneration); `--ephemeral` hosts register a short-lived entry. Registration is the only call a
   host makes to our infrastructure; prompts never do. Documented in README under "What touches our
   servers": the relay (ciphertext only, when the direct path is unavailable) and this registration.
3. Friend keys are NOT registered (tier 2 declined for now: invite sharing stays a gateway-level
   concern, per BELIEFS Protections).
4. Evidence: an unregistered tailcat client pointed at our relay cannot reach a registered host; a
   registered host works with the admission service stopped (cache); registration survives restarts.

**Backlog / declined:** tier 2 (friend keys on the relay), tier 3 (metering — declined by ruling).

## Log

## Report
