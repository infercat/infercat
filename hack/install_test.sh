#!/bin/sh
# Local fake releases exercise the actual installer, including its download clients.
set -eu
installer=$(CDPATH='' cd -- "$(dirname "$0")" && pwd)/install.sh
test_root=$(mktemp -d "${TMPDIR:-/tmp}/infercat-install-test.XXXXXX")
server_pid=
cleanup() {
    if [ -n "$server_pid" ]; then kill "$server_pid" 2>/dev/null || :; wait "$server_pid" 2>/dev/null || :; fi
    rm -rf "$test_root"
}
trap cleanup 0
trap 'exit 1' 1 2 3 15
go build -o "$test_root/fixture" "$(dirname "$installer")/install-fixture"
"$test_root/fixture" "$test_root" &
server_pid=$!
attempt=0
while [ ! -s "$test_root/port" ]; do
    attempt=$((attempt + 1))
    [ "$attempt" -lt 100 ] || { echo 'fixture server did not start' >&2; exit 1; }
    kill -0 "$server_pid" 2>/dev/null || { echo 'fixture server exited' >&2; exit 1; }
    sleep 0.1
done
base=http://127.0.0.1:$(cat "$test_root/port")
mkdir "$test_root/tools"
cat > "$test_root/tools/tar" <<'SH'
#!/bin/sh
printf 'called\n' > "$INSTALL_TEST_TAR_LOG"
exec "$INSTALL_TEST_REAL_TAR" "$@"
SH
chmod +x "$test_root/tools/tar"
real_tar=$(command -v tar)
normal_path=$test_root/tools:$PATH
passed=0
failed=0
skipped=0
fail() { failed=$((failed + 1)); printf 'FAIL: %s\n' "$*" >&2; cat "$case_dir/output" >&2; summary; exit 1; }
summary() { printf 'install tests: %s passed / %s failed / %s skipped of %s total\n' "$passed" "$failed" "$skipped" "$((passed + failed + skipped))"; }
pass() { passed=$((passed + 1)); printf 'PASS: %s\n' "$*"; }
run() {
    case_dir=$test_root/$1
    mkdir -p "$case_dir/tmp" "$case_dir/bin"
    if env PATH="$6" TMPDIR="$case_dir/tmp" HOME="$case_dir/home" INFERCAT_INSTALL_DIR="${install_override-$case_dir/bin}" INFERCAT_SYSTEM_BIN="${system_override-$case_dir/system}" \
        INFERCAT_RELEASE_BASE="${base_override-$base/$2}" INFERCAT_OS="$3" INFERCAT_ARCH="$4" INFERCAT_VERSION="$5" \
        INSTALL_TEST_REAL_TAR="$real_tar" INSTALL_TEST_TAR_LOG="$case_dir/extracted" \
        /bin/sh "$installer" > "$case_dir/output" 2>&1; then status=0; else status=$?; fi
    [ -z "$(ls -A "$case_dir/tmp")" ] || fail 'installer left temporary files'
}
run linux good linux x86_64 '' "$normal_path"
[ "$status" -eq 0 ] && [ -x "$case_dir/bin/infercat" ] || fail 'linux install'
grep -Fx 'infercat 0.1.0 fixture' "$case_dir/output" >/dev/null || fail 'version not printed'
grep -F 'first on PATH' "$case_dir/output" >/dev/null || fail 'PATH hint missing'
grep -F "'$case_dir/bin/infercat' keys add alice" "$case_dir/output" >/dev/null || fail 'quickstart missing'
pass 'linux/amd64 latest: installed, version and PATH/quickstart printed, temp cleaned'
run darwin good darwin aarch64 v0.1.0 "$normal_path"
[ "$status" -eq 0 ] && [ "$("$case_dir/bin/infercat" version)" = 'infercat 0.1.0 fixture' ] || fail 'darwin install'
pass 'darwin/arm64 pinned: installed and executable, temp cleaned'
for kind in bad-checksum short-download missing-checksum; do
    run "$kind" "$kind" linux amd64 v0.1.0 "$normal_path"
    [ "$status" -ne 0 ] && [ ! -e "$case_dir/bin/infercat" ] && [ ! -e "$case_dir/extracted" ] || fail "$kind was not refused before extraction"
    pass "$kind: refused before extraction, nothing installed, temp cleaned"
done
run bad-archive bad-archive linux amd64 v0.1.0 "$normal_path"
[ "$status" -ne 0 ] && [ ! -e "$case_dir/bin/infercat" ] && [ -e "$case_dir/extracted" ] || fail 'invalid verified archive was installed'
pass 'truncated archive with matching checksum: tar refused, nothing installed, temp cleaned'
run unknown good linux riscv64 v0.1.0 "$normal_path"
[ "$status" -ne 0 ] && [ "$(wc -l < "$case_dir/output" | tr -d ' ')" = 1 ] || fail 'unknown arch message'
grep -Fx 'infercat: unsupported platform linux/riscv64; see https://github.com/infercat/infercat/releases' "$case_dir/output" >/dev/null || fail 'unknown arch needs Releases link'
pass 'unknown arch: one-line Releases refusal'
printf 'existing binary\n' > "$test_root/existing-sentinel"
mkdir -p "$test_root/preserve/bin"
cp "$test_root/existing-sentinel" "$test_root/preserve/bin/infercat"
run preserve bad-checksum linux amd64 v0.1.0 "$normal_path"
[ "$status" -ne 0 ] || fail 'checksum refusal succeeded'
cmp -s "$test_root/existing-sentinel" "$case_dir/bin/infercat" || fail 'existing install changed on checksum refusal'
pass 'checksum refusal preserves existing install'
run 'path with spaces' good darwin arm64 v0.1.0 "$normal_path"
[ "$status" -eq 0 ] && [ -x "$case_dir/bin/infercat" ] || fail 'install path with spaces'
pass 'install directory with spaces'
mkdir -p "$test_root/target-directory/bin/infercat"
printf 'keep\n' > "$test_root/target-directory/bin/infercat/sentinel"
run target-directory good linux amd64 v0.1.0 "$normal_path"
[ "$status" -ne 0 ] && [ "$(ls -A "$case_dir/bin/infercat")" = sentinel ] || fail 'directory target was mutated'
pass 'directory target refused without mutation'
if command -v wget >/dev/null 2>&1; then
    mkdir "$test_root/wget-only"
    for tool in wget uname mktemp tr sed head awk shasum sha256sum perl mkdir cp chmod mv rm gzip; do
        path=$(command -v "$tool" || :)
        [ -z "$path" ] || ln -s "$path" "$test_root/wget-only/$tool"
    done
    ln -s "$test_root/tools/tar" "$test_root/wget-only/tar"
    run wget good linux amd64 v0.1.0 "$test_root/wget-only"
    [ "$status" -eq 0 ] && [ -x "$case_dir/bin/infercat" ] || fail 'wget-only install'
    pass 'wget without curl: verified install and version output'
else
    skipped=$((skipped + 1))
    echo 'SKIP: wget-only (wget not installed)'
fi
# Default destination probes and PATH truth use an isolated test home/system prefix.
install_override=
system_override=$test_root/system-bin
mkdir "$system_override"
run default-system good linux amd64 '' "$normal_path"
[ "$status" -eq 0 ] && [ -x "$system_override/infercat" ] || fail 'system destination'
pass 'writable system destination, large single-line latest response'
system_override=$test_root/absent-system
run default-home good linux amd64 v0.1.0 "$normal_path"
[ "$status" -eq 0 ] && [ -x "$case_dir/home/.local/bin/infercat" ] || fail 'home fallback'
pass 'default home fallback created'
unset install_override system_override
mkdir -p "$test_root/shadow"
printf '#!/bin/sh\nprintf "old version\\n"\n' > "$test_root/shadow/infercat"
chmod +x "$test_root/shadow/infercat"
run shadow good linux amd64 v0.1.0 "$test_root/shadow:$normal_path"
[ "$status" -eq 0 ] || fail 'shadow install'
grep -F "Installed $case_dir/bin/infercat; PATH resolves infercat to $test_root/shadow/infercat" "$case_dir/output" >/dev/null || fail 'PATH shadow not named'
[ "$(tail -2 "$case_dir/output" | head -1 | sed 's/ serve$/ version/' | /bin/sh)" = 'infercat 0.1.0 fixture' ] || fail 'quickstart selected the wrong binary'
pass 'PATH shadow names both paths and full-path quickstart'
mkdir -p "$test_root/symlink/bin"
ln -s "$test_root/shadow/infercat" "$test_root/symlink/bin/infercat"
run symlink good linux amd64 v0.1.0 "$normal_path"
[ "$status" -ne 0 ] && [ -L "$case_dir/bin/infercat" ] || fail 'symlink replaced'
grep -F 'brew upgrade' "$case_dir/output" >/dev/null || fail 'brew advice missing'
[ "$("$test_root/shadow/infercat")" = 'old version' ] || fail 'symlink target changed'
pass 'symlink install refused with brew upgrade advice'
run "quote'path" good linux amd64 v0.1.0 "$normal_path"
[ "$status" -eq 0 ] || fail 'quoted path install'
[ "$(tail -2 "$case_dir/output" | head -1 | sed 's/ serve$/ version/' | /bin/sh)" = 'infercat 0.1.0 fixture' ] || fail 'quoted quickstart'
pass 'quickstart shell-quotes destination'
run resolve absent linux amd64 '' "$normal_path"
[ "$status" -ne 0 ] || fail 'missing latest accepted'
grep -F 'rate limit, no published release, or network failure' "$case_dir/output" >/dev/null || fail 'resolve reasons missing'
grep -F 'INFERCAT_VERSION=vX.Y.Z' "$case_dir/output" >/dev/null || fail 'pin advice missing'
pass 'resolve failure names causes and pin escape hatch'

base_override=http://example.invalid/releases
run insecure good linux amd64 v0.1.0 "$normal_path"
[ "$status" -ne 0 ] || fail 'insecure release URL accepted'
grep -F 'downloads require HTTPS' "$case_dir/output" >/dev/null || fail 'HTTPS refusal missing'
pass 'non-loopback HTTP refused before download'
unset base_override

summary
