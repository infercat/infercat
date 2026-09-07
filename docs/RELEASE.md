# Releasing

A release is a tag. Everything else is a workflow reading the tag. Nothing here is run before the
founder's decisions in `pm/LAUNCH.md` are closed (name, URL, public repo, history rewrite).

## Before the first release (once)

1. `docs/RENAME.md` — the rename, top to bottom, on a branch; `make brand`; `make launch-check`.
2. Create the tap repository `infercat/homebrew-tap` (empty, public, with a `Casks/` directory) and a
   fine-grained PAT with **contents: write** on it; store it as the repository secret
   `HOMEBREW_TAP_GITHUB_TOKEN`.
3. In `.goreleaser.yaml`, flip `homebrew_casks[].skip_upload` from `true` to `"auto"` (uploads on
   a real tag, skips prereleases and snapshots).
4. Set `homepage` there and `product.WebURL` in Go to the web app's URL (F3).
5. Run the release workflow by hand once (`gh workflow run release.yml`): it builds the snapshot
   and uploads it as a workflow artifact, publishing nothing. Download it, install the binary the way
   a stranger would (README, Quickstart), serve, mint, chat from a phone.
6. Move the `## v0.1.0 — unreleased` heading in `CHANGELOG.md` to the date; set `Version` in
   `internal/product/product.go` to `0.1.0`.

## Every release

```
git switch main && git pull
make check && make notices-check && make release-dry      # green from a clean tree
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The tag runs `.github/workflows/release.yml`: `make release-publish` → goreleaser builds the five
binaries, the six archives, the checksums file, and a **draft** GitHub Release named
`infercat 0.1.0`; the cask is committed to the tap. Then:

1. Open the draft. Paste the `CHANGELOG.md` entry above the generated notes. Publish.
2. `brew install infercat/tap/infercat` on a machine that has never had it; `infercat
   version` prints the tag.
3. Deploy `web-0.1.0.zip` to the web app's host (F3) — the bundle is static; unzip at the site root.
4. Bump `Version` in `product.go` to the next `-dev` (e.g. `0.1.1-dev`) on main.

## If the workflow is down

`make release-publish` from a laptop with `GITHUB_TOKEN` and `HOMEBREW_TAP_GITHUB_TOKEN` in the
environment does the same thing; goreleaser refuses unless HEAD is the tag and the tree is clean.

## What a release must never do

Tag from a branch, ship notices that `make notices-check` rejects, or publish a version the web
bundle does not carry (the Makefile stamps both from the same constant — a bare `goreleaser` run
fails on purpose).
