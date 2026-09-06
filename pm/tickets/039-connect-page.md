---
id: 039
title: The connect screen is a page — header, two columns and a footer on desktop; the phone card gains five comforts
kind: normal (user-facing)
size: 3
status: dispatched
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

### 2026-09-06 — engineer

Built the page. Branch `t039-connect-page`, rebased onto `d818a17` (the loaf mark landed on main
while this was in flight — see "The mark", below).

**Files.**
- `web/src/ui/Connect.tsx` — a `Page` component renders the frame (header, statement, footer) and
  every card state is passed through it as children, so the left column is constant and only the
  card's content is stateful (promise 3). `CardHead`/`CardFoot` carry the H1, pitch, privacy line
  and small print that the page's own columns say above 900 px. Adds the Paste button, the hint,
  the no-code line, and the About disclosure.
- `web/src/invite.ts` — `inviteHint(text)`, a pure function of the field's text through
  `decodeInvite`: `empty` / `valid` (+ the address's first six characters) / `invalid`. No state.
- `web/src/ui/clipboard.ts` (new) — `clipboardReader(navigator)`, the predicate Paste is rendered
  behind. Shaped like `composing` next door, so the fallback is a test and not a browser matrix.
- `web/src/product.ts` — `HOST_URL` (`SOURCE_URL#quickstart-host`), used by the header link and the
  no-code line so the two are one constant.
- `web/src/styles.css` — the card comforts, and one `@media (min-width: 900px)` block for the page.
  The mock's four per-frame measures (gutter, gap, card width, headline) became four clamps, each a
  ramp between the 1280 frame and 900 px: at 1024 they land exactly on the mock's 32 / 52 / 400 / 44.
- `web/src/invite.test.ts`, `web/src/ui/clipboard.test.ts` — the two tests the ticket asks for.
- `web/dev/screenshots.mjs` — the 039 page states, with assertions (chrome present iff ≥ 900 px, the
  card's H1 present iff < 900 px, one Paste button, the hint reads the code back, no overflow).
- `web/dev/launch-check.mjs` — see "Checks changed", below.

**Gate.** All green, in this order, on the rebased branch:

    make check EXIT=0  |  CHECK OK
    pnpm typecheck EXIT=0
    pnpm test EXIT=0  |  Tests  266 passed (266)
    pnpm lint EXIT=0
    make brand EXIT=0
    make notices-check EXIT=0  |  notices: OK — 49 Go + 110 npm dependencies + 2 bundled fonts, licences all in ALLOWED, verbatim texts present
    pnpm screenshots EXIT=0  |  66 screenshots in dev/screenshots/
                             |  No console errors, no page errors, no horizontal overflow at 360 px or 390 px.
    make launch-check EXIT=0  |  launch-check: OK

Launch-check's connect lines (both schemes, the built bundle):

    30-connect-desktop: 7 requests, 0 to a third party — none, all same-origin
    30-connect-desktop: 7 controls in the accessibility tree, 0 unnamed
    30-connect-desktop: 16 text runs, lowest 6.31:1 (p.promise) — AA met
    30-connect-desktop: Tab → a[Host your own] → a[Source] → textarea[Invite code] → button[Paste] → a[host your own] → a[Source on GitHub]
    30-connect-dark: 16 text runs, lowest 7.5:1 (a) — AA met
    30-connect-phone: 12 text runs, lowest 6.31:1 (p.pitch) — AA met
    30-connect-phone: Tab → textarea[Invite code] → button[Paste] → a[host your own] → a[Source on GitHub] → summary[About]
    30-connect-phone: no horizontal overflow at 390px
    chat: connected in 0.8 s (relayed via New York · 67 ms)

The chat capture ran against a host built from this branch on port 9096 with its own `--data-dir`
under the worktree, against the laptop engine on 18080; the host and its data were removed after.
The founder's host on 9091 was not touched.

**Screenshots.** Before / after: `30-connect-desktop.png`, `30-connect-dark.png`,
`30-connect-phone.png` (all three regenerated in place — the desktop pair now show the page, the
phone the card with its five comforts). New: `39-page-1280-light.png`, `39-page-1280-dark.png`,
`39-page-1024.png`, `39-collapse-899.png`, `39-phone-390.png`, `39-about-open.png`.
`docs/media/friend-chat.gif|png` re-captured — its first frame is the connect screen at 880 px and
it changed. `web/public/og.png` and `.github/social-preview.png` follow from `make brand`, which
renders the real connect screen.

**The mark.** The page renders the mark from `web/public/favicon.svg` via `<img>` — the one file
that owns the geometry, and the one `web/dev/brand.mjs` renders every other raster from — so the
mark contest still changes exactly one file, and that file's own `prefers-color-scheme` switch is
what makes it flip on black. This was written against the interim cat; main then decided the loaf
(`c3a22f9`), and after the rebase the header and the statement picked it up with no code change.
The mock draws `content-a.svg`, the PM's pick at the time; the layout does not depend on it, as the
mock says. Rebasing (rather than leaving the branch on `255b238`) was to keep the committed
screenshots honest — evidence showing a superseded mark would only have to be re-reviewed. The only
rebase conflicts were `og.png` and `.github/social-preview.png`, both `make brand` outputs, resolved
by taking main's and regenerating.

**Three judgement calls, flagged for the ruling.**

1. *The invalid state shows two lines.* The hint says "That doesn't look like an Infercat invite
   yet." in mono; under it the existing `.inline-error` still gives the parser's specific reason
   ("An invite starts with ic1. — this one starts with ic9."). The ticket adds the hint and says no
   other copy changes, so the inline error was kept: it is 014 promise 10's mechanism, and deleting
   it would remove a shipped promise. It reads as summary-then-reason, but the mock draws one line.
   Say the word and the inline error goes.

2. *The field grows past three rows.* The mock reserves the Paste corner on the first line only,
   with a floated spacer. A `<textarea>` cannot float anything, so the corner is reserved on every
   line (`padding-right: 66px`) — and that alone is enough to push a real ~90-character invite to a
   fourth row at 390 px, where it would have scrolled its own first line out of sight. So the field
   now sizes to its content with the mock's three rows (85 px) as the floor: empty and short codes
   are unchanged, long ones are fully visible. Measured at 360 / 390 / 899 / 1024 / 1280 with the
   demo code, a real tailcat invite and the mock's code: no clipping anywhere.

3. *`.hint` was taken.* The chat composer already owns `.hint`, and centres it. The connect line is
   `.code-hint`.

**Checks changed (both in `web/dev/launch-check.mjs`).**

- The tab-order assertion was "`order[0]` must be the invite field". The frozen header puts two
  ghost links before it, so it now asserts the field is the first control *past the chrome*, and
  that the chrome is at most those two — the same intent, and it additionally holds the header to
  two links for good. Below 900 px there is no chrome and the field is still first.
- After the tab-order walk the check now blurs. With more than six controls on the page, Tab stops
  on one and left a focus ring in the evidence shot; it used to run off the end of the document and
  leave none. The shot is meant to show the page, not the checker's cursor.

**One defect found by that check, and fixed.** Paste inside the `<label>` made it part of the label,
so the field announced itself as "Invite code Paste". The field is now `htmlFor`/`id`-associated and
the button is a sibling: back to "Invite code", 0 unnamed controls in the tree.

**Not done, and why.** Nothing in the ticket is outstanding. One thing worth the PM's eye that is
not mine to fix here: `pnpm screenshots` was **red on unmodified main** at `07-log-prompts-chat` —
the page shows the gate, accepts, and lands in a chat whose `/me` no longer reports `log_prompts`
(its meters also show another session's usage). Reproduced twice against `origin/main`'s sources
with this branch's work set aside. It went green once the 039 section was placed before the 007
block, which means the dev harness's shared browser context and its per-page `?fake` state are
order-sensitive — not that anything was fixed. Worth its own ticket; the suite is green as committed.
