---
id: 039
title: The connect screen is a page — header, two columns and a footer on desktop; the phone card gains five comforts
kind: normal (user-facing)
size: 3
status: drafted
updated: 2026-09-06
release: demo-1
---

# 039 — Connect page

**Why.** Founder (2026-09-06): "the invite screen looks a bit too bare in the demo" and "I question the
wisdom of having the desktop version be only a card in the center". Two visitors arrive at the same
URL: a friend with a code (paste, connect: a card is right) and a stranger from the launch post with no
code (needs to understand what this is and how to host their own: a card on an empty page is a locked
door with no sign). Decision (founder + PM): ONE responsive page, not two products; mobile-first untouched.
The visual spec is `docs/brand/connect-page.html` (the approved mock; the earlier card-only mock is
`docs/brand/connect-comforts.html`).

**Frozen decisions.**
- **≥ 900 px, a page.** Header 48–56 px with a hairline rule: mark 24 px + wordmark left; exactly two
  ghost links right, "Host your own" (→ the README's host quickstart, `SOURCE_URL#quickstart-host` until
  a docs URL exists) and "Source" (→ `SOURCE_URL`). Nothing else in the header, ever (BELIEFS: few
  concepts). Two columns: LEFT the statement (mark 48 px, headline "Chat with a friend’s GPU.", body
  "They send you one code; you paste it here. No account, no install, nothing to set up.", the privacy
  promise `privacyLine('', false)` shape, a 2 px rule, mono facts "Self-hosted · end-to-end encrypted ·
  MIT"); RIGHT the card slimmed to the task (no H1, no description there). Footer across the bottom, mono:
  "Version <v> · MIT · Source on GitHub · About · Made by 2185 Lab"; About is a `<details>` disclosure
  holding the Tailscale sentence, which leaves the card.
- **< 900 px, the card**, as today plus the comforts: the mark 48 px above the wordmark; no header bar.
- **Five comforts, both layouts:** (1) the mark as the anchor; (2) a "Paste" ghost button inside the
  field frame, top-right, using `navigator.clipboard.readText()` on tap and falling back silently
  (button hidden) where the API is unavailable; (3) a one-line mono hint under the field with three
  states derived from the parser the card already runs — empty "Paste the code your friend sent you.",
  valid "✓ Reads as an invite · host <first 6 chars of the address>…", invalid "That doesn’t look like an
  Infercat invite yet." in the danger colour; (4) the quiet line "No code? Ask a friend who runs
  Infercat, or host your own →" after the actions; (5) the Tailscale sentence into About.
- **Welcome-back, revoked, asleep and every other card state** keep their copy and behaviour; they
  render inside the same page frame on desktop (the left column and footer are constant; only the
  card's content is stateful). The chat screen is untouched: its top bar is its header.
- **No new states.** The hint's three states are a pure function of the field's text through the
  existing parser. Concept budget 0. Copy is fixed above; no other copy changes.

**Promises.**
1. The page at 1280, 1024 and 900 px, and the card at 390 and 360 px, match the spec in both schemes;
   the 900 px collapse is clean (no orphaned column, no overflow).
2. The five comforts work: Paste fills the field and triggers the same validation as typing; the hint
   updates on every input; Enter still connects; the About disclosure opens and closes with the keyboard.
3. Every card state (empty, invalid, valid, welcome back, asleep/failed, revoked, relay unreachable,
   log-prompts consent) renders inside the page frame without layout shift of the left column.
4. `pnpm screenshots` and `make launch-check` regenerated (the contrast and no-third-party checks pass;
   `30-connect-desktop.png` now shows the page); `docs/media/friend-chat.gif|png` re-captured only if the
   first frame changed (it shows the connect screen — it did).
5. Gate green: `make check`; `cd web && pnpm typecheck && pnpm test && pnpm lint`; `make brand`;
   `make notices-check`.

**Size 3**, concept budget **0**. Kind normal. Tests: **the promises** — a test for the hint's three
states over the parser, and one for Paste's fallback when `readText` is absent; the suite stays green.
Scope: `web/src/ui/Connect.tsx` (+ a small `ConnectPage` layout component if the file wants one),
`web/src/styles.css`, `web/src/product.ts` (only if a facts-line constant is wanted), `web/dev/screenshots.mjs`
(new page states to capture), tests, media. Non-goals: any change to the chat screen, new links or
sections in the header/footer, a docs site, animation.

**Evidence of completion:** branch `t039-connect-page` with one commit, the gate lines in the log, the
before/after `30-connect-desktop.png` and `30-connect-phone.png` named. Do not merge; the PM lands.

## Log
