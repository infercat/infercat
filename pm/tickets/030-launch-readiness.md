---
id: 030
title: Launch readiness — every public surface looks finished: web app, README, GitHub metadata, binaries, release
kind: normal
size: 3
status: dispatched
updated: 2026-09-03
release: demo-1
---

# 030 — Launch readiness (everything except the name)

**Why.** Founder (2026-09-03 02:30): other than the name, make everything involved in the launch look
launch-ready — the web app, the GitHub README, the GitHub metadata, all the binaries. This is the last
engineering gate before the post. Constraint: the product name lives in `internal/product/product.go`
and `web/src/product.ts` plus the invite prefix; do the work with the working name in place so the
rename is a one-hour substitution, and list every name-bearing surface in the report so nothing is
missed on rename day.

**Promises — judged like a stranger who arrived from a social post, on each surface.**

1. **Web app.** A real `<title>`, description meta, Open Graph and Twitter card tags with a generated
   preview image (the connect screen, rendered as a PNG at build time or a static asset — no external
   requests); a favicon set (SVG + PNG + apple-touch-icon); a `manifest.webmanifest` so "Add to Home
   Screen" on a phone gives an icon and a name; correct theme-color; no console warnings on load;
   Lighthouse (or equivalent) sanity: accessible names on every control, contrast, focus order; a
   footer/About with version, "MIT · source on GitHub", and the privacy sentence; the connect screen
   explains the product in one sentence and looks finished on desktop and at 390 px (re-verify).
   The wasm loads with a visible progress state and the page is usable before it loads.

2. **README** as the landing page for the repository (many will read it before the web app): what it is
   in one paragraph; a screenshot or short GIF of a friend chatting (recorded from the real app);
   Quickstart (host) with the Homebrew command first and the binary download second, then Quickstart
   (friend); what friends can reach (the access sentence); privacy (counts, never text; relay sees
   ciphertext; direct when possible); limits per friend; the `connect` command if 026 landed; FAQ
   (does the host need an account? no; does the friend? no; what touches your servers? the relay
   only, ciphertext, when direct fails — and registration once 025 lands); Building; License; a
   "Status: beta" line that is honest about known limitations (link `docs/LIMITS.md` from 028).
   Badges only for things that are true (license, release version, CI). No marketing adjectives.

3. **GitHub metadata.** Repository description (one line, the job); homepage URL (the web app URL when
   F3 lands — leave a TODO the rename ticket fills); topics (self-hosted, llm, llama-cpp, vllm, ollama,
   openai-api, wireguard, tailscale, p2p, local-ai — choose what is true); social preview image
   (1280×640, same design as the OG image); `SECURITY.md` (how to report, what is in scope — the
   Protections), `CONTRIBUTING.md` (short: issues welcome, PRs by discussion first, the aipm ticket
   discipline is internal), `CODE_OF_CONDUCT.md` (Contributor Covenant), issue templates (bug, host
   setup problem, friend cannot connect — each asking for `status` output), a PR template; CI on
   push/PR running `make check`, web checks, and `make release-dry` from a fresh checkout (law 4).
   `.github/workflows/release.yml` on tag: goreleaser (F4 publishing is the founder's; the workflow
   exists and is exercised with `--snapshot` until the founder tags).

4. **Binaries.** `bunny-network version` prints name, version, commit, date; `--help` on every command
   reads as a finished product (the strangers praised it — keep it, fix only what changed since);
   macOS binaries are signed and notarized if the founder's Developer ID is available via the existing
   BunnyKit signing setup (check `bunny-kit` docs; if not available tonight, document the
   right-click-Open path and ticket notarization); Linux and Windows archives include README, LICENSE,
   THIRD_PARTY_NOTICES; the checksums file is referenced in the README with the verify command; the
   web zip is self-serving (index.html at root, wasm included, README inside).

5. **Release.** A CHANGELOG.md seeded with the `v0.1.0` entry (what ships, known limitations,
   thanks to tailcat/Tailscale); `make release-dry` from a fresh clone produces every artifact with
   the new files inside; a dry run of the Homebrew formula (`brews` section with `skip_upload: true`)
   generates a formula that `brew install --build-from-source ./formula.rb` accepts locally.

6. **Name-bearing surfaces inventory** (report): every file/string the rename touches — product
   constants, invite prefix, binary name, archive names, brew formula name, web title/manifest/OG,
   README, repo description/topics, CHANGELOG, the relay comment on Cloudflare — with the exact
   command or edit for each, so rename day is mechanical.

7. **Evidence.** Screenshots of the web app's OG preview, home-screen icon on a phone, the README
   rendered on GitHub (private repo is fine), `gh repo view` output, the CI run green, `make release-dry`
   listing, the formula dry-run. Checks printed.

**Size 3** (≤900 lines of product change; docs/templates/CI YAML are surfaces, not source). Concept
budget 0. **Normal**; the founder is the reviewer of taste on this one.

**Scope.** `web/**` (meta, manifest, icons, about/footer — not the chat logic), `README.md`,
`CHANGELOG.md`, `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `.github/**`,
`.goreleaser.yaml` (brews with skip_upload, extra files), `Makefile`, `cmd/bunny-network` (`--help`
text only), `docs/`. GitHub metadata changes via `gh` on the private repo are allowed (description,
topics, social preview); **do not make the repo public, do not tag, do not publish anything** — F4/F6
are the founder's and the PM's on rename day.

## Log

- **2026-09-03 03:21 EDT** — ACK. Base `3eb9033` (= origin/main; the worktree sat one commit behind and
  was fast-forwarded), lane `t030-launch`. Read BELIEFS, LAUNCH, this ticket, README, `.goreleaser.yaml`,
  `hack/notices.sh`, Makefile, `web/index.html`, both product constants, MARKET-LMLINK, NAME, and the
  017/009 reports. Environment facts: `security find-identity -v -p codesigning` lists **one** identity,
  `Apple Development: Yuanping Song (R32T2KFMTK)` — no `Developer ID Application`, and neither
  `bunny-kit/docs` nor `bunny-screenshot/docs` documents a notarytool profile (bunny-screenshot's ADR-0012
  uses ad-hoc `codesign --sign -`; its LAUNCH_CHECKLIST lists notarization as future). So: no signing
  tonight; README documents the right-click-Open path; ticket 034 drafted. `gh repo view`: description,
  homepage, and topics all empty; `.github/` absent. Playwright 1.56 (web devDependency) launches
  Chromium 151 headless; Playwright's bundled ffmpeg will make the GIF. Founder's host (pid 13765 on
  :9090, pid 43556 on :9091) and the shared llama-server on :18080 are left alone; my host uses
  `tmp/bn030-data`, ports 6830–6839.
- **2026-09-03 03:38 EDT** — Web surfaces done and checked. `index.html` carries title, description,
  OG/Twitter metas, two `theme-color`s, the icon set and the manifest link; all filled from
  `src/product.ts` by the vite plugin, which now also emits `manifest.webmanifest` (build) and serves
  it (dev) — no second copy of the name. `web/dev/brand.mjs` renders the five PNG icons from
  `favicon.svg` and the OG/social-preview cards from the name, the sentence and a screenshot of the
  real connect card (`make brand`). `web/dev/launch-check.mjs` (`make launch-check`) checks the served
  head, the manifest and every asset, console warnings, accessible names (aria snapshot), contrast
  against the real ground (WCAG AA), Tab order and 390 px overflow, and with `INVITE`/`APP` records
  the friend's chat against a real host. First run found the class defect: `--faint` was 2.87:1
  light / 3.85:1 dark on every hint, meter label and the privacy line → two token values changed
  (`#716c65` / `#8e8981`), now 4.74:1 at the lowest across connect and chat, light and dark. Second
  run: OK end to end; GIF 1.1 MB, 12 s, from my own host (`bn030-data`, :6830) through the New York
  relay. Composer textarea got an `aria-label` (its only name was the placeholder). goreleaser 2.18
  deprecates `brews` (`goreleaser check` fails on it) → `homebrew_casks`, with the documented
  post-install quarantine hook because the binaries are unsigned. Go: 247 passed / 0 failed / 2
  skipped (the two opt-in live probes); web: 239 passed, typecheck and lint clean.

## Report

Everything below is from the code on this branch as it is; the name is still the working name and
every place it lives is in `docs/RENAME.md`. Two things the ticket assumed turned out not to exist
and are declared in **Declared**: a Developer ID, and an API for GitHub's social preview.

### The core, in one minute

**A stranger's browser, checked, not eyeballed.** `make launch-check` (new, `web/dev/launch-check.mjs`)
serves the built app, reads its head, fetches every asset, and asks the accessibility tree — then, with
an invite, connects to a real host and records the chat. Against my own host (`bn030-data`, :6830, the
shared llama-server, the New York relay):

```
connect: title, description, OG/Twitter metas, theme-color, manifest "Bunny Network" (3 icons), 7 assets served
30-connect-desktop: 3 controls in the accessibility tree, 0 unnamed · 6 text runs, lowest 5.08:1 — AA met
30-connect-desktop: Tab → textarea[Invite code] → a[Source on GitHub]      (same for dark and 390 px; no overflow)
chat: connected in 0.9 s (relayed via New York · 70 ms)
chat: 12 controls in the accessibility tree, 0 unnamed · 40 text runs, lowest 4.78:1 (button.ghost) — AA met
chat-phone: 13 controls, 0 unnamed · no horizontal overflow at 390px · chat-dark: lowest 4.74:1 — AA met
docs/media/friend-chat.gif  1078 KB          launch-check: OK
```

The first run was not OK: `--faint` — every hint, meter label, step, and the privacy line — was 2.87:1
in light and 3.85:1 in dark. One class, two token values (`web/src/styles.css`), and the lowest run in
the whole app is now 4.74:1. The composer textarea had only its placeholder for a name; it has
`aria-label="Message"`. No console warning or error on any load.

**The name lives in one place, still.** `web/index.html` carries `<title>`, description, Open Graph,
Twitter card, two `theme-color`s, the icon set and the manifest link — as `%PRODUCT_NAME%`,
`%PRODUCT_DESCRIPTION%`, `%WEB_URL%`. The vite plugin fills them from `src/product.ts` and now emits
`manifest.webmanifest` at build (and serves it in dev) from the same constant. `VITE_WEB_URL` comes from
`product.WebURL` via the Makefile, so the social-card image URL turns absolute the day F3 is set.

**Rasters are rendered, never drawn.** `make brand` (`web/dev/brand.mjs`) makes the five PNG icons from
`favicon.svg` and the two cards from the name, the sentence and a screenshot of the real connect card:
`web/public/og.png` (1200×630) and `.github/social-preview.png` (1280×640). Rename = re-run.

**README as the landing page.** One paragraph, the recording, three lines on how it works, host
quickstart (Homebrew → download + checksum verify → source), the unsigned-macOS path, friend
quickstart, the access sentence, privacy in three bullets, limits with the defaults, FAQ (accounts: no
and no; what touches our servers: the relay, ciphertext), Status: beta with the limitations spelled
out, data directory, building, licence. Badges: licence and CI only — there is no release yet, so no
release badge (docs/RENAME.md lists it for the first tag).

**Release path, exercised without releasing.** `.goreleaser.yaml` gains `release` (draft, on a tag
only) and `homebrew_casks` with `skip_upload: true`. `make release-dry` writes
`dist/homebrew/Casks/bunny-network.rb`; installed from a throwaway local tap (Homebrew refuses casks
outside a tap now) with the URLs rewritten to `file://` the same archives:

```
==> Linking Binary 'bunny-network' to '/opt/homebrew/bin/bunny-network'
🍺  bunny-network was successfully installed!
$ bunny-network version
Bunny Network 0.0.1-dev (3eb9033618ddf98c54ce2f2dcab5912aa6f7d64e, 2026-09-03T07:15:41Z)
xattrs on the staged binary: [com.apple.provenance ]        ← no quarantine: the postflight hook ran
==> Purging files for version 0.0.1-dev of Cask bunny-network · Untapped 1 cask
```

**CI, from a fresh checkout on ubuntu** (`.github/workflows/ci.yml`: build/vet/test, web checks,
notices, `make release-dry`, checksums verified, artifacts uploaded): {{CI}}
`release.yml` runs the same as a snapshot on `workflow_dispatch` and publishes only on a `v*` tag.

**GitHub metadata** (`gh repo view --json description,repositoryTopics`):

```
description: Share the model on your machine with friends: one binary in front of llama.cpp, vLLM,
             Ollama or LM Studio; one invite code; they chat from a browser. Self-hosted,
             end-to-end encrypted, no accounts.
topics:      go, llama-cpp, llm, lm-studio, local-llm, ollama, openai-api, react, self-hosted,
             tailscale, vllm, webassembly, wireguard        homepageUrl: "" (F3)
```

`SECURITY.md` (the four Protections as scope), `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md` (Contributor
Covenant 2.1), three issue forms that each ask for `status` and `version`, a PR template.

**Fresh clone of this branch** (`tmp/bn030-clone`, HEAD of the WIP commit; the freeze commit is docs-only): `make check` → `CHECK OK`, **247 passed / 0 failed / 2 skipped** (the two opt-in live probes); web: `pnpm typecheck` clean, **239 passed / 0 failed / 0 skipped** in 10 files, `pnpm lint` clean (after the fix below), `pnpm build` 669 ms; `make notices-check` → `notices: OK — 40 Go + 110 npm dependencies`; `make release-dry` → 6 archives + checksums, all six `OK` under `shasum -c`; the linux archive holds `LICENSE README.md THIRD_PARTY_NOTICES.md bunny-network`; `web-0.0.1-dev.zip` holds `index.html manifest.webmanifest og.png icon-192.png apple-touch-icon.png bunny.wasm README.md LICENSE THIRD_PARTY_NOTICES.md` at its root; `dist/cli_darwin_arm64_v8.0/bunny-network version` → `Bunny Network 0.0.1-dev (3733111…, 2026-09-03T07:42:56Z)`; the cask is written to `dist/homebrew/Casks/`. The one red line in that log — `pnpm lint` exit 1 — was `launch-check.mjs` using `getComputedStyle`/`NodeFilter` bare inside `page.evaluate` against the hand-listed eslint globals; qualified with `window.`, lint clean, re-verified below.

### Evidence (paths)

- Recording and stills: `docs/media/friend-chat.gif` (12 s, 720 px, 1.05 MB), `docs/media/friend-chat.png`.
- `web/dev/screenshots/30-connect-{desktop,dark,phone}.png`, `30-chat-{desktop,phone,dark}.png`,
  `30-og-preview.png` (the served og.png inside a link-preview card), `30-home-screen-icon.png`
  (**a mock**: the served apple-touch-icon in a home-screen tile — no phone was involved),
  `30-readme-rendered.png` (the README through GitHub's Markdown API, `gh api /markdown` gfm mode,
  styled locally — see Declared for why not a browser shot).
- `web/public/og.png`, `.github/social-preview.png`, the icon set in `web/public/`.
- Fresh-clone log: `/Users/yuanpingsong/.claude/jobs/12b4a99c/tmp/bn030-clone.log`; launch-check logs
  `bn030-check{1,2,3}.log`; release-dry `bn030-reldry.log`; CI run 33729356606.

### Rename inventory (promise 6) — `docs/RENAME.md`

Six sections in order: the two constants (everything derived follows: binary, archives, checksums
name, cask, title, metas, manifest); module path + repo rename (commands); copy that spells the name
(help constants, `bn1.` in tests and fixtures, the optional `BunnyTunnel`/`bunny.wasm` internals);
assets (`make brand`, `make launch-check`, the social preview by hand); documents and metadata
(README/CHANGELOG/docs, templates, goreleaser, `gh repo edit`, the tap); then the checks. The relay
carries no product name (nothing to do there).

### Edges, one line

Empty `WebURL` leaves `og:image` root-relative rather than broken; the manifest is emitted in build
and served in dev so `pnpm dev` has one too; `brand.mjs` and `launch-check.mjs` start their own
`vite preview` on 6832/6833 (or take `APP=`); the launch GIF is budget-checked (2 MB) by the script;
disabled controls are exempt from the contrast check (WCAG) and the check blends ancestor opacity
before measuring; Tab order is measured from the document top, not from the autofocused field.

### Declared

- **No Developer ID on this machine** — only `Apple Development: Yuanping Song (R32T2KFMTK)`; no
  notarytool profile documented in either sibling repo. Nothing was signed. README carries the
  right-click-Open / `xattr` path; the cask strips quarantine in its postflight (goreleaser's
  documented hook for unsigned binaries); `pm/tickets/034-notarization.md` drafted (size 1).
- **GitHub has no API for the social preview image**, and the browser on this laptop is not signed
  in to GitHub (the README URL returned 404 under "Sign in"); signing in is not mine to do. The image
  is at `.github/social-preview.png`; the upload is a one-click step in Settings → Social preview
  (RENAME.md §4). The same is why the README screenshot is a local render of GitHub's Markdown API
  output.
- **`brews` → `homebrew_casks`.** goreleaser 2.18 deprecates `brews` (and `goreleaser check` fails on
  it). The cask covers macOS and Linux; `brew install 2185Lab/tap/bunny-network` is the same command.
- **Production-touching, as granted:** `gh repo edit` set the description and 13 topics on the private
  repo. Two CI runs were triggered by pushing this branch. Nothing else left the machine: no tag, no
  release, no visibility change, no tap, nothing pushed anywhere but `t030-launch`.
- **Installed for the evidence, then removed:** the generated cask into a local tap `bn030/localtest`
  (uninstalled, untapped); `ffmpeg-static` into `tmp/ffm` (Playwright's bundled ffmpeg cannot write
  GIF); the throwaway host on :6830 and static server on :6831 were mine and are stopped at the end.
- **Not a bug:** in `30-readme-rendered.png` the CI badge shows as alt text — the repo is private, so
  shields cannot read the workflow status; it renders once the repo is public.
- **Not built:** the `connect` section (026 has not landed; the README FAQ names it as in progress
  with the ticket path); `docs/LIMITS.md` (028 has not landed; the Status line names it with a
  `TODO(028)` comment to turn into a link).
- **Two `TODO(rename day)` addresses** — `security@2185lab.com`, `conduct@2185lab.com` — must exist
  before the repo goes public.

### Adjacent, not fixed (candidates)

- The web `short_name` is the full product name (13 characters); iOS truncates labels around 12.
  Whatever the new name is, keep it short or add a `short_name` constant.
- `rootHelp`'s tagline says "share your local inference"; the audience says "local model"
  (docs/MARKET-LMLINK.md). Kept, per 009 ("keep the help"); one word for the rename-day pass.
- `web/dev/screenshots.mjs` (the 004–024 harness) and `launch-check.mjs` both start vite and watch
  the console; one shared helper would remove ~40 lines. Left alone: 031/032 may be in that file's
  neighbourhood.

## Freeze

- **Base commit:** `ebc1146` (= `origin/main` at freeze: 033 landed after dispatch, a ticket file only; rebased, no conflicts).
- **Lane:** worktree branch `t030-launch`, pushed. Not merged.
- **Product diff SHA-256** (`git diff origin/main -- . ':!pm' | shasum -a 256`, before this Report was appended): `fb71f341852f30be85f703297bb48f25c65ee241e5f19637f5158f192799427f`
- **Accounting** (recomputed from `git diff origin/main --numstat`):

| Bucket | Measured | Budget | Verdict |
|---|---|---|---|
| Product change (web meta/manifest/icons/About/styles/plugin + `main.go` help + `package.json` scripts) | +98 / −18 | ≤900 | met |
| Dev tooling, declared separately (`web/dev/brand.mjs` 145, `web/dev/launch-check.mjs` 398) | +543 | — (harness, like `screenshots.mjs`) | declared |
| Surfaces: README, CHANGELOG, SECURITY, CONTRIBUTING, CODE_OF_CONDUCT, docs/RELEASE, docs/RENAME, `.github/**`, `.goreleaser.yaml`, Makefile, ticket 034 | +978 / −43 | surfaces | — |
| Binary assets | 17 files (icons, og/social cards, 9 screenshots, GIF + PNG) | — | — |
| Concepts | **0** — no CLI verb, flag, error code, config key or state file. `VITE_WEB_URL` is a build-time env like `VITE_APP_VERSION`; `make brand`/`launch-check`/`release-publish` are make targets; `DESCRIPTION`/`SOURCE_URL` are TS constants beside `PRODUCT_NAME` | 0 | met |

- **Checks at freeze:** the fresh-clone block above, and CI run 33729356606.
