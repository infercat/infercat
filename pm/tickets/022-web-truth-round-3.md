---
id: 022
title: Web truth, round 3 — degraded state re-probes itself, delivery marks derive from delivery, honest context copy, phone layout, durable Disconnect (third experience pass)
kind: normal
size: 2
status: dispatched
updated: 2026-09-02
release: demo-1
---

# 022 — Web truth, round 3

## Binding

**Why.** Third experience pass: desktop says launch-ready ("no screenshot here would embarrass anyone");
phone found two false states; synthesis: "not yet — roughly a day of work, none of it architecture."
This product's differentiator is telling the truth when things break; the remaining lies are in the
session and message reducers, so they are fixed there.

**Promises (gate items first).**
1. **Degraded re-probes itself (blocker).** In `degraded(engine)` (host asleep / stopped answering)
   the session re-probes `/me` with backoff (5 s → 10 s → 20 s → 30 s cap) and, on success, returns to
   `connected` on its own — header flips to live values, the thread's Reconnect resolves — exactly the
   way pause already heals within a poll. A host that is up is never called "probably asleep" for more
   than one backoff step. Test with a fake clock: host dead 40 s → alive → connected within ≤30 s.
2. **Delivery marks derive from delivery (blocker).** A turn is marked undelivered only while no
   request containing it has succeeded; the moment a successful request includes that turn (resend,
   Try again, or a later send whose history contains it), its mark clears and it is "part of the next
   question" in truth. The phone's ZEBRA test (send while paused → resume → next send → the model
   recalls ZEBRA) must show no red mark and no "not part of the next question" on that turn. One
   derivation over turn state, no per-site flags.
3. **Context wall copy is true.** The client trims history to fit (reasoning first) and the chat does
   go on. Say so: "This chat has filled the <N>k memory on <host> — older turns will be dropped from
   here on. Start a new chat for a clean slate." Keep New chat; do not disable the composer. Promote
   the note out of the quiet meta style (it decides whether the chat survives). Limits sheet sentence
   matches.
4. **Phone layout.** The third meter is clipped ("— / 4.1k conte") and the page swipes sideways at
   390 px — fix the header row (wrap or abbreviate: "4.1k ctx" with the full word in the sheet) and
   assert no horizontal overflow in the Playwright check at 360 and 390 px. The drawer's Disconnect is
   unreachable on the phone viewport — make the drawer scroll or pin the action.
5. **Disconnect is durable.** Persist a `disconnected` flag beside the stored invite; reload lands on
   the Welcome-back card; auto-connect only when the last state was connected. Name and copy stay.
6. **The revoked card never destroys chats.** "Paste a new code" opens an empty card without clearing
   storage; chats stay under their host namespace ("still on this device" must be true); only
   "Forget this invite" removes anything, and it says what it removes.
7. **Context meter while streaming.** Estimate locally (known prompt tokens + streamed chars ÷ 4) and
   fill the bar during a reply; amber past 80 %; a one-line hint above the composer at 90 %.
   (Budget-permitting after 1–6.)
8. **One name per action.** "Regenerate" on every successful reply; "Try again" only on a failed row.
   Plus, budget-permitting: Copy shows "Copied" for a second; the returning card's sentence reads as a
   sentence ("Your 3 chats with <host> are still on this device."); older undelivered turns get the
   same resend affordance as the newest.
9. **Evidence.** vitests for 1, 2, 5, 6 over the reducers; Playwright on the real host at 390 px and
   1280 px: the asleep→alive self-heal, the ZEBRA sequence, the context wall copy, no horizontal
   overflow, Disconnect surviving a reload, the revoked card keeping chats. Checks printed.

**Size 2** (≤400 TS/TSX source lines of change; contest with the accounting if the layout or the
estimate pushes it). Concept budget 0. **Normal**; the fourth experience pass (phone-led) is the gate.

**Scope contract.** `web/**` except `web/wasm/**`. No Go.

## Background

- Reports: workflow `wf_206fb155-23b` journal; screenshots under
  `/Users/yuanpingsong/.claude/jobs/12b4a99c/tmp/ux3-{desktop,phone}-shots/` (the phone named
  `58-false-asleep.png` for item 1). Ports for local runs: 6720–6729.
- Keep (both personas): everything in the 020 keep list plus the auto-connect from a link, the
  self-healing pause, the per-turn honesty of "still counted against today's tokens".

## Log

All times 2026-09-02, EDT, laptop.

- 18:40 — ACK. Worktree `t022-web-truth-3` at `5d04654` (public main); scope `web/**` except
  `web/wasm/**`, no Go. Read BELIEFS, DESIGN §2, the 014/020 reports, all of `web/src`, both
  harnesses and the phone shots (`ux3-phone-shots/51–94`). pnpm 11, Node 22; `make build`,
  `make web`, `pnpm install --frozen-lockfile`, `pnpm test` (218) green at base. Host for evidence:
  `bin/bunny-network … --dev-listen 127.0.0.1:6720`, preview on 6721; the shared llama-server only
  receives requests.
- 18:50 — Two premises checked against the real host before any edit (probe script in the job's
  tmp, not the repo):
  (i) **Promise 3's mechanism is not in the code.** Nothing in `web/src` trims history; the gateway
  refuses a prompt over the context (`internal/gateway/proxy.go:238`, `context_too_long`) and
  shrinks `max_tokens` to what is left (`:241`). Measured: after a 51 + 4045 = 4096 wall the next
  send went through with `prompt_tokens: 3339` — the thread fit again only because the model's
  *thinking* is never sent back (`Chat.tsx toChatMessages`), not because anything was dropped. So
  "older turns will be dropped from here on" would be the new lie. The copy will say what happens:
  replies get shorter until a message no longer fits, and the host says so; New chat stays; the
  composer stays. Judgment call, declared in the report; client-side trimming listed as a candidate.
  (ii) **Whether a dead session heals by itself once the host is back** (the reviewer's 57 says
  not, at 2 min): probing now — decides whether the self-probe is a /me over the session it has,
  or a fresh dial when that /me cannot get through.

- 19:00 — (ii) answered: **a session whose host restarted behind it never answers again.** Probe
  on the real host: killed at 3.7 s, back at 23.9 s, the pill read "not answering" from the poll's
  first timed-out /me (43.9 s) through 184 s — four 30 s polls, nothing — and a send over it at
  184 s was still "Waiting for the first token…". The reviewer's `57-same-tab-recovered.png` shows
  the same at 2 min. So promise 1's probe is a /me over the session it has only when that session
  reached the host (engine down, path unmeasurable); when the host stopped answering (`!meOk`) the
  probe is a fresh dial — what Reconnect does by hand — and the reducer adopts the candidate the
  moment the invite verifies over it (`verified` from `degraded`, old transport closed by
  `dropped()`). The bridge's `connect()` retries the handshake every 5 s for 60 s
  (`web/wasm/main_js.go pingUntil`), so a dial that starts while the host is still dead finds it
  within ~5 s of its return.
- 19:05 — Design, in the order the ticket names:
  (1) `Live.probed` counts misses; `probeDelay(n)` = 5 → 10 → 20 → 30 s cap; one event `probed`;
  one effect `useProbe` in App.tsx, armed on the miss count and on whether the session is degraded
  at all — never on the poll's other news, so a 30 s poll cannot keep resetting a 30 s probe; the
  `probing()` rule: every degradation but `key` (a pause heals on the poll; a revoke cannot heal).
  `dialAgain()` is the one dial shared by Reconnect and the probe.
  (2) `Message.pending` deleted with `settlePending`; `undelivered(messages)` walks back from the
  end and marks the user turns after the last reply that got through (`delivered`: any ended reply
  but `interrupted` — a model that only thought was still asked). Chat derives the set once; the
  mark and the action's name read it. "not part of the next question" is said only of text that is
  on screen. (3) Copy as logged at 18:50; `.ended.wall` promoted (own block, accent, text colour);
  the sheet sentence matches. (4) CSS: `.meters` wraps at ≤760 px; the drawer is `height: 100dvh`
  with `bottom: auto` — measured in the phone emulation: layout viewport 927 px, visual 844, the
  foot was at 878–927. (5) `LastHost.left`, written by Disconnect (`rememberLeft`), cleared by the
  next verify; `dialsOnArrival(last, viaLink)`: a link always dials. (6) `pasteNewCode` (thread and
  card) forgets the dead code only — a reload must not dial it again — and nothing else; "Forget
  this invite" is the one remover and says so under the button. (8) "Regenerate" on every reply
  that got through, "Try again" only when the last exchange failed; the returning sentence reads
  as one; Copy already showed "Copied" (2 s). Promise 7 does not fit: 363 of 400 source lines at
  the WIP commit. No new state, no new concept.

## Report
