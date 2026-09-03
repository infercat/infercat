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

## Report
