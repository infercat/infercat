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

### 2026-09-06 — engineer, branch `t038-brand-swiss-ink` (one commit, pushed)

**What changed (files).**

- **`web/src/styles.css` — rewritten (405 lines changed).** The whole palette is now one token block
  at the top plus one `prefers-color-scheme: dark` block, values exactly as `docs/brand/swiss-ink.md`:
  `--paper --surface --surface-2 --text --muted --border --rule --accent --accent-ink --accent-text
  --focus --success --warning --danger --code --scrim`. `--faint` is gone (the Swiss palette has one
  muted, and it is AA on paper, surface and surface-2); `--radius` is gone (nothing is rounded);
  `--accent-soft` is gone (no tint washes — the surfaces that used it are `--surface` behind a 3 px
  ink rule, or the danger rule when they are faults). The **syntax highlighting** was five hardcoded
  hexes; it is now five `--syn-*` tokens drawn from the palette's own colours, so no component and no
  stylesheet rule names a hex any more. Idiom: zero border radius everywhere; hairline `--border` for
  separators and ink `--rule` for frames (sidebar edge, topbar, composer, inputs, sheet); meters are
  3 px ink rules on a `--surface-2` track (`--text` fill, not the accent); the path line is a
  rectangular hairline pill with a 7 px state square; cobalt only on Connect/Send/Done, links, the
  focus ring and the relayed dot; focus rings are cobalt on paper and `#7E93FF` on black, because flat
  cobalt is 3.1:1 there. Mono (IBM Plex Mono) is on exactly the measured and machine-issued things —
  invite code and its field label, all field labels, model id, path pill, meters, reply timings, the
  composer hint, `Not delivered`, the thinking line, step details — and on nothing that is prose.
- **`web/public/fonts/` (new).** `archivo-latin-var.woff2` (35 KB, one variable file, wght 400–700,
  latin), `ibm-plex-mono-400-latin.woff2` and `-500-` (10 KB each), plus `LICENSE-Archivo.txt` and
  `LICENSE-IBMPlexMono.txt` — the OFL travels with the fonts, and `public/` ships verbatim into
  `dist/`. 55 KB of type in total. Taken from the Google Fonts CSS API with a desktop UA (latin
  `@font-face` blocks only), never requested at runtime.
- **`web/index.html`.** `theme-color` → `#ffffff` / `#000000`; three `rel="preload" as="font"
  crossorigin` links, because an `@font-face` inside the CSS bundle is discovered a round trip late.
- **`web/src/ui/Chat.tsx` (+8 lines).** One derived modifier on the existing path element —
  `pathState(live)` → `direct | relayed | reconnecting` — so the stylesheet can colour the pill's dot.
  No element, state or copy added; the three states are the three `pathLine` already describes.
- **`web/vite.config.ts`.** The manifest's `background_color`/`theme_color` were `#fdfcfb`; now
  `#ffffff`. (Two palette literals outside the token file; leaving them would have shipped the old
  brand's cream as the PWA splash.)
- **`web/public/favicon.svg`.** Geometry untouched (the interim cat; the mark contest owns it). Only
  the two fills: `#b0522b`/`#e9926a` → `#0A0A0A`/`#FFFFFF`. It renders in one colour, so it did not
  need a fix — but it named the old accent, and the whole icon set is rendered from it.
- **`web/dev/brand.mjs`.** The OG and social cards restyled to the idiom: paper ground, ink 92 px
  Archivo 700 wordmark tracked −0.045 em, the sentence in muted Archivo, a 2 px ink rule over
  `Self-hosted · end-to-end encrypted · MIT` set in mono, and the real connect screen in a 1 px ink
  frame with no radius and no shadow. Both faces are inlined as base64 `@font-face`, so the
  composition still makes no network request. The card's field is now filled with a well-formed
  (invented) invite and the field blurred before the shot, so the card shows the enabled **cobalt**
  Connect the brand calls for instead of a disabled button behind a focus ring.
- **`web/dev/launch-check.mjs` (+47).** Promise 3's assertion, which did not exist: `netWatch` records
  every request a page makes, `thirdParty` reduces them to off-origin origins, and `firstParty` turns
  any of them into a failure on all three connect screens (checked after `document.fonts.ready`, so a
  CDN font would be in the log). The chat run prints its off-origin hosts as evidence rather than
  asserting, because once connected the app is *meant* to reach the relay.
- **`hack/notices.sh` + `THIRD_PARTY_NOTICES.md`.** A third tree: `FONTS`, an inventory (family,
  upstream, the Google Fonts API revision, licence, licence file, woff2 files). `OFL-1.1` joins
  `ALLOWED`; the file gains a **Bundled fonts (web app)** table and the two OFL texts verbatim; and
  `check` fails if a declared woff2 or licence file is missing.
- **`README.md`.** The header strip: the mark floated left of the `Infercat` H1, the tagline under it,
  the MIT badge as flat-square cobalt `1F3BFF`, then a rule. Nothing else in the README changed. (The
  CI badge stays GitHub's own workflow badge — a shields.io version cannot read a private repo.)
- **`docs/DESIGN.md` §8 Visual system** — one paragraph naming the direction, the token file, the mono
  rule, the self-hosted faces, and the only two places a hex may still appear (brand.mjs, favicon.svg).
- **`web/eslint.config.js`** — `URL` added to the `.mjs` browser/node globals (lint blocker).
- Regenerated media: all 60 `web/dev/screenshots/*`, `web/public/{og,favicon,apple-touch-icon,icon-192,
  icon-512,icon-maskable-512}.png`, `.github/social-preview.png`, `docs/media/friend-chat.{gif,png}`,
  and `30-readme-rendered.png` (GitHub's Markdown API, styled locally, as ticket 037 made it).

**Gate lines, verbatim, all from this worktree after the last source change.**

- `make check` → `CHECK OK` (go vet clean; `go test ./...` 11 packages ok, 0 failures)
- `cd web && pnpm typecheck` → `$ tsc --noEmit` (no output, exit 0)
- `cd web && pnpm test` → `Test Files  10 passed (10)` · `Tests  257 passed (257)`
- `cd web && pnpm lint` → `$ eslint .` (no output, exit 0)
- `make wasm` → ` 26934131 web/public/infercat.wasm` · ` 6189920 web/public/infercat.wasm.gz`
- `make notices-check` → `notices: OK — 49 Go + 110 npm dependencies + 2 bundled fonts, licences all in ALLOWED, verbatim texts present`
- `make release-dry` → `• release succeeded after 4s`; dist: `infercat_0.0.1-dev_{darwin_arm64,darwin_amd64,linux_amd64,linux_arm64}.tar.gz`, `infercat_0.0.1-dev_windows_amd64.zip`, `infercat_0.0.1-dev_checksums.txt`, `web-0.0.1-dev.zip`, `homebrew/Casks/infercat.rb`
- `make brand` → `public/favicon.png 2 KB · apple-touch-icon.png 3 KB · icon-192.png 3 KB · icon-512.png 10 KB · icon-maskable-512.png 7 KB · og.png 83 KB · .github/social-preview.png 90 KB`
- `pnpm screenshots` → `60 screenshots in dev/screenshots/` · `No console errors, no page errors, no horizontal overflow at 360 px or 390 px.`
- `make launch-check` (with `INVITE=`, against a host from this branch) → `launch-check: OK`, and:
  - `connect: title, description, OG/Twitter metas, theme-color, manifest "Infercat" (3 icons), 7 assets served`
  - `30-connect-desktop: 6 text runs, lowest 6.31:1 (p.pitch) — AA met`
  - `30-connect-dark: 6 text runs, lowest 7.5:1 (a) — AA met`
  - `30-connect-phone: 6 text runs, lowest 6.31:1 (p.pitch) — AA met` · `no horizontal overflow at 390px`
  - `chat: connected in 0.8 s (relayed via New York · 70 ms)`
  - `chat (light): 47 text runs, lowest 5.63:1 (button.conv-del) — AA met`
  - `chat-phone: 43 text runs, lowest 5.63:1 (button.conv-del) — AA met` · `no horizontal overflow at 390px`
  - `chat-dark: 18 text runs, lowest 7.18:1 (button.conv-del) — AA met`
  - `docs/media/friend-chat.gif  865 KB` (under the 2 MB budget)

**The network-request assertion (promise 3), and what it says.**

- `30-connect-desktop: 6 requests, 0 to a third party — none, all same-origin`
- `30-connect-dark: 6 requests, 0 to a third party — none, all same-origin`
- `30-connect-phone: 6 requests, 0 to a third party — none, all same-origin`
- **Proved by failing.** With `<link href="https://fonts.googleapis.com/css2?family=Archivo">`
  injected into `web/dist/index.html`, the same run printed `30-connect-desktop: 7 requests, 1 to a
  third party — https://fonts.googleapis.com` and exited **1** with three problems. `dist/` was
  restored and every screenshot recaptured afterwards.
- The chat run's line, which is evidence and not an assertion:
  `chat: 14 requests, off-origin — https://tailcat.dev, https://tc301a.ipn.dev (the relay is the only
  one Protection 3 allows)`. Those two are the DERP map and the relay node — the host-and-relay path
  Protection 3 names. Pre-existing behaviour; this ticket added and removed no hop.
- `make notices-check` was also proved by failing: with `ibm-plex-mono-500-latin.woff2` moved aside it
  printed `notices: IBM Plex Mono declares web/public/fonts/ibm-plex-mono-500-latin.woff2, which is
  not there` and exited 1.

**Before / after screenshots (same file names; the "before" is the parent commit `dff93cc`).**

- `web/dev/screenshots/30-connect-desktop.png` — before: terracotta Connect on `#fdfcfb`, 10–12 px
  rounded corners, system font, "Invite code" in sentence case. After: white paper, the wordmark in
  Archivo 700 at 31 px tracked −0.035 em, `INVITE CODE` in tracked uppercase mono, a 1 px ink frame
  round the field, the cobalt Connect (pale here only because the field is empty and the button
  disabled — the same state as before), a hairline over the privacy paragraph, zero radius anywhere.
- `web/dev/screenshots/30-chat-phone.png` — before: rounded bubbles and a rounded composer on cream.
  After: the path as a hairline pill with a cobalt square, the three meters in mono wrapping to two
  lines under it, a square `--surface-2` bubble, `Thinking 198 words` in mono behind a 2 px ink rule,
  the reply's timing line in mono, an ink-framed composer and a cobalt Send. No horizontal overflow.
- `web/dev/screenshots/30-chat-dark.png` — before: `#121110` warm near-black with a terracotta Send.
  After: true `#000000`, a white hairline down the sidebar and across the topbar and composer, the
  cobalt pill dot, ink (white) meter fills, `#7E93FF` wherever cobalt would have had to carry text.
- `web/public/og.png` and `.github/social-preview.png` — before: cream ground, 54 px semibold name,
  the connect card in a 16 px-radius frame with a drop shadow, facts line in a faint system grey.
  After: paper, a 92 px Archivo 700 `Infercat`, the sentence in four muted lines, a 2 px ink rule over
  `Self-hosted · end-to-end encrypted · MIT` set in mono, and the real connect screen — now showing a
  filled invite field and the solid cobalt Connect — in a square 1 px ink frame with no shadow.
- Also re-looked at and correct: `13-chat-dark.png`, `06-chat-complete.png`, `08-settings.png`,
  `07-degraded-path.png` (the reconnecting pill: acid yellow square, ink-outlined, on an ink-bordered
  pill), `14-paused-banner.png`, `20-context-wall.png`, `22-phone-360-header.png`,
  `30-readme-rendered.png`.

**Two header defects found by looking, not by a test.**

1. The pill's chrome and mono's wider figures pushed the chat header onto two rows at 1280 px, with
   `Settings` alone on the second — a regression against `dff93cc`. Reclaimed by proportion, not by
   shrinking type below the brand's 10.5 px floor: sidebar 264 → 248 px (the mock's is 186 in a 1100 px
   sheet), topbar gap 16 → 14, `.truth` and `.meters` gaps tightened, meter track 108 → 96, pill
   padding 3/7 px. Measured, not eyeballed: with the longest real path string (`relayed via San
   Francisco · 85 ms`) the row now needs 971 px of 992 — 21 px of slack.
2. At the 880 px width the README recording is captured at, the third meter used to be **clipped by
   the window edge** — in `dff93cc` too. `.truth` and `.meters` now wrap within themselves at every
   width, so the numbers move to a second line instead of off the screen.

**Media capture (promise 4).** Host started from this branch, its own everything:
`./bin/infercat serve --upstream http://127.0.0.1:18080 --dev-listen 127.0.0.1:9094 --data-dir
<worktree>/hd038 --name "Max's laptop"`, minted `alice`, ran `INVITE=ic1.… make launch-check`, then
stopped it → `shutting down (up to 10s for in-flight requests)`, and deleted the data dir. The
founder's host on **9091 was never touched and was still listening before, during and after** (checked
with `lsof` at both ends). The data dir had to be named `hd038` rather than something readable: the
worktree path is long enough that `<data-dir>/admin.sock` hits the 103-byte unix socket limit, and the
CLI says so clearly.

**Left undone, and why.**

1. **The connect card does not lead with the tagline as its H1.** The frozen-decisions "Idiom" bullet
   asks for it, but the same ticket's Non-goals forbid "any layout/IA/copy change", the front matter
   scopes the copy change to "docs/copy for the README header", and the dispatch capped this at no
   copy or layout changes. "Give friends a key to the AI on your machine." exists nowhere in the app
   today (only in `docs/NAME.md` and the mock), so putting it on the card is new product copy, and
   moving `Infercat` out of the H1 into a new top strip is a new element. Everything else in that
   bullet is done — the H1 is the Archivo display headline, the description sits under it in muted
   Archivo, the field label is tracked mono, the privacy paragraph sits behind a hairline. **This is a
   one-line change if the PM rules the tagline in**; I did not want to make that ruling by writing it.
2. **The mock's dark OG card is not the default share card.** The mock shows dark as the default and
   light as a variant; the ticket's own words say "ink on paper, cobalt button", `brand.mjs`'s law is
   that the card carries a screenshot of the real connect screen and never a replica, and
   `index.html`'s `og:image:alt` describes that screenshot. Going dark-first meant either dropping the
   real screenshot or changing that alt text. The card is the light variant, restyled.
3. **No explicit light/dark control was added.** Promise 1 asks that "the system three-state
   (`prefers-color-scheme` + explicit choice) keeps working as today" — as of `dff93cc` there is no
   explicit choice anywhere in the app: the whole mechanism is `color-scheme: light dark` plus one
   `@media (prefers-color-scheme: dark)` block, and both are intact and now carry the new palette.
   Adding a control would have been new state. Flagging it in case the promise meant something else.
4. **The composer has no Thinking toggle.** Promise 2 lists one; the app's Thinking control lives in
   the Settings sheet (031) and is styled there. Moving or duplicating it is a layout and state change.
5. The two stale `web/public/bunny.wasm{,.gz}` files are untracked leftovers from before 037's rename
   and were left alone.

### 2026-09-06 — engineer, fixes after review (round 1), branch `t038-brand-swiss-ink`

Eleven findings, two high and nine medium. All eleven are fixed inside the frozen decisions — no new
element, state or copy except the streaming cursor promise 2 names and the mock defines. Every claim
below is measured against the built app or the committed file, not read off the source.

**[high] `.conv-del:hover` failed AA (styles.css:235).** `--surface-2` is the one ground in this
palette that drops `--danger` below 4.5:1, and the delete button was the only thing standing on it.
The hover ground is now `--surface`, the same ground the row under it already takes, so the colour
change from muted to danger stays the whole affordance. Measured with a real Playwright hover over
the element's box on the built bundle: **light `#D5182F` on `#F1F2F4` = 4.69:1** (was 4.32:1),
**dark `#FF5266` on `#101114` = 5.98:1** (was 5.40:1). Nothing else in the app uses `--surface-2` as
a ground for danger or success.

**[high] The degraded ("stale") header dropped three runs under AA (styles.css:281).** `opacity:
0.55` on `.truth` and `.who .dim` is gone. The truths now recede by dropping out of ink into the
muted grade instead of fading — `.main.stale .truth, .main.stale .who .dim { color: var(--muted) }`,
with the path pill's ink text, its state square and the meter fills following it — because the muted
grade is AA on every ground in this palette and a faded number is a number nobody can read. Driven
with the fake harness in the paused-invite state (`?fake&keyPaused`, which is `key !== 'active'` —
the same flag the revoked state sets), at 1280×800, 390×844 and 360×780, light and dark, walked with
launch-check's own contrast walker:

- `stale light 1280x800: 20 runs, lowest 5.25:1 (span.pending-mark) — AA met`
- `stale dark 1280x800: 20 runs, lowest 6.65:1 (span.pending-mark) — AA met`
- `stale light 390x844: 21 runs, lowest 5.25:1 (span.pending-mark) — AA met`
- `stale dark 360x780: 21 runs, lowest 6.65:1 (span.pending-mark) — AA met`

Before, in the same state: `span.meter-label` 2.39:1 light / 2.94:1 dark, `span.dim` 2.39:1,
`span.path` 4.4:1. The review is right that this was carried over from `dff93cc` (2.24:1 there) — it
is fixed now rather than carried further.

**[medium] The three transparent icons vanished on dark chrome (brand.mjs:56,58,59).** `favicon.png`,
`icon-192.png` and `icon-512.png` now render on `P.paper` like the other two, so all five rasters
carry their ground with them. The mark is one colour and in this palette that colour is ink; a PNG
cannot re-read the `prefers-color-scheme` query that `favicon.svg` still uses in a modern tab.
Committed files, PIL: corner pixel `(255,255,255,255)` and centre `(10,10,10,255)` on all three —
`favicon.png` 65.5 % paper / 31.3 % ink, `icon-192.png` 66.0 / 32.4, `icon-512.png` 66.5 / 32.9.
`apple-touch-icon.png` (78.2 % paper) and `icon-maskable-512.png` (87.9 %) are unchanged. On Chrome's
dark tab strip `#202124` the icon is now a paper square carrying the mark instead of 1.09:1 of
nothing. The SVG favicon still flips to white and is what a modern browser actually uses.

**[medium] The app had no streaming cursor (Message.tsx / promise 2).** Added, as the mock defines it
(`docs/brand/swiss-ink.html:174`): an 8×16 px cobalt block, blinking on a 1 s step, on the end of the
last line of the reply this tab is streaming. It is a `::after` on the reply's last block
(`.row.streaming > .md > :last-child::after`), so it sits inline after the final word rather than on
a line of its own, and it is on `live && m.content !== ''` only — never on a reply another tab is
writing, and never next to "Waiting for the first token…", which already says what is happening.
`@media (prefers-reduced-motion: reduce)` stops the blink and keeps the block. Visible in
`web/dev/screenshots/05-streaming-answer.png` (after "You are talking to a model running") and in the
re-recorded `docs/media/friend-chat.gif`.

**[medium] The stale `bunny.wasm` pair was tracked, not untracked — my deviation 5 was wrong.**
It was: `git ls-tree -l 25cbc09 web/public/` returns both blobs (26,933,534 and 6,188,895 bytes),
they were copied into `web/dist` and they shipped inside `web-0.0.1-dev.zip`. `git rm`'d. After
`make release-dry` the zip is **23 files / 33,968,107 bytes** and contains exactly `infercat.wasm`,
`infercat.wasm.gz` and `wasm_exec.js` — 33,122,429 bytes of duplicate dead payload gone from the
tree, from `web/dist` and from the release artifact. `.gitignore` already ignores the `infercat.*`
pair, so nothing regenerates them. (`pm/BELIEFS.md:144` still says `make wasm` builds
`web/public/bunny.wasm`; that is a PM file and 037's rename, so I have left it to you.)

**[medium] The connect card was not the mock's connect card (styles.css:150).** `.connect-card` is
now the ink rectangle the mock frames it in — `border: 1px solid var(--rule)` with the card's own
padding — and `.about` is a foot strip flush to that frame behind a hairline, as is `.devmode` when
dev mode is on. That is the frame and the footer strip; the **top strip and the tagline H1 are still
declared deviation 1** and still yours to rule on, because both are new elements or new copy. The OG
card's `.shot` lost its own border in the same change: the card carries the rectangle now, and two
frames would be two. Computed on the built app, both schemes: `.connect-card` border is
`1px solid rgb(10,10,10)` / `1px solid rgb(255,255,255)`, radius `0px`.

**[medium] The version string was in Archivo, not mono (styles.css:174).** The build line is now its
own `<span class="build">` inside `.about`, set in `var(--mono)` at 11 px, matching the mock's
connect-card foot; the tailcat attribution sentence under it is prose and stays in Archivo. The same
`build` class is on the settings sheet's `App version …` line. Walking every text node on the built
connect screen now reads `IBM Plex Mono 11px build … | Version` / `0.0.1-dev` / `Source on GitHub`.
`docs/DESIGN.md` §8 already named versions as mono; the app now matches it.

**[medium] The Temperature slider was the last rounded object and used two untokened greys
(styles.css:206).** `accent-color` is gone; the control is drawn from the tokens — `appearance:
none`, a 3 px `--surface-2` track (the same rule the meters are) and a square 9×17 px `--accent`
thumb, with the `-moz-` pair for Firefox. Pixel scan of the control at 3× in the settings sheet:
**light = paper 82.6 % + `rgb(231,233,237)` 15.4 % + `rgb(31,59,255)` 2.0 %**, **dark = black 82.6 %
+ `rgb(26,28,33)` 15.4 % + `rgb(31,59,255)` 2.0 %** — three tokens each, and different in each
scheme, where before it was `#EFEFEF`/`#B2B2B2` identically in both. A DOM sweep of every element in
the chat and the sheet in both schemes now returns `rounded elements: none`.

**[medium] The README mark flipped against the OS, not against GitHub's page theme (README.md:1).**
The header is now a `<picture>` with a `(prefers-color-scheme: dark)` `<source>` — the mechanism
GitHub honours against its own canvas — over two flat one-colour files, `docs/media/mark-ink.svg` and
`docs/media/mark-paper.svg`. Both are **generated by `make brand`** from `web/public/favicon.svg`
(the stylesheet flattened to one `fill`), so the mark contest still changes exactly one file.
Evidence: GitHub's Markdown API returns the block wrapped in its own
`<themed-picture data-catalyst-inline="true">`, which the previous `<img>` form did not get; each
source over its intended canvas is **19.80:1 on GitHub light and 18.92:1 on GitHub dark**, and the
pairings the `<picture>` prevents are the 1.05:1 and 1.00:1 the review measured; `float: left` still
resolves (`getComputedStyle(img).float === "left"`, box at x 53). `30-readme-rendered.png`
re-captured through the same pipeline 030/037/038 used.

**[medium] The one cobalt control rested as a pale periwinkle (styles.css:104).** `button:disabled {
opacity: 0.45 }` is gone. Disabled is now a state of the token system: `.primary:disabled` is
`--surface-2` behind a `--border` hairline with the `--muted` label, `.secondary:disabled` the same
hairline and label, and nothing composites. That takes the empty-field Connect and the empty-composer
Send from **white on `#9AA7FF` at 2.25:1 to `#5C6068` on `#E7E9ED` at 5.50:1** (dark: 3.10:1 →
`#9AA0AA` on `#1A1C21` at 6.48:1), and it means cobalt now appears only on a button that will do
something. Visible in `30-connect-desktop.png`, `30-chat-phone.png`, `08-settings.png` and the
re-captured `docs/media/friend-chat.png`. `brand.mjs` still fills the card's field before the OG
shot, which is now about showing a friend the screen they arrive at with a code in hand rather than
about dodging a pastel button.

**Gate lines, verbatim, all from this worktree after the last source change.**

- `make check` → `CHECK OK` (go vet clean; `go test ./...` 11 packages ok, 0 failures)
- `cd web && pnpm typecheck` → `$ tsc --noEmit` (no output, exit 0)
- `cd web && pnpm test` → `Test Files  10 passed (10)` · `Tests  257 passed (257)`
- `cd web && pnpm lint` → `$ eslint .` (no output, exit 0)
- `make wasm` → ` 26934031 web/public/infercat.wasm` · ` 6189810 web/public/infercat.wasm.gz`
- `make notices-check` → `notices: OK — 49 Go + 110 npm dependencies + 2 bundled fonts, licences all in ALLOWED, verbatim texts present`
- `make release-dry` → `• thanks for using GoReleaser!`; dist: `infercat_0.0.1-dev_{darwin_arm64,darwin_amd64,linux_amd64,linux_arm64}.tar.gz`, `infercat_0.0.1-dev_windows_amd64.zip`, `infercat_0.0.1-dev_checksums.txt`, `web-0.0.1-dev.zip` (23 files, no `bunny.wasm`), `homebrew/Casks/infercat.rb`
- `make brand` → `web/public/favicon.png 2 KB · apple-touch-icon.png 3 KB · icon-192.png 3 KB · icon-512.png 9 KB · icon-maskable-512.png 7 KB · docs/media/mark-ink.svg · docs/media/mark-paper.svg · web/public/og.png 84 KB · .github/social-preview.png 91 KB`
- `pnpm screenshots` → `60 screenshots in dev/screenshots/` · `No console errors, no page errors, no horizontal overflow at 360 px or 390 px.`
- `INVITE=ic1.… make launch-check` (against a host from this branch) → `launch-check: OK`, and:
  - `30-connect-desktop: 6 requests, 0 to a third party — none, all same-origin` (the same for dark and phone)
  - `30-connect-desktop: 7 text runs, lowest 6.31:1 (p.pitch) — AA met`
  - `30-connect-dark: 7 text runs, lowest 7.5:1 (a) — AA met`
  - `30-connect-phone: 7 text runs, lowest 6.31:1 (p.pitch) — AA met` · `no horizontal overflow at 390px`
  - `chat: connected in 0.8 s (relayed via New York · 72 ms)`
  - `chat (light): 44 text runs, lowest 5.63:1 (button.conv-del) — AA met`
  - `chat-phone: 49 text runs, lowest 5.63:1 (button.conv-del) — AA met` · `no horizontal overflow at 390px`
  - `chat-dark: 18 text runs, lowest 7.18:1 (button.conv-del) — AA met`
  - `chat: 14 requests, off-origin — https://tailcat.dev, https://tc301a.ipn.dev (the relay is the only one Protection 3 allows)`
  - `docs/media/friend-chat.gif  817 KB` (under the 2 MB budget)
- Off the gates, with the fake harness and launch-check's own walker — the states launch-check never
  visits: the four stale runs above, `conv-del hover light: lowest 4.69:1 — AA met`, `conv-del hover
  dark: lowest 5.98:1 — AA met`, `settings light: 32 runs, lowest 5.63:1 — AA met`, `settings dark:
  32 runs, lowest 6.71:1 — AA met`, and `rounded elements: none` in both schemes.

**Before / after (the "before" is `25cbc09`, this ticket's first commit).**

- `web/dev/screenshots/30-connect-desktop.png` — before: unframed text floating on bare paper, the
  version line in Archivo, a pale periwinkle Connect. After: the card is a 1 px ink rectangle, the
  build line is mono behind a hairline foot strip flush to the frame, and Connect rests as
  `--surface-2` behind a hairline until the field holds a well-formed code.
- `web/dev/screenshots/30-chat-phone.png` — before: a pale cobalt Send on an empty composer. After:
  the same grey-and-hairline disabled state; everything else in the header and the thread is
  unchanged.
- `web/dev/screenshots/05-streaming-answer.png` — the cursor, which was not there before.
- `web/dev/screenshots/08-settings.png` — the slider is a 3 px rule with a square cobalt thumb, and
  `App version 0.0.1-dev · MIT` is mono.
- `web/public/{favicon,icon-192,icon-512}.png` — an opaque paper ground.
- `web/public/og.png`, `.github/social-preview.png` — one frame instead of two: the card's own.
- `web/dev/screenshots/30-readme-rendered.png` — re-captured for the `<picture>` header.

**Production-touching actions, declared.** A host of my own from this branch
(`./bin/infercat serve --upstream http://127.0.0.1:18080 --dev-listen 127.0.0.1:9095 --data-dir
/tmp/hd038r1 --name "Max's laptop"`) and one `keys add alice` against it, for the media re-capture;
stopped afterwards (`shutting down (up to 10s for in-flight requests)`, port 9095 free) and the data
dir deleted. It lives in `/tmp` rather than the worktree only because `<data-dir>/admin.sock` would
pass the 103-byte unix socket limit under this worktree's path. The founder's host on **9091 was
never touched and was listening before and after** (`lsof` at both ends). One read-only
`api.github.com/markdown` call for the README render; nothing written to GitHub.

**Still open, unchanged from the first log.** Deviation 1 is now half done — the frame and the foot
strip are in; the tagline H1 and the top strip are still new copy and a new element, so still your
ruling — and deviations 2, 3 and 4 stand as written. Deviation 5 was wrong and is corrected above.

### 2026-09-06 — PM landing

Rulings on the engineer's declared deviations: (1) the tagline stays OUT of the connect card — it is
host-facing marketing; the friend who pastes a code needs the friend's sentence, which the card
already has; (2) the light share card with the real screenshot is right; (3)–(5) accepted as stated.
Verifiers (three lenses, two rounds, one fix round `2eb1db2`) left two regressions and three cosmetics;
PM fixes in this landing commit: the chat header below 1260 px is now two rows and never more (who +
Settings, then the truths full width — the phone rule generalised), `.connect-actions` wraps so a
two-button wall stacks instead of squeezing "Not now" into two lines at 390 px, and the share-card
screenshot is bounded to the canvas (`bottom: 8.5%`, `object-fit: cover`) so its frame no longer
runs off the bottom of `.github/social-preview.png`. Accepted to the backlog (012): meter values set
in ink weight 500 with muted names (needs a span in `Meters`); the paused-invite composer hint is a
prose sentence set in mono (needs a second hint class). Media regenerated after the fixes.
