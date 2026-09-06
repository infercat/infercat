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

### 2026-09-05 — engineer, branch `t037-rename-infercat` (one commit, pushed; the run crossed into 09-06)

**What was done.** The whole inventory, from the two constants outward. `internal/product/product.go`
(`Name = "Infercat"`, `CLIName = "infercat"`, `InvitePrefix = "ic1"`, `WebURL` untouched at `""`) and
`web/src/product.ts` (`PRODUCT_NAME`, `INVITE_PREFIX`, `SOURCE_URL = https://github.com/2185Lab/infercat`);
`go mod edit -module github.com/2185Lab/infercat` and every import; `git mv cmd/bunny-network cmd/infercat`;
Makefile / `.goreleaser.yaml` / `hack/notices.sh` paths; every help text, example, comment, error string
and `bn1.` fixture on both sides of the invite parser; `docs/ARCHITECTURE.md` §Invite format; README,
CHANGELOG, `docs/RELEASE.md`, `docs/MEASURE.md`, `docs/DESIGN.md`, CONTRIBUTING, SECURITY,
CODE_OF_CONDUCT, the four issue templates, `release.github.name`, the cask name/homepage/description,
`web/package.json` → `infercat-web`. The wasm internals too: `infercat.wasm`, `window.InfercatTunnel`,
`web/dev/fake-bunny-tunnel.ts` → `fake-infercat-tunnel.ts`, `BUNNY_TUNNEL_DATA_DIR` →
`INFERCAT_TUNNEL_DATA_DIR`, the `Host:` header the CLI and the wasm bridge send (`bunny` → `infercat`),
`__bunnyReady`, `bunny.local` in the fake backend. README gained the disclaimer and the footer;
`docs/RENAME.md`'s header now says done 2026-09-05 as ticket 037 and its body is left as the record.
100 files, +409/−390.

**Four things the rename forced, called out because they are behaviour, not spelling.**
1. `internal/invite.checkPrefix` and `web/src/invite.ts`'s `VERSION_TAG` hardcoded the prefix family
   `"bn"`. Left alone, an `ic2` invite would have said "not an Infercat invite" instead of "this invite
   needs a newer app". Both now derive the family from the constant (`strings.TrimRight(InvitePrefix,
   "0123456789")` / `INVITE_PREFIX.replace(/\d+$/, '')`); the existing table tests caught it (fixtures
   `prefix_bn_only`/`newer_bn2`/… are now `prefix_ic_only`/`newer_ic2`/…, per "fixtures follow the prefix").
2. `ErrPrefix` read "not a " + Name; with a vowel that is ungrammatical, so it is "not an Infercat
   invite (missing or wrong prefix)". Same in `web/src/api.ts`'s `not_found` copy, which now takes the
   name from `PRODUCT_NAME` instead of carrying a second literal.
3. `inviteFromHash` matched a literal `'bn1.'`; it now matches `` `${INVITE_PREFIX}.` `` — one less copy
   of the name to miss next time.
4. `web/dev/screenshots.mjs`'s bad-invite fixture was `bn9.…`, whose whole point is the "needs a newer
   app" error. `ic9.…` now, so that screenshot still shows what it was made to show.

**The mark.** `web/public/favicon.svg` keeps the idiom exactly — one `<g class="m">`, the same two
colours and the same dark-scheme `<style>` — with the rabbit's two rotated ellipses replaced by two
`<polygon>` ears on a `r=9.6` face circle. **og.png (1200×630):** the cat mark in the terracotta,
"Infercat" at 54 px, the one sentence, "Self-hosted · end-to-end encrypted · MIT", and the real connect
card on the right showing the `ic1.…` placeholder and the new About line. Nothing says Bunny anywhere.
**favicon.png** is 96 px (brand.mjs's size, not 32); downscaled to 32 px and to 16 px it still reads as a
cat: a solid round head with two pointed ears and a clear notch between them — no thin strokes to lose.

**Gates, all from this worktree.**
- `make check` → `CHECK OK` (go vet clean; `go test ./...` — 11 packages ok, 0 failures)
- `cd web && pnpm typecheck` → `$ tsc --noEmit` (no output, exit 0)
- `cd web && pnpm test` → `Test Files  10 passed (10)` · `Tests  257 passed (257)` (0 failed, 0 skipped)
- `cd web && pnpm lint` → `$ eslint .` (no output, exit 0)
- `make wasm` → `26934131 web/public/infercat.wasm` · `6189928 web/public/infercat.wasm.gz`
- `make brand` → `public/favicon.png 2 KB · apple-touch-icon.png 3 KB · icon-192.png 4 KB ·
  icon-512.png 11 KB · icon-maskable-512.png 7 KB · og.png 76 KB · .github/social-preview.png 81 KB`
- `make notices-check` → `notices: OK — 49 Go + 110 npm dependencies, licences all in ALLOWED, verbatim texts present`
- `make release-dry` → `release succeeded after 5s`; dist: `infercat_0.0.1-dev_darwin_arm64.tar.gz`,
  `infercat_0.0.1-dev_darwin_amd64.tar.gz`, `infercat_0.0.1-dev_linux_amd64.tar.gz`,
  `infercat_0.0.1-dev_linux_arm64.tar.gz`, `infercat_0.0.1-dev_windows_amd64.zip`,
  `infercat_0.0.1-dev_checksums.txt`, `web-0.0.1-dev.zip`, `homebrew/Casks/infercat.rb`

**Media (promise 3) — done, not skipped.** Host started from this branch: `./bin/infercat serve
--upstream http://127.0.0.1:18080 --dev-listen 127.0.0.1:9093 --data-dir <worktree>/host-data --name
"Max's laptop"` (its own identity, its own data dir outside the repo; the founder's host on 9091 and its
data dir were never touched — still listening). Minted `ic1.tco2FwWC…` for alice, then
`INVITE=ic1.… pnpm launch-check` → `launch-check: OK`, `chat: connected in 0.8 s (relayed via New York ·
72 ms)`, all contrast/accessible-name/tab-order/overflow checks AA. New files committed:
`docs/media/friend-chat.gif` (893 KB, under the 2 MB budget) and `friend-chat.png`, and
`web/dev/screenshots/30-{connect-desktop,connect-dark,connect-phone,og-preview,home-screen-icon,chat-desktop,chat-phone,chat-dark}.png`.
Host stopped afterwards (`shutting down (up to 10s for in-flight requests)`).

**Data-dir default (promise 5).** `cmd/infercat/main.go:resolveDataDir` → `filepath.Join(os.UserConfigDir(),
product.CLIName)`, so it moves with the constant and nothing else had to change.
- old: `~/Library/Application Support/bunny-network` (macOS) · `~/.config/bunny-network` (Linux) · `%AppData%\bunny-network` (Windows)
- new: `~/Library/Application Support/infercat` · `~/.config/infercat` · `%AppData%\infercat`
README's Data-directory section and `docs/ARCHITECTURE.md` now print the new paths. No migration note was
added: v0.1.0 is the first release, so the only host that predates the rename is the founder's, which keeps
its identity by starting once with `--data-dir <old path>` (or by moving the directory) — and its `bn1`
invites stop parsing regardless, so they must be re-minted. Say the word if you want that as a README line;
it would be a copy change beyond the name, the disclaimer and the footer.

**`git grep -n -i 'bunny|bn1'` remainder** — everything below is deliberate:
- `pm/**`, `docs/NAME.md`, `docs/MARKET-LMLINK.md` — records, untouched per scope.
- `docs/RENAME.md` body — the record, per scope; only its header was rewritten.
- `docs/MEASURE.md:6` — `~/Desktop/repos/2185Lab/bunny-kit/binaries/llama-server/…`. `bunny-kit` is a
  *different* repository of the lab's (the llama.cpp build the numbers were taken against). Renaming it
  would make the reproduction command false, so it stands.
- `hack/load/sample.go:147` — `top -bn1`, a flag of `top(1)` inside the relay sampling script.
- `web/pnpm-lock.yaml:306` — the letters `BN1` inside a base64 integrity hash.
- CHANGELOG has no remainder: the `version` line moved with the binary name and v0.1.0 is unreleased.

**Deliberately left alone (not caught by that grep, so flagging them here).**
- The browser's localStorage namespace `bn.*` (`bn.invite`, `bn.privateKey`, `bn.conversations.*`,
  `bn.lastHost`, `bn.settings`, `bn.me`) and the `bn.tunnel-identity` web lock, in `web/src/storage.ts`
  and `web/src/transport/index.ts`. Renaming these keys silently orphans every existing browser's saved
  chats, tunnel identity and remembered invite — that is a migration, with a concept in it, not a rename.
  A friend never sees the string. Worth its own ticket before launch if you want the namespace to match.
- Test temp-dir prefixes (`bn005-`, `bn009-`, `bn029-`, `bnadm`, `bn-launch-`): ticket-numbered scratch
  directories, invisible to anyone.

**`TODO(rename day)` comments.** Not one of the five had its condition met by the rename, so none was
deleted; each was reworded to name what it actually waits on and no longer claims to wait on a day that
has passed: `TODO(tap)` in README and `.github/workflows/release.yml`, `TODO(address)` in SECURITY.md and
CODE_OF_CONDUCT.md, `TODO(F3)` on the cask homepage (which still points at the repo, since `WebURL` is `""`).

**Not done, and why.** Everything GitHub-side is yours by the ticket's own non-goals: `gh repo rename`,
the description/homepage/topics, uploading `.github/social-preview.png`, creating `2185Lab/homebrew-tap`
and flipping `skip_upload`. Nothing was pushed to GitHub but this branch.

**Production-touching actions, declared.** (1) Installed `ffmpeg` on this machine with Homebrew: the
capture needs a full ffmpeg for the GIF and Playwright's own build cannot write one (launch-check.mjs's
header says exactly this). Without it the run dies after `friend-chat.png` and leaves the media set half
old, half new. (2) Ran a host of my own and one `keys add` against the laptop engine on :18080, as
instructed. Nothing else on this machine was written outside the worktree.

### 2026-09-06 — engineer, fixes after review (round 1)

**One defect, reported three times: `web/dev/screenshots/30-readme-rendered.png` was never
re-captured.** Correct, and my 09-05 media paragraph made it worse by listing the eight files
`make launch-check` writes as though they were the whole of promise 3. Nine `30-*.png` are tracked;
the ninth is not a launch-check output — ticket 030 made it by hand (030's log, line 221) and left no
producer in the repo, so `INVITE=… pnpm launch-check` could not have touched it and I never noticed it
standing still. It is also invisible to this ticket's own remainder gate: neither `git grep -i
'bunny|bn1'` nor a binary-aware `grep -rail bunny` reads anything inside a deflate-compressed PNG. The
committed image showed the old name in four places — the crumb `2185Lab / bunny-network · branch
t030-launch`, the H1 `Bunny Network (working name)`, and the in-app line `Bunny Network records
counts, never text.`

**Re-captured, not declared-and-skipped.** Same pipeline as 030, recovered from that run's own
artifacts (its `readme.html` survived), so the stylesheet and the 1100×900 geometry are the
original's:
1. `gh api /markdown` with `{text: README.md, mode: gfm, context: 2185Lab/bunny-network}` — the
   context repo is the one that exists today; it drives only camo-proxying and `dir="auto"`, never a
   visible string, and every visible name comes from this branch's README.
2. 030's wrapper verbatim (`<base href="http://127.0.0.1:6834/">` and its GitHub-ish stylesheet), a
   `python3 -m http.server 6834` over the repo root so `docs/media/friend-chat.gif` resolves to the
   GIF this branch regenerated, and the crumb rewritten.
3. Playwright chromium, viewport 1100×900, light, scale 1 — the same clip 030 shot.

**What the new file shows.** Crumb `2185Lab / infercat · branch t037-rename-infercat · README.md ·
rendered by GitHub's Markdown API (gfm), styled locally`; the H1 `Infercat`; the friend-chat still
from this branch's recording (`relayed via New York · 72 ms`, a fresh `0/200k tokens today` meter);
the in-app line now reads `Infercat records counts, never text.` 1100×900, 181 KB. Zero old-name
strings, checked where a PNG can be checked: the HTML the shot was taken from has 0 hits for
`bunny|bn1` and 27 for `infercat`.

**The crumb says `2185Lab / infercat` while GitHub still says `bunny-network`.** Deliberate, and the
one judgement call here: the frozen decisions write the repo path as `github.com/2185Lab/infercat`
everywhere, every link inside the render does, and the GitHub rename is your landing step.
Photographing today's literal path would have put `bunny-network` back into the one image this round
exists to clear. Say the word if you would rather the evidence show the pre-rename state.

**Unchanged from 030, still not a bug.** The CI badge renders as its alt text `CI`: shields cannot
read a private repo's workflow status, and `2185Lab/infercat` does not exist yet. It fills in once the
repo is public under the new name. The MIT badge renders.

**Still no producer in the repo** — this file is now a hand-made one-off for the second ticket
running. A small `web/dev/readme-shot.mjs` beside `launch-check.mjs` would fold it into
`make launch-check` and end the whole class of miss. That is new surface on a rename ticket, so I have
not added it; it is a cheap follow-up if you want it.

**Checks re-run.** Only a PNG changed, so the Go, wasm, brand and release-dry gates have no input that
moved; I re-ran everything that reads `web/` regardless.
- `cd web && pnpm typecheck` → `$ tsc --noEmit` (no output, exit 0)
- `cd web && pnpm test` → `Test Files  10 passed (10)` · `Tests  257 passed (257)`
- `cd web && pnpm lint` → `$ eslint .` (no output, exit 0)
- remainder gate re-run, identical to 09-05: `docs/MEASURE.md:6` (the `bunny-kit` repo path),
  `hack/load/sample.go:147` (`top -bn1`), `web/pnpm-lock.yaml:306` (base64 `BN1`).

**Production-touching, declared.** One read-only `gh api /markdown` call (nothing written to GitHub)
and a `python3 -m http.server` bound to 127.0.0.1:6834 over this worktree, stopped at the end (port
free). The founder's host on :9091 and the engine on :18080 were not touched.
- 2026-09-06 00:40 PM landing (mechanical freeze on top of `7b070b9`). Verifiers' surviving findings and what was done: docs/DESIGN.md
  `bn<N>` bullet → `ic<N> (N > 1)`; docs/MEASURE.md model path restored to the real `~/.cache/bunny-network/models/…`
  (the directory predates the rename; a rewritten path that does not exist breaks the reproduction command);
  `web/src/invite.test.ts` non-invite hash fixture follows the prefix family (`#ic2.something`); the three
  `BN_*` env vars of the Go live test and the web dev harnesses → `INFERCAT_LIVE_UPSTREAM`, `INFERCAT_BIN`,
  `INFERCAT_DATA_DIR` (pm/ tickets keep the old names as history); the Tailscale disclaimer moved to the first
  tailcat mention (README "How it works"), the License paragraph points back to it; all 112 `web/dev/screenshots`
  regenerated with `pnpm screenshots` (the fake-gateway harness) so no tracked image shows the old name. Left as
  history on purpose: `Inferact` in docs/NAME.md (the knockout record), the `bn.*` localStorage keys and the
  `bn###-` temp-dir prefixes (invisible to a friend; renaming the storage keys is a migration with a concept).
