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
python3 - "$test_root" <<'PY' &
import functools, hashlib, http.server, io, json, pathlib, tarfile, sys
root = pathlib.Path(sys.argv[1])
web = root / 'web'
for kind in ['good', 'bad-checksum', 'short-download', 'bad-archive', 'missing-checksum']:
    folder = web / kind / 'download' / 'v0.1.0'
    folder.mkdir(parents=True)
    (web / kind / 'latest').write_text(json.dumps({'tag_name': 'v0.1.0'}))
    checksums = []
    for os, arch in [('linux', 'amd64'), ('darwin', 'arm64')]:
        name = f'infercat_0.1.0_{os}_{arch}.tar.gz'
        data = b'#!/bin/sh\n[ "$1" = version ] || exit 1\nprintf "infercat 0.1.0 fixture\\n"\n'
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode='w:gz') as tar:
            info = tarfile.TarInfo('infercat'); info.size = len(data); info.mode = 0o755
            tar.addfile(info, io.BytesIO(data))
        archive = buf.getvalue()
        digest = hashlib.sha256(archive).hexdigest()
        if kind == 'bad-checksum': digest = '0' * 64
        if kind in ('short-download', 'bad-archive'): archive = archive[:16]
        if kind == 'bad-archive': digest = hashlib.sha256(archive).hexdigest()
        (folder / name).write_bytes(archive)
        if kind != 'missing-checksum': checksums.append(f'{digest}  {name}\n')
    (folder / 'infercat_0.1.0_checksums.txt').write_text(''.join(checksums))
class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args): pass
server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(Handler, directory=str(web)))
(root / 'port').write_text(str(server.server_port))
server.serve_forever()
PY
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
fail() { printf 'FAIL: %s\n' "$*" >&2; cat "$case_dir/output" >&2; exit 1; }
pass() { passed=$((passed + 1)); printf 'PASS: %s\n' "$*"; }
run() {
    case_dir=$test_root/$1
    mkdir -p "$case_dir/tmp" "$case_dir/bin"
    if env PATH="$6" TMPDIR="$case_dir/tmp" INFERCAT_INSTALL_DIR="$case_dir/bin" \
        INFERCAT_RELEASE_BASE="$base/$2" INFERCAT_OS="$3" INFERCAT_ARCH="$4" INFERCAT_VERSION="$5" \
        INSTALL_TEST_REAL_TAR="$real_tar" INSTALL_TEST_TAR_LOG="$case_dir/extracted" \
        /bin/sh "$installer" > "$case_dir/output" 2>&1; then status=0; else status=$?; fi
    [ -z "$(ls -A "$case_dir/tmp")" ] || fail 'installer left temporary files'
}
run linux good linux x86_64 '' "$normal_path"
[ "$status" -eq 0 ] && [ -x "$case_dir/bin/infercat" ] || fail 'linux install'
grep -Fx 'infercat 0.1.0 fixture' "$case_dir/output" >/dev/null || fail 'version not printed'
grep -F 'to your PATH.' "$case_dir/output" >/dev/null || fail 'PATH hint missing'
grep -Fx 'infercat keys add alice' "$case_dir/output" >/dev/null || fail 'quickstart missing'
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
    echo 'SKIP: wget-only (wget not installed)'
fi
printf 'install tests: %s passed / 0 failed\n' "$passed"
