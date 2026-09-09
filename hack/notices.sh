#!/bin/sh
# notices.sh — build (or verify) THIRD_PARTY_NOTICES.md, the notice we owe every dependency whose
# licence asks for one. Three parts, because we ship three things: the host binary's Go tree
# (`go-licenses report`), the web bundle's locked npm production tree, and the
# two typefaces the web bundle carries as woff2 files (FONTS below — an inventory, because a font
# checked into public/ has no package manager to ask).
#
#   sh hack/notices.sh write   # regenerate the file          (make notices)
#   sh hack/notices.sh check   # fail if it is stale or wrong (make notices-check)
#
# check fails on four things: a licence that is unknown or outside ALLOWED, a file that no longer
# matches what the trees say, a bundled font whose woff2 or licence file is missing, and a missing
# verbatim text for the licences reproduced in full — the two the tunnel is built on and the OFL of
# each font. The generator prints no date, so "regenerate and diff" is a real staleness test.
#
# Prereqs: go-licenses (go install github.com/google/go-licenses@latest), pnpm, node.
set -eu
# go install puts go-licenses in $GOPATH/bin, which is often not on PATH.
PATH="$(go env GOPATH 2>/dev/null)/bin:$PATH"; export PATH

mode=${1:-write}
case "$mode" in write | check) ;; *) echo "usage: notices.sh [write|check]" >&2; exit 2 ;; esac

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
out=THIRD_PARTY_NOTICES.md
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

command -v go-licenses >/dev/null 2>&1 ||
  { echo "notices: go-licenses not on PATH (go install github.com/google/go-licenses@latest)" >&2; exit 1; }

# Permissive: a notice is the whole obligation. Anything else (copyleft, source-offer, unknown) is
# a decision for the founder, not for this script — so it fails loudly instead of listing quietly.
ALLOWED="0BSD Apache-2.0 BSD-2-Clause BSD-3-Clause CC0-1.0 ISC MIT MIT-0 OFL-1.1 Unlicense Zlib"

# The two whose full text the file must carry verbatim: the tunnel we are built on.
VERBATIM="tailscale.com github.com/tailscale/tailcat"

# The typefaces the web bundle ships as woff2 (ticket 038: self-hosted, so the app asks no third
# party for a font). One row per family: name, upstream, the Google Fonts API revision the files
# were taken at, licence, the woff2 files, and the licence text — which the OFL requires to travel
# with the fonts, so it lives next to them in public/fonts and is reproduced in full below.
# family|upstream URL|version|licence|licence file|woff2 files (space-separated), all under web/public
FONTS="Archivo|https://github.com/Omnibus-Type/Archivo|Google Fonts API v25 (latin subset)|OFL-1.1|fonts/LICENSE-Archivo.txt|fonts/archivo-latin-var.woff2
IBM Plex Mono|https://github.com/IBM/plex|Google Fonts API v20 (latin subset)|OFL-1.1|fonts/LICENSE-IBMPlexMono.txt|fonts/ibm-plex-mono-400-latin.woff2 fonts/ibm-plex-mono-500-latin.woff2
Noto Sans SC|https://github.com/google/fonts/tree/main/ofl/notosanssc|app glyph subset|OFL-1.1|fonts/LICENSE-NotoSansSC.txt|fonts/noto-sans-sc.woff2"

module=$(go list -m)

# --- the Go tree -------------------------------------------------------------------------------
# Our own packages carry no third-party obligation, so they are ignored rather than reported as
# "Unknown", which is what go-licenses calls a package with no LICENSE of its own.
#
# The tree differs by target — dbus and netlink only on Linux, certstore only on macOS — so the
# report is the union over every platform the release builds (.goreleaser.yaml), never the
# platform this script happens to run on: a file generated on a laptop must verify in CI on Linux.
TARGETS="darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64"
printf '{{ range . }}{{ .Name }}\t{{ .Version }}\t{{ .LicenseName }}\t{{ .LicenseURL }}\t{{ .LicensePath }}\n{{ end }}' >"$tmp/go.tmpl"
: >"$tmp/go.all"
for target in $TARGETS; do
  GOOS=${target%/*} GOARCH=${target#*/} go-licenses report ./cmd/infercat --ignore "$module" --template "$tmp/go.tmpl" \
    >>"$tmp/go.all" 2>>"$tmp/go.err" ||
    { cat "$tmp/go.err" >&2; echo "notices: go-licenses report failed for $target" >&2; exit 1; }
done
LC_ALL=C sort -u "$tmp/go.all" >"$tmp/go.tsv"
[ -s "$tmp/go.tsv" ] || { cat "$tmp/go.err" >&2; echo "notices: go-licenses reported nothing" >&2; exit 1; }

# --- the web tree ------------------------------------------------------------------------------
[ -d web/node_modules ] || (cd web && pnpm install --frozen-lockfile >/dev/null 2>&1)
(cd web && pnpm licenses list --json --prod 2>/dev/null) >"$tmp/web.json" ||
  { echo "notices: pnpm licenses list failed" >&2; exit 1; }
node --test hack/notices-spdx.test.mjs >&2
node hack/notices-spdx.mjs "$tmp/web.json" "$ALLOWED" "$tmp/web-texts.md" web/pnpm-lock.yaml >"$tmp/web.tsv"
[ -s "$tmp/web.tsv" ] || { echo "notices: pnpm reported no packages" >&2; exit 1; }

# The embedded console has its own production dependency tree.
[ -d console/node_modules ] || (cd console && pnpm install --frozen-lockfile >/dev/null 2>&1)
(cd console && pnpm licenses list --json --prod 2>/dev/null) >"$tmp/console.json"
node hack/notices-spdx.mjs "$tmp/console.json" "$ALLOWED" "$tmp/console-texts.md" console/pnpm-lock.yaml >"$tmp/console.tsv"
cat "$tmp/console.tsv" >>"$tmp/web.tsv"
cat "$tmp/console-texts.md" >>"$tmp/web-texts.md"

# --- the bundled fonts -------------------------------------------------------------------------
# No package manager knows about a woff2 checked into public/, so the inventory is the FONTS list
# above and the check is that every file it names is really there: a font added or removed without
# its notice fails `make notices-check` instead of shipping unnoticed.
echo "$FONTS" | while IFS='|' read -r fname furl fver flic flicfile ffiles; do
  [ -n "$fname" ] || continue
  for f in $ffiles $flicfile; do
    [ -f "web/public/$f" ] ||
      { echo "notices: $fname declares web/public/$f, which is not there" >&2; exit 1; }
  done
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$fname" "$furl" "$fver" "$flic" "$flicfile" "$ffiles"
done >"$tmp/fonts.tsv" || exit 1
[ -s "$tmp/fonts.tsv" ] || { echo "notices: the FONTS inventory is empty" >&2; exit 1; }

# --- the licence gate --------------------------------------------------------------------------
{ cut -f3 "$tmp/go.tsv"; cut -f3 "$tmp/web.tsv"; cut -f4 "$tmp/fonts.tsv"; } | LC_ALL=C sort -u >"$tmp/seen"
bad=""
while read -r lic; do
  case " $ALLOWED " in *" $lic "*) ;; *) bad="$bad $lic" ;; esac
done <"$tmp/seen"
if [ -n "$bad" ]; then
  echo "notices: licence(s) not permissive or not recognised:$bad" >&2
  echo "notices: the dependencies concerned —" >&2
  for lic in $bad; do
    awk -F'\t' -v l="$lic" '$3 == l { print "  " $1 "  " l }' "$tmp/go.tsv" "$tmp/web.tsv" >&2
    awk -F'\t' -v l="$lic" '$4 == l { print "  " $1 "  " l }' "$tmp/fonts.tsv" >&2
  done
  exit 1
fi

# --- the file ----------------------------------------------------------------------------------
{
  echo "# Third-party notices"
  echo
  echo "Generated by \`make notices\` (hack/notices.sh) — do not edit by hand. Verified by"
  echo "\`make notices-check\`, which fails if this file is stale or if a dependency's licence is"
  echo "not one of: $ALLOWED."
  echo
  echo "Three things ship: the host binary (Go — the union over every release platform: $TARGETS),"
  echo "the web and console bundles (npm, locked production closure across platforms), and the two typefaces that bundle carries as"
  echo "woff2 files under \`web/public/fonts\` (self-hosted, so the app asks no third party for a font)."
  echo "Full licence texts live at the URLs below; the two the tunnel is built on and the Open Font"
  echo "License of each typeface are reproduced verbatim at the end of this file."
  echo
  echo "## Host binary (Go)"
  echo
  echo "| Package | Version | Licence | Text |"
  echo "|---|---|---|---|"
  awk -F'\t' '{ printf "| %s | %s | %s | %s |\n", $1, $2, $3, $4 }' "$tmp/go.tsv"
  echo
  echo "## Web and console bundles (npm, production dependencies)"
  echo
  while read -r lic; do
    n=$(awk -F'\t' -v l="$lic" '$3 == l { c++ } END { print c + 0 }' "$tmp/web.tsv")
    [ "$n" -gt 0 ] || continue
    if [ "$n" -eq 1 ]; then noun=package; else noun=packages; fi
    echo "**$lic** ($n $noun):"
    echo
    awk -F'\t' -v l="$lic" '$3 == l { printf "%s@%s, ", $1, $2 } END { printf "\n" }' "$tmp/web.tsv" |
      sed 's/, $//' | fold -s -w 96 | sed 's/[[:space:]]*$//'
    echo
  done <"$tmp/seen"
  echo "SPDX alternatives select the first permitted branch; AND obligations are listed under every required licence."
  echo
  cat "$tmp/web-texts.md"
  echo
  echo "## qrcode-generator — full licence text (MIT)"
  echo
  echo '```'
  cat console/public/LICENSE-qrcode-generator.txt
  echo '```'
  echo
  echo "## Bundled fonts (web app)"
  echo
  echo "| Typeface | Upstream | Version | Licence | Files |"
  echo "|---|---|---|---|---|"
  awk -F'\t' '{ gsub(/ /, ", ", $6); printf "| %s | %s | %s | %s | %s |\n", $1, $2, $3, $4, $6 }' "$tmp/fonts.tsv"
  echo
  for pkg in $VERBATIM; do
    path=$(awk -F'\t' -v p="$pkg" '$1 == p { print $5; exit }' "$tmp/go.tsv")
    [ -n "$path" ] && [ -f "$path" ] ||
      { echo "notices: no licence text found for $pkg" >&2; exit 1; }
    echo "## $pkg — full licence text"
    echo
    echo '```'
    cat "$path"
    echo '```'
    echo
  done
  # The OFL requires its own text to travel with the font, verbatim.
  while IFS='	' read -r fname _ _ _ flicfile _; do
    [ -n "$fname" ] || continue
    echo "## $fname — full licence text (SIL Open Font License 1.1)"
    echo
    echo '```'
    cat "web/public/$flicfile"
    echo '```'
    echo
  done <"$tmp/fonts.tsv"
} >"$tmp/notices.md"

if [ "$mode" = write ]; then
  mv "$tmp/notices.md" "$out"
  echo "notices: wrote $out ($(wc -l <"$out" | tr -d ' ') lines, $(wc -l <"$tmp/go.tsv" | tr -d ' ') Go, $(cut -f1,2 "$tmp/web.tsv" | sort -u | wc -l | tr -d ' ') npm, $(wc -l <"$tmp/fonts.tsv" | tr -d ' ') fonts)"
  exit 0
fi

[ -f "$out" ] || { echo "notices: $out is missing — run 'make notices'" >&2; exit 1; }
if ! diff -u "$out" "$tmp/notices.md"; then
  echo "notices: $out is stale — run 'make notices'" >&2
  exit 1
fi
echo "notices: OK — $(wc -l <"$tmp/go.tsv" | tr -d ' ') Go + $(cut -f1,2 "$tmp/web.tsv" | sort -u | wc -l | tr -d ' ') npm dependencies + $(wc -l <"$tmp/fonts.tsv" | tr -d ' ') bundled fonts, licences all in ALLOWED, verbatim texts present"
