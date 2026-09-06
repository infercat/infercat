---
id: 037
title: Rename day — the product is Infercat (founder decision 2026-09-05)
kind: docs/copy + normal
size: 3
status: dispatched
updated: 2026-09-05
release: demo-1
---

# 037 — Rename to Infercat

**Why.** The founder chose the name (2026-09-05: "let's proceed with infercat"), closing pm/LAUNCH.md
F1. docs/RENAME.md is the inventory of every surface the working name touches; this ticket executes
it. Nothing else in the launch waits on anything but this and the founder's remaining decisions.

**Frozen decisions (do not re-open).**
- Display name **Infercat**; CLI and package **`infercat`**; invite prefix **`ic1`** (invites read
  `ic1.<address>.<secret>`); Go module **`github.com/2185Lab/infercat`**; repo path
  `github.com/2185Lab/infercat` everywhere a URL is written (the PM renames the GitHub repo at
  landing; GitHub redirects the old path; a later move to an `infercat` org is the founder's call).
- `product.WebURL` stays `""` (no domain yet; the founder registers one). `SOURCE_URL` → the new repo path.
- Homebrew references → `2185Lab/tap/infercat` (the tap itself is created later, docs/RELEASE.md).
- The mark becomes a **cat** in the same idiom as today's rabbit: two triangular ears and a round
  face, same `.m` class, same colours, same dark-scheme `<style>`, drawn to read at 16 px. Nothing
  else changes in the SVG's structure. Rasters and cards are regenerated with `make brand`.
- README and the web About line say, once, near the tailcat mention: "Built on tailcat, Tailscale's
  open-source library. Infercat is not affiliated with or endorsed by Tailscale Inc." (BSD-3 clause 3
  and the audience's predicted first question.) The name never appears next to "Inferact" anywhere.
- README footer gains one line: "Made by [2185 Lab](https://2185lab.com). MIT."
- The wasm internals are renamed too: `bunny.wasm` → `infercat.wasm`, `window.BunnyTunnel` →
  `window.InfercatTunnel` (web/wasm/main_js.go, web/wasm/build.sh, web/src/transport/*, web/dev/*,
  .gitignore). After this, `git grep -n -i 'bunny\|bn1'` must return only pm/, docs/NAME.md,
  docs/MARKET-LMLINK.md, CHANGELOG history lines, and the Bunny Screenshot mention in pm/ if any.

**Promises.**
1. Steps 1–3 and 5 of docs/RENAME.md are done in full: constants, module path, `cmd/infercat`,
   Makefile/goreleaser/hack paths, every help text and example, every `bn1.` fixture and test, the
   ARCHITECTURE §Invite format line, README, CHANGELOG, docs/RELEASE.md, docs/MEASURE.md, docs/DESIGN.md,
   CONTRIBUTING.md, SECURITY.md, issue templates, `.goreleaser.yaml` (`release.github.name`, cask
   name/homepage/description), `web/package.json` name. Delete the `TODO(rename day)` comments whose
   condition is now met; keep the two that wait on an address or the tap, reworded to name what they wait on.
2. Step 4: the cat mark in `web/public/favicon.svg`; `make brand` regenerates favicon.png, apple-touch-icon,
   icon-192/512/maskable, og.png and .github/social-preview.png. Look at og.png and the 32 px favicon
   render and say in the log what you saw.
3. Media: `docs/media/friend-chat.gif|png` and `web/dev/screenshots/30-*.png` show the name in the
   header. Re-capture them with `make launch-check` per the script's header, against a host you start
   from this branch on a free port (the laptop engine is up at http://127.0.0.1:18080; do NOT touch
   the founder's running host on 9091 or its data dir) with a fresh `ic1` invite. If the capture is
   not feasible within budget, say exactly why and leave the old files; the PM will redo them.
4. Gate, all green from the worktree: `make check`; `cd web && pnpm typecheck && pnpm test && pnpm lint`;
   `make wasm`; `make brand`; `make notices-check`; `make release-dry` (archive names carry `infercat`).
   Paste the one-line results in the log.
5. The data-dir default follows `CLIName` (verify in internal/tunnel or wherever it lives); note in
   the log the old and new default paths so the README's upgrade note is true.

**Size 3**, concept budget **0** (nothing new: same states, same codes, same flags). Kind docs/copy +
normal. Tests: **no new tests**; fixtures follow the prefix; the suite stays green. Scope: repo-wide
per the inventory, **except** pm/ (records; leave every file), docs/NAME.md and docs/MARKET-LMLINK.md
(history), docs/RENAME.md (rewrite its header to say "done 2026-09-05 as ticket 037" and leave the
body as the record). Non-goals: creating the GitHub org or the tap, registering domains, the history
rewrite (F6), notarization (034), any copy change beyond the name, the disclaimer and the footer.

**Evidence of completion:** the branch `t037-rename-infercat` pushed with one commit "rename: Infercat
(037)", the gate lines in the log, the `git grep` remainder listed, and the new og.png/screenshots
committed. Do not merge; the PM lands.

## Log
