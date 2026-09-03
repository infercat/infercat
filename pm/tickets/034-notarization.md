---
id: 034
title: Sign and notarize the macOS binaries (Developer ID) so the first run needs no right-click
kind: normal
size: 1
status: draft
updated: 2026-09-03
release: demo-1 (or the first release after it)
---

# 034 — macOS signing and notarization

**Why.** 030 found no `Developer ID Application` identity on the build machine — only
`Apple Development: Yuanping Song (R32T2KFMTK)` (`security find-identity -v -p codesigning`), and
neither `bunny-kit/docs` nor `bunny-screenshot/docs` records a notarytool profile (bunny-screenshot's
ADR-0012 signs ad hoc, `--sign -`). An unsigned binary downloaded by a browser carries the
quarantine attribute, and macOS refuses to run it until the host right-clicks → Open or runs
`xattr -d com.apple.quarantine`. The README documents that path (030); this ticket removes the need
for it. The Homebrew cask installs without quarantine on its own (the cask's post-install hook
strips the attribute — .goreleaser.yaml), so this is the tarball path's problem only.

**Founder's part (before dispatch).** In the Apple Developer account: create a **Developer ID
Application** certificate, install it in the login keychain of the machine (or the CI runner) that
signs; create an App Store Connect API key (or an app-specific password) for `notarytool` and store
it as a keychain profile: `xcrun notarytool store-credentials AC_NOTARY --apple-id … --team-id
R32T2KFMTK --password …`. Nothing in the repo holds a secret.

**Promises.**
1. `.goreleaser.yaml` signs the darwin binaries (`notarize.macos` with `codesign --options runtime
   --timestamp`, entitlements none) and submits them to notarytool; the archive carries the signed
   binary. A signed, notarized `bunny-network` downloaded through Safari runs on first double-click
   and from Terminal with no dialog (`spctl -a -vv -t install` says `accepted`, `source=Notarized
   Developer ID`).
2. The release workflow does the signing on `macos-latest` (or the signing step alone on macOS with
   the rest on Linux), with the certificate and the notarytool key as encrypted repository secrets
   (`MACOS_SIGN_P12`, `MACOS_SIGN_P12_PASSWORD`, `MACOS_NOTARY_KEY`, `MACOS_NOTARY_KEY_ID`,
   `MACOS_NOTARY_ISSUER`).
3. `make release-dry` still runs on a machine with no identity (signing skipped with a printed
   note), so the "green from nothing" law holds.
4. README: the "macOS says it cannot verify the developer" section is deleted; CHANGELOG's known
   limitation is removed.

**Size 1** (≤150 lines: YAML and one Makefile note). Concept budget 0. **Normal.**

**Scope.** `.goreleaser.yaml`, `.github/workflows/release.yml`, `README.md`, `CHANGELOG.md`. Not
the binary, not the web app.

**Evidence wanted.** `codesign -dv --verbose=2` and `spctl -a -vv -t install` output on a release
artifact; `xcrun notarytool log` for one submission; the first-run experience from a fresh macOS
user account, described in one line.

## Log

## Report
