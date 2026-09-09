"""Install/startup proof on a disposable Ubuntu CI runner; no inference is claimed."""
import hashlib
import http.server
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import threading
import time


def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=90).strip()


assert os.environ.get('GITHUB_ACTIONS') == 'true', 'run only on the disposable CI runner'
packages = sorted([*Path('dist').glob('*.deb'), *Path('dist').glob('*.rpm')])
assert len(packages) == 4, packages
checksums, = Path('dist').glob('*_checksums.txt')
checksums = dict(line.split(maxsplit=1)[::-1] for line in checksums.read_text().splitlines())
expected = {'/usr/bin/infercat', '/usr/lib/systemd/user/infercat.service',
            '/usr/share/doc/infercat/LINUX.md', '/usr/share/doc/infercat/LINUX.zh-CN.md',
            '/usr/share/doc/infercat/LICENSE', '/usr/share/doc/infercat/THIRD_PARTY_NOTICES.md'}
seen = set()
for package in packages:
    assert hashlib.sha256(package.read_bytes()).hexdigest() == checksums[package.name]
    if package.suffix == '.deb':
        arch = run('dpkg-deb', '-f', str(package), 'Architecture')
        assert run('dpkg-deb', '-f', str(package), 'Maintainer') == '2185 Lab <max@2185lab.com>'
        data = subprocess.check_output(['dpkg-deb', '--fsys-tarfile', str(package)])
        with tarfile.open(fileobj=io.BytesIO(data)) as archive:
            names = {'/' + m.name.removeprefix('./') for m in archive.getmembers()}
            member = next(m for m in archive.getmembers() if '/' + m.name.removeprefix('./') == '/usr/lib/systemd/user/infercat.service')
            unit = archive.extractfile(member).read()
            assert unit == Path('packaging/infercat.service').read_bytes()
    else:
        arch = run('rpm', '-qp', '--qf', '%{ARCH}', str(package))
        arch = {'x86_64': 'amd64', 'aarch64': 'arm64'}[arch]
        names = set(run('rpm', '-qpl', str(package)).splitlines())
        assert run('rpm', '-qp', '--qf', '%{LICENSE}', str(package)) == 'MIT'
    assert expected <= names, (package, expected - names)
    assert (package.suffix, arch) not in seen
    seen.add((package.suffix, arch))
    print(f'{package.name}: {arch}, payload and checksum verified', flush=True)
assert seen == {(fmt, arch) for fmt in ('.deb', '.rpm') for arch in ('amd64', 'arm64')}

# The real service discovers this fixture. It offers metadata, never a completion endpoint.
class MetadataOnly(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != '/v1/models':
            self.send_error(404)
            return
        body = json.dumps({'data': [{'id': 'metadata-only-092', 'owned_by': 'fixture'}]}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


data_dir = Path(os.environ.get('XDG_CONFIG_HOME') or Path.home() / '.config') / 'infercat'
assert not data_dir.exists(), 'proof requires a fresh user data directory'
server = http.server.HTTPServer(('127.0.0.1', 8080), MetadataOnly)
threading.Thread(target=server.serve_forever, daemon=True).start()
os.environ['XDG_RUNTIME_DIR'] = f'/run/user/{os.getuid()}'
os.environ['DBUS_SESSION_BUS_ADDRESS'] = f'unix:path={os.environ["XDG_RUNTIME_DIR"]}/bus'
run('sudo', 'systemctl', 'start', f'user@{os.getuid()}.service')
# Match the invoking user's configuration directory without overriding the shipped unit.
run('systemctl', '--user', 'import-environment' if os.environ.get('XDG_CONFIG_HOME') else 'unset-environment', 'XDG_CONFIG_HOME')
deb, = Path('dist').glob('*_linux_amd64.deb')
print(run('sudo', 'dpkg', '-i', str(deb)), flush=True)
print(run('/usr/bin/infercat', 'version'), flush=True)
run('systemctl', '--user', 'daemon-reload')
assert run('systemctl', '--user', 'show', 'infercat', '--property=UnitFileState', '--value') == 'disabled'
assert run('systemctl', '--user', 'show', 'infercat', '--property=ActiveState', '--value') == 'inactive'
print('Installed unit: disabled and inactive (no automatic startup)', flush=True)
try:
    run('systemctl', '--user', 'start', 'infercat')
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        result = subprocess.run(['/usr/bin/infercat', 'status'], text=True, capture_output=True, timeout=10)
        banner = run('journalctl', '--user', '-u', 'infercat', '--no-pager', '-o', 'cat')
        if result.returncode == 0 and 'metadata-only-092' in banner and 'console   http://127.0.0.1:9101' in banner:
            break
        time.sleep(0.25)
    else:
        raise AssertionError('service did not become ready: ' + banner + result.stderr)
    assert run('systemctl', '--user', 'is-active', 'infercat') == 'active'
    assert run('systemctl', '--user', 'show', 'infercat', '--property=NRestarts', '--value') == '0'
    print(banner, flush=True)
    print(result.stdout, flush=True)
    print('PASS: 4/4 packages; 1/1 installed user-service startup (metadata-only, no inference)', flush=True)
finally:
    run('systemctl', '--user', 'stop', 'infercat')
    server.shutdown()
    server.server_close()
