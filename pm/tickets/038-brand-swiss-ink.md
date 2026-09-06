---
id: 038
title: Brand — implement the "Swiss ink" direction across the web app, cards, icons and README
kind: normal (user-facing; docs/copy for the README header)
size: 5
status: dispatched
updated: 2026-09-06
release: demo-1
---

# 038 — Swiss ink

**Why.** Founder ruling 2026-09-06 (pm/LAUNCH.md F8): "I am picking the design of option 2 Swiss ink",
with one adjustment: the mascot should be "a bit more cute and a little bit less ghostly — right now the
slit eyes read like a ghost". The direction is the file the founder chose, `docs/brand/swiss-ink.html`
(+ `.md`: palette with contrast ratios, type stack, rules). The mark is being revised in a separate
contest; this ticket implements everything else and takes the mark as a drop-in SVG.

**Frozen decisions.**
- **Palette, exactly as `docs/brand/swiss-ink.md`**: light = paper `#FFFFFF`, surface `#F1F2F4`, surface-2
  `#E7E9ED`, text `#0A0A0A`, muted `#5C6068`, border `#D8DBE0`, accent cobalt `#1F3BFF` (fill and text on
  white), success `#0B7A4B`, warning `#E5E80B` (fill only, ink on it), danger `#D5182F`, code `#EDEFF2`;
  dark = true black `#000000`, surface `#101114`, surface-2 `#1A1C21`, text `#FFFFFF`, muted `#9AA0AA`,
  border `#2A2D33`, accent fill `#1F3BFF` with **accent-as-text `#7E93FF`** (flat cobalt never carries
  text on black), success `#35D48D`, warning `#F2F52E` (fill only), danger `#FF5266`, code `#16181D`.
  Every colour lives in one token file; nothing in a component names a hex.
- **Type**: Archivo (headings, UI, body) + IBM Plex Mono (invite codes, latencies, token counts,
  versions, anything measured or machine-issued). Both are SIL OFL. **Self-hosted**: woff2 files in
  `web/public/fonts/` with `@font-face`, `font-display: swap`, real fallback stacks; **no request to
  fonts.googleapis.com or any third party from the app** (Protection 3: a friend's IP goes to the host
  and the relay, nobody else). Add both licences to THIRD_PARTY_NOTICES via `hack/notices.sh` (extend the
  script's inventory if it only knows Go and npm; the OFL text must be verbatim).
- **Idiom**: zero border radius everywhere; hairline rules (`border`) as the separator, ink rules for
  frames; meters are 3 px ink rules, not gauges; the connect card leads with the tagline as the H1 and
  the description under it, as in the mock; mono only for measured things; the cobalt Connect/Send;
  focus rings visible in both schemes. The mock's information hierarchy is already the app's; do not
  move, add or remove any element, state or copy.
- **Assets**: `make brand` regenerates favicon/apple-touch/icons/maskable/og/social-preview from
  `web/public/favicon.svg` + `product.ts`; restyle the OG and social cards to the Swiss idiom (ink on
  paper, cobalt button, "Self-hosted · end-to-end encrypted · MIT" in mono). The mark: keep the current
  cat until the contest lands; the SVG is the only file the pick will change.
- **README**: the header strip as in the mock (mark + Infercat + tagline). No other copy changes.

**Promises.**
1. Both schemes implemented through tokens; the system three-state (`prefers-color-scheme` + explicit
   choice) keeps working as today; every text/ground pair in `docs/brand/swiss-ink.md` meets AA (the
   launch-check contrast step must pass).
2. Connect card, chat (header, path pill in all three states, meters, thinking disclosure, streaming
   cursor, footer metrics, composer with the Thinking toggle), sidebar/drawer, settings sheet, error and
   degraded states, the revoked/asleep/relay-unreachable walls: all in the new idiom, desktop and phone
   (360/390 px, no horizontal overflow).
3. Fonts self-hosted; zero third-party requests from the built app (verify with the network log in
   `launch-check`, which already inspects requests; add the assertion if it does not).
4. `make brand` + `make launch-check` + `pnpm screenshots` regenerated; `docs/media/friend-chat.gif|png`
   re-captured against a host from this branch on a free port (the laptop engine is at
   http://127.0.0.1:18080; never touch the founder's host on 9091 or its data dir).
5. Gate green: `make check`; `cd web && pnpm typecheck && pnpm test && pnpm lint`; `make wasm`;
   `make notices-check`; `make release-dry`; `make launch-check`.

**Size 5** (a stylesheet rewrite across every component; source lines are mostly CSS and tokens),
concept budget **0**. Tests: no new tests; the suite stays green; launch-check is the manual check.
Scope: `web/src/**` styles and tokens, `web/index.html` (font preload), `web/public/fonts/`,
`web/public/favicon.svg` only if the interim mark needs a fix to render in one colour, `web/dev/brand.mjs`
(or wherever `make brand` draws the cards), `hack/notices.sh` + `THIRD_PARTY_NOTICES.md`, `README.md`
header only, `docs/DESIGN.md` §visual (one short paragraph naming the direction and the token file).
Non-goals: any layout/IA/copy change, the mark itself, dark-first defaults, animation beyond what exists.

**Evidence of completion:** branch `t038-brand-swiss-ink` pushed with one commit, the gate lines in the
log, the regenerated media committed, and a before/after pair of `30-connect-desktop.png` and
`30-chat-phone.png` named in the log. Do not merge; the PM lands.

## Log
