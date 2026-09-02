---
id: 009
title: Host polish — first-run truth, invite destination, key hygiene, accounting words (from the stranger pass)
kind: normal
size: 3
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 009 — Host polish (CLI first ten minutes)

## Binding

**Why.** Three fresh-context strangers set up the host from the README and `--help` alone (llama.cpp,
Ollama-user-without-Ollama, remote vLLM). All reached an invite in 2–6 minutes; none gave up; all
three praised `--help`, the banner, the error house style, and the privacy posture — keep those. What
they hit is below, ranked by the synthesis. The founder's theme is polish: a stranger's first ten
minutes must be clean.

**Promises (each with a test where logic is involved; copy items get a before/after in the report).**
1. **First-run address bug (blocker).** On a fresh data dir, the first `serve` prints a tunnel address
   that differs from the address every later `serve` (and `SavedAddr`) produces, so invites minted in the
   first session never connect. Find the cause in `internal/tunnel` (hypothesis: the region pinned into
   `host.key.json` and the region the first start actually used diverge; the address must be derived
   from the saved key exactly as `SavedAddr` does, and the server must start with that pinned region).
   Test: fresh dir → `Start` → `Addr()` equals `SavedAddr(dir)` equals `Addr()` after restart. This is
   001's file; you may edit it under this ticket.
2. **Invite destination.** Add `product.WebURL` (string constant, may be empty until hosting is decided)
   and a `serve --web-url` override persisted in config.json. When set, `keys add`/`rotate` print
   `Send Alice this link: <WebURL>#<invite>` and the QR encodes the link; the banner names the web URL
   on its own line. When empty, print `Alice pastes this code into the web app (serve web/dist yourself
   for now — see README)`. The web app already accepts a `#bn1.…` fragment (ticket 007 adds it).
3. **Duplicate names.** `keys add <name>` refuses when an active or paused key with that name exists:
   `alice already has an active key (k_…, last seen …). Lost the invite? keys rotate alice. Second
   device? keys add alice-laptop.` `--force` mints anyway.
4. **`--name` default** = OS hostname; banner shows `name  <value>  (shown to your friends)`; if truly
   empty the `/me` host name is empty and the client omits the sentence (007 handles the client side).
5. **No-upstream error** ends with the runnable fix: `bunny-network serve --upstream http://127.0.0.1:<port>`
   and names that `--upstream` is a `serve` flag.
6. **Accounting words.** `usage` counts model calls as requests and shows web-app polls separately
   (`requests 1 model call (+5 app polls)`); `status` RPM and `usage` agree on the definition. Latency
   percentiles are over successful completions only. Limits line says what the numbers count
   (`completions only; /v1/models is free` — verify against the gateway: 006 made `/v1/models` consume
   RPM; pick the truthful sentence).
7. **Blast radius line** in the banner: `access  friends reach only /v1/models and /v1/chat/completions
   on <upstream> — nothing else on this machine`. Verify the route list against the gateway.
8. **Data dir truth.** Banner gets a `data  <dir>` line. First creation of `host.key.json` prints a
   one-time note: it is the host identity, back it up, do not sync it, deleting it invalidates every
   invite. `--help` for `--data-dir` names the files.
9. **Revoke hygiene.** `keys revoke` asks `[y/N]` (or `--yes`) and names `pause` as the reversible
   alternative. After any key write, the CLI pokes the running daemon over the admin socket
   (`POST /reload`, unix socket only, Protection 1 intact) so the change is live before the command
   returns; if no daemon, nothing. Test: revoke → next request 403 without waiting a second.
10. **Remembered upstream that is down.** The warning names the config file and the way out:
    `serve --upstream auto` re-runs detection and forgets the remembered URL.
11. **Machine-readable invite.** `keys add --json` prints `{"key_id","name","invite","link"}` only; the
    QR is skipped automatically when stdout is not a TTY, and by `--no-qr`.
12. **One-liners.** Pluralise `1 keys`; the zero-active banner line is actionable; `keys list` LAST SEEN
    derives from model calls, not polls.

**Size 3** (≤900 source lines). Concept budget 1: `web_url` config key. **Normal**.

**Scope contract.** `cmd/bunny-network/**`, `internal/admin/**` (reload endpoint + client),
`internal/keys/store*.go` (name uniqueness helper), `internal/usage/aggregate*.go` (accounting words),
`internal/product/product.go` (WebURL), `internal/tunnel/**` (promise 1 only). Not `internal/gateway`
(if the accounting needs a gateway change, contest with the symbol). Not `web/**`.

**Keep as is (from the strangers; do not "improve"):** `--help` and per-command help; the banner's
order and density; error house style (cause + fix in one sentence); flag memory; `--log-prompts`
wording; the invite output's self-explanation and QR; commands working without the daemon; graceful
shutdown wording; empty states.

## Background

- Raw reports and synthesis: workflow `wf_e6401585-7ab` journal under
  `~/.claude/projects/-Users-yuanpingsong-Desktop-repos-2185Lab-kb/12b4a99c-7f66-4439-8d81-9c1d93c1cdee/subagents/workflows/`.
- The README quickstart and the Makefile `web`→`wasm` dependency are the PM's (landing separately).

## Log

## Report
