# Rename day — every surface the name touches

**Done 2026-09-05 as ticket 037.** The name is **Infercat**: CLI and package `infercat`, invite
prefix `ic1`, module `github.com/2185Lab/infercat`. Everything below is the record of the inventory
as it was written before the name existed — the placeholders (`NEWNAME`, `newcli`, `nn1`) and the
old name are history, not instructions. Read `pm/tickets/037-rename-infercat.md` for what was
actually done and what was deliberately left (the tap, the web app URL, the two email addresses).

Placeholders below: `NEWNAME` (display, e.g. `Guestroom`), `newcli` (binary and package, lowercase,
no spaces, e.g. `guestroom`), `nn1` (invite prefix: two or three letters + `1`), `NEWORG/newrepo`
(the GitHub path; the org stays `2185Lab` unless the founder moves it), `https://app.example`
(the web app URL, F3).

## 1. The two constants (everything below follows from these)

| File | Edit |
|---|---|
| `internal/product/product.go` | `Name = "NEWNAME"` · `CLIName = "newcli"` · `InvitePrefix = "nn1"` · `WebURL = "https://app.example"` |
| `web/src/product.ts` | `PRODUCT_NAME = 'NEWNAME'` · `INVITE_PREFIX = 'nn1'` · `SOURCE_URL = 'https://github.com/NEWORG/newrepo'` · `DESCRIPTION` (re-read it: it names nothing today, keep it that way) |

The Makefile reads `CLIName`, `Version` and `WebURL` out of `product.go` (`CLI_NAME`,
`PRODUCT_VERSION`, `VITE_WEB_URL`); vite.config.ts reads `PRODUCT_NAME` and `DESCRIPTION` out of
`product.ts`. So the binary name, the archive names, the checksums file name, the cask name, the web
title, the metas and the manifest all follow from step 1 with no further edit. Test:
`grep -rn "Bunny Network\|bunny-network\|bn1" --include='*.go' --include='*.ts' --include='*.tsx' .`
must then find only the module path (step 2) and the places listed in steps 3–5.

## 2. The Go module path and the GitHub repository

```
gh repo rename newrepo --repo 2185Lab/bunny-network            # GitHub redirects the old path
go mod edit -module github.com/NEWORG/newrepo
find . -name '*.go' -not -path './web/node_modules/*' | xargs sed -i '' 's#github.com/2185Lab/bunny-network#github.com/NEWORG/newrepo#g'
sed -i '' 's#github.com/2185Lab/bunny-network#github.com/NEWORG/newrepo#g' .goreleaser.yaml README.md CONTRIBUTING.md .github/ISSUE_TEMPLATE/config.yml
git mv cmd/bunny-network cmd/newcli
sed -i '' 's#cmd/bunny-network#cmd/newcli#g' Makefile .goreleaser.yaml hack/notices.sh
```

`.goreleaser.yaml` also carries the repository twice by name: `release.github.name` and the cask's
`homepage` (set the latter to the web app URL). `web/package.json` `"name": "bunny-network-web"` →
`"newcli-web"` (cosmetic; the package is private). `go.mod`'s first line is the module path.

## 3. Copy that spells the name (not derived from the constants)

| Where | What | Edit |
|---|---|---|
| `cmd/bunny-network/main.go` `rootHelp` | `Bunny Network — share …` and every `bunny-network <cmd>` example | search-replace the display name and the binary name; the tests in `main_test.go` compare against `rootHelp`, so they follow |
| `cmd/bunny-network/serve.go` `serveHelp`, `keys.go` `keysHelp`/`keysAddHelp`/`keysLimitsHelp`, `status.go` `statusHelp`, `usagecmd.go` `usageHelp` | `bunny-network …` examples; `keysHelp` explains `bn1.<host address>.<secret>` | same replace; change `bn1.` to `nn1.` |
| `cmd/bunny-network/serve.go`, `keys.go`, `wire.go`, `config.go`, `usagecmd.go`, `status.go` | comments and error strings that say `bunny-network` (`grep -n bunny-network cmd/bunny-network/*.go`) | same replace |
| `web/src/ui/Connect.tsx` | the invite field placeholder `bn1.…` | `nn1.…` |
| `web/src/invite.ts` + `invite.test.ts`, `web/src/connect.test.ts`, `internal/invite/invite.go` + `_test.go`, `web/dev/*.mjs`, `web/dev/fake-*.ts` | literal `bn1.` invites in code and fixtures | `grep -rln 'bn1\.'` and replace; the format comment in `docs/ARCHITECTURE.md` §Invite format too |
| `web/src/transport/*.ts`, `web/wasm/*`, `web/dev/*` | `BunnyTunnel` (the wasm global) and `bunny.wasm` (the artifact name) | optional: these are internal identifiers a friend never sees; leave them, or rename `window.BunnyTunnel` → `window.NewcliTunnel` in `web/wasm/main_js.go`, `web/src/transport/types.ts` and `wasm.ts`, and `bunny.wasm` in `web/wasm/build.sh`, `web/src/transport/wasm.ts`, `.gitignore` |
| `internal/tunnel/tunnel.go` | the data-dir default and any `bunny-network` in comments | follows `product.CLIName`; check comments only |

Data directory: the default is `<user config dir>/<CLIName>`, so it moves with the constant. Existing
hosts (the founder's demo host) keep their identity by starting once with `--data-dir <old path>`,
or by moving the directory. Invites minted under `bn1` stop parsing when the prefix changes — mint
new ones after the rename; that is why the name is decided before the post.

## 4. Assets (rendered, not drawn: re-run)

| Asset | Source | Command |
|---|---|---|
| `web/public/favicon.svg` | the mark — a rabbit today; the new name may want a new mark | edit the SVG by hand; keep the `.m` class and the dark-scheme `<style>` |
| `web/public/favicon.png`, `apple-touch-icon.png`, `icon-192.png`, `icon-512.png`, `icon-maskable-512.png` | from the SVG | `make brand` |
| `web/public/og.png` (1200×630), `.github/social-preview.png` (1280×640) | the name and sentence from `product.ts` + the real connect card | `make brand` |
| GitHub → Settings → Social preview | `.github/social-preview.png` | upload by hand (there is no API for it) |
| `docs/media/friend-chat.gif`, `friend-chat.png`, `web/dev/screenshots/30-*.png` | the real app, which shows the name in its header and card | `make launch-check` with `INVITE=… APP=…` against a running host (see the script's header); commit the new files |

## 5. Documents and metadata

| Where | Edit |
|---|---|
| `README.md` | title line, `brew install 2185Lab/tap/newcli`, every `bunny-network` command, the data-dir paths, the alt text; delete the `TODO(rename day)` comments |
| `CHANGELOG.md` | the `bunny-network version` line and the release name |
| `docs/RELEASE.md`, `docs/ARCHITECTURE.md`, `docs/MEASURE.md`, `docs/DESIGN.md`, `docs/NAME.md` | `grep -n -i bunny docs/*.md` — commands and paths; NAME.md is history, leave it |
| `CONTRIBUTING.md`, `SECURITY.md` | the two `TODO(rename day)` addresses (`security@`, `conduct@`) must exist before the repo is public; the command names |
| `.github/ISSUE_TEMPLATE/*.yml` | `bunny-network version` / `status` in the prompts |
| `.goreleaser.yaml` | `release.github.name`, cask `homepage` (→ web URL), `description` if the sentence changes |
| GitHub description | `gh repo edit --description "…"` — today's text names nothing but says "one binary"; keep or reword |
| GitHub homepage | `gh repo edit --homepage https://app.example` (F3) |
| GitHub topics | `gh repo edit --add-topic newcli` if the name is a word people search; the rest stay |
| Homebrew tap | create `2185Lab/homebrew-tap` with a `Casks/` directory; flip `skip_upload` to `"auto"`; store `HOMEBREW_TAP_GITHUB_TOKEN` (docs/RELEASE.md) |
| `pm/BELIEFS.md`, `pm/LAUNCH.md`, `pm/HANDOFF.md`, tickets | records; leave as written, add the decision to BELIEFS' header line |
| The relay | `derp.2185lab.com` carries no product name; the DNS record and the droplet are named for the lab, not the product (pm/HANDOFF.md). Nothing to do unless the founder wants `derp.<newname>.…` |

## 6. Then

```
make check && (cd web && pnpm typecheck && pnpm test && pnpm lint)
make brand && make launch-check                      # new rasters, and a clean stranger's-eye pass
make notices-check && make release-dry               # archive names carry the new CLI name
git grep -n -i 'bunny\|bn1'                          # what remains must be history (pm/, docs/NAME.md) or the tunnel's internals
```

Commit as one change ("rename: NEWNAME"), tag nothing yet: F6 (history rewrite) comes first, then
the first tag (docs/RELEASE.md).
