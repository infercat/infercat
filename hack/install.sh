#!/bin/sh
# Pin INFERCAT_VERSION=v0.1.0; INFERCAT_INSTALL_DIR overrides the destination.
# Test hooks only: INFERCAT_OS / INFERCAT_ARCH override uname. INFERCAT_RELEASE_BASE replaces
# https://github.com/infercat/infercat/releases and serves latest JSON at BASE/latest for local tests.
set -eu

fail() { printf 'infercat: %s\n' "$*" >&2; exit 1; }
fetch() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$2" "$1"
    else
        fail 'install curl or wget first'
    fi
}
cleanup() {
    [ -z "$staged" ] || rm -f "$staged"
    rm -rf "$tmp"
}
main() {
    releases=https://github.com/infercat/infercat/releases
    os=$(printf '%s' "${INFERCAT_OS:-$(uname -s)}" | tr '[:upper:]' '[:lower:]')
    arch=${INFERCAT_ARCH:-$(uname -m)}
    case "$arch" in amd64|x86_64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; esac
    case "$os/$arch" in
        linux/amd64|linux/arm64|darwin/amd64|darwin/arm64) ;;
        *) fail "unsupported platform $os/$arch; see $releases" ;;
    esac
    tmp=$(mktemp -d "${TMPDIR:-/tmp}/infercat-install.XXXXXX")
    staged=
    trap cleanup 0
    trap 'exit 1' 1 2 3 15
    base=${INFERCAT_RELEASE_BASE:-$releases}
    base=${base%/}
    tag=${INFERCAT_VERSION:-}
    if [ -z "$tag" ]; then
        api=https://api.github.com/repos/infercat/infercat/releases/latest
        [ -z "${INFERCAT_RELEASE_BASE:-}" ] || api=$base/latest
        fetch "$api" "$tmp/latest.json" || fail "cannot resolve latest release; see $releases"
        tag=$(sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)
    fi
    version=${tag#v}
    case "$version" in ''|*[!0-9A-Za-z.+-]*) fail "invalid release version; see $releases" ;; esac
    case "$tag" in v*) ;; *) tag=v$tag ;; esac
    archive=infercat_${version}_${os}_${arch}.tar.gz
    fetch "$base/download/$tag/$archive" "$tmp/$archive" || fail 'archive download failed'
    fetch "$base/download/$tag/infercat_${version}_checksums.txt" "$tmp/checksums" || fail 'checksum download failed'
    expected=$(awk -v name="$archive" '$2 == name || $2 == "*" name { print $1 }' "$tmp/checksums")
    [ "${#expected}" -eq 64 ] || fail 'missing or ambiguous SHA-256 checksum'
    case "$expected" in *[!0-9a-fA-F]*) fail 'invalid SHA-256 checksum' ;; esac
    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$tmp/$archive")
    elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 "$tmp/$archive")
    else
        fail 'install sha256sum or shasum first'
    fi
    actual=${actual%% *}
    expected=$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')
    [ "$actual" = "$expected" ] || fail 'SHA-256 mismatch; nothing installed'
    # Only the named binary is streamed out, after verification; archive paths never reach disk.
    tar -xzOf "$tmp/$archive" infercat > "$tmp/infercat" || fail 'invalid or truncated archive'
    [ -s "$tmp/infercat" ] || fail 'archive contains no binary'
    if [ -n "${INFERCAT_INSTALL_DIR:-}" ]; then
        dest=$INFERCAT_INSTALL_DIR
    elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
        dest=/usr/local/bin
    else
        dest=${HOME:?HOME is not set}/.local/bin
    fi
    case "$dest" in /*) ;; *) dest=$(pwd)/$dest ;; esac
    [ ! -d "$dest/infercat" ] || fail 'install target is a directory'
    mkdir -p "$dest"
    # Rename on the destination filesystem so failures cannot truncate an existing install.
    staged=$(mktemp "$dest/.infercat.XXXXXX")
    cp "$tmp/infercat" "$staged"
    chmod 755 "$staged"
    mv -f "$staged" "$dest/infercat"
    staged=
    case ":${PATH:-}:" in *:"$dest":*) ;; *) printf 'Add %s to your PATH.\n' "$dest" ;; esac
    "$dest/infercat" version
    printf 'infercat serve\ninfercat keys add alice\n'
}
main
