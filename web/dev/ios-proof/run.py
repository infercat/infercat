#!/usr/bin/env python3
"""096: native iOS proof. No browser flags, fabricated screenshots, or shared-network changes."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[3]
WDA = '23b864e7cc2cd1e94bbbc83b3a32e501f1f2b484'  # Appium WebDriverAgent 16.12.6
RUNTIME = 'com.apple.CoreSimulator.SimRuntime.iOS-26-5'
DEVICE = 'com.apple.CoreSimulator.SimDeviceType.iPhone-14'  # 390 x 844 points
SITE = 'https://infercat.ai/'
LABELS = {
    'en': ['Settings', 'Add to Home Screen', 'Copy invite', 'Got it', 'View More', 'Add',
           'Paste', 'Allow Paste', 'Connect', 'Message', 'Edit', 'Customize', 'Tinted', 'Default'],
    'zh': ['设置', '添加到主屏幕', '复制邀请码', '知道了', '查看更多', '添加',
           '粘贴', '允许粘贴', '连接', '消息', '编辑', '自定义', '色调', '默认'],
}


def native_env():
    # /usr/bin/python3's Xcode shim injects macOS CPATH/SDKROOT; never inherit those for iOS.
    return {k: v for k, v in os.environ.items() if k not in ('CPATH', 'SDKROOT')}


def run(*args, check=True):
    p = subprocess.run(list(map(str, args)), cwd=ROOT, capture_output=True, text=True,
                       env=native_env() if str(args[0]) == 'xcodebuild' else None)
    if check and p.returncode:
        if str(args[0]) == 'xcodebuild':
            print('\n'.join(line for line in (p.stdout + p.stderr).splitlines()
                            if 'error:' in line or 'fatal' in line), flush=True)
        # Arguments/output may contain the test invite. Do not log either.
        raise RuntimeError(f'{Path(args[0]).name} failed ({p.returncode})')
    return p.stdout.strip()


def until(read, description, timeout=60):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        try:
            value = read()
            if value:
                return value
        except (urllib.error.URLError, RuntimeError, OSError):
            pass
        time.sleep(.3)
    raise RuntimeError('Timed out: ' + description)


def stop(p):
    if p and p.poll() is None:
        p.terminate()
        try:
            p.wait(timeout=10)
        except subprocess.TimeoutExpired:
            p.kill()
            p.wait()


def launch_frame(video, output):
    # Detect the blank native transition in the original video; crop only for detection.
    probe = subprocess.run(['ffmpeg', '-hide_banner', '-i', str(video), '-vf',
        'crop=iw-100:ih-600:50:300,negate,blackframe=amount=100:threshold=32', '-an', '-f', 'null', '-'],
        capture_output=True, text=True, check=True)
    matches = re.findall(r'frame:(\d+) pblack:100 ', probe.stderr)
    if not matches:
        raise RuntimeError('No blank launch transition detected; inspect the original MOV')
    frame = int(matches[0])  # First blank light frame after the native opening animation.
    run('ffmpeg', '-y', '-hide_banner', '-loglevel', 'error', '-i', video,
        '-vf', f'select=eq(n\\,{frame})', '-frames:v', '1', output)
    return frame


class Phone:
    def __init__(self, device, port, lang, out):
        self.device, self.lang, self.out = device, lang, out
        self.base, self.sid = f'http://127.0.0.1:{port}', ''
        until(lambda: self.call('GET', '/status')['value']['ready'], 'native runner', 90)
        self.sid = self.call('POST', '/session', {'capabilities': {'alwaysMatch': {
            'bundleId': 'com.apple.mobilesafari', 'shouldWaitForQuiescence': False,
            'waitForIdleTimeout': 0}}})['sessionId']

    def call(self, method, path, body=None):
        request = urllib.request.Request(self.base + path, method=method,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Content-Type': 'application/json'})
        with urllib.request.urlopen(request, timeout=45) as response:
            return json.load(response)

    def api(self, method, path, body=None):
        return self.call(method, '/session/' + self.sid + path, body)['value']

    def tree(self):
        return ET.fromstring(self.api('GET', '/source'))

    def node(self, name, kind=None):
        return next((e for e in self.tree().iter() if e.get('visible') == 'true'
                     and (e.get('name') == name or e.get('label') == name)
                     and (kind is None or e.tag == 'XCUIElementType' + kind)), None)

    def has(self, name, kind=None):
        return self.node(name, kind) is not None

    def wait(self, name, kind=None):
        return until(lambda: self.has(name, kind), name)

    def tap(self, name, web=False):
        self.wait(name)
        if web:
            e = self.node(name, 'Button')
            assert e is not None, name
            self.xy(float(e.get('x')) + float(e.get('width')) / 2,
                    float(e.get('y')) + float(e.get('height')) / 2)
        else:
            quoted = json.dumps(name, ensure_ascii=False)
            e = self.api('POST', '/element', {'using': 'predicate string',
                         'value': f'name == {quoted} OR label == {quoted}'})
            self.api('POST', '/element/' + e['ELEMENT'] + '/click', {})
        time.sleep(.5)  # UIKit presentation animation, not network readiness.

    def xy(self, x, y):
        self.api('POST', '/wda/tap', {'x': x, 'y': y})

    def swipe(self, direction, velocity=700):
        self.api('POST', '/wda/swipe', {'direction': direction, 'velocity': velocity})
        time.sleep(.5)

    def home(self):
        self.api('POST', '/wda/pressButton', {'name': 'home'})
        time.sleep(.6)

    def shot(self, name):
        path = self.out / f'096-{self.lang}-{name}.png'
        run('xcrun', 'simctl', 'io', self.device, 'screenshot', path)
        return path

    def proof(self, invite, checks):
        settings, install, copy, got, more, add, paste, allow, connect, message, edit, customize, tinted, default = LABELS[self.lang]
        def passed(name):
            checks.append(self.lang + ': ' + name)
            print('PASS', checks[-1], flush=True)
        run('xcrun', 'simctl', 'openurl', self.device, SITE + '#' + invite)
        self.wait(message, 'TextView')
        if self.has('xmark.circle.fill'):
            self.tap('xmark.circle.fill')
        self.tap(settings, web=True)
        for _ in range(5):
            if self.has(install, 'Button'):
                break
            self.swipe('up')
        self.tap(install, web=True)
        self.tap(copy, web=True)
        assert run('xcrun', 'simctl', 'pbpaste', self.device) == invite
        self.shot('copy-invite')
        passed('Copy invite equals the isolated host invite')
        self.tap(got, web=True)
        for label in ['MoreMenuButton', 'ShareButton', more, install]:
            self.tap(label)
        # The installation URL must have no invite fragment; iOS generates the launch screen.
        self.wait(add, 'Button')
        until(lambda: any(e.get('value') == SITE or e.get('label') == SITE
                          for e in self.tree().iter()), 'clean native install URL', 10)
        self.shot('native-install')
        self.tap(add)
        self.home()
        if not self.has('Infercat', 'Icon'):
            self.swipe('left')
        self.wait('Infercat', 'Icon')
        self.shot('icon-default')
        passed('native Add to Home Screen installed the masked icon')
        video = self.out / f'096-{self.lang}-launch.mov'
        with tempfile.TemporaryFile(mode='w+') as log:
            recorder = subprocess.Popen(['xcrun', 'simctl', 'io', self.device,
                'recordVideo', '--codec=h264', '--force', str(video)], stdout=log, stderr=log)
            try:
                def started():
                    log.seek(0)
                    return 'Recording started' in log.read()
                until(started, 'video recording', 10)
                self.tap('Infercat')
                self.wait(paste, 'Button')
            finally:
                recorder.send_signal(signal.SIGINT)
                recorder.wait(timeout=15)
        frame = launch_frame(video, self.out / f'096-{self.lang}-launch.png')
        passed(f'original launch-video frame {frame} captures the native blank transition')
        if self.has('Done', 'Button'):
            self.tap('Done')
        elif self.has('完成', 'Button'):
            self.tap('完成')
        self.tap(paste, web=True)
        self.wait(allow, 'Button')
        self.shot('paste-permission')
        self.tap(allow)
        # iOS may focus/scroll after paste. Dismiss keyboard and use native scroll-to-top.
        for done in ['Done', '完成']:
            if self.has(done, 'Button'):
                self.tap(done)
        self.xy(190, 20)
        time.sleep(.7)
        fields = [e for e in self.tree().iter() if e.tag == 'XCUIElementTypeTextView']
        assert any(e.get('value') == invite for e in fields), 'exact pasted invite'
        self.tap(connect, web=True)
        self.wait(message, 'TextView')
        assert self.has('096 isolated host'), 'owned host identity'
        assert not self.has('MoreMenuButton'), 'no Safari chrome'
        composer = self.node(message, 'TextView')
        assert float(composer.get('y')) + float(composer.get('height')) <= 810
        self.shot('standalone')
        passed('native paste permission, exact invite, standalone chat and bottom safe area')
        self.home()
        self.api('POST', '/wda/touchAndHold', {'x': 180, 'y': 550, 'duration': 1.2})
        for label in [edit, customize, tinted]:
            self.tap(label)
        self.shot('icon-tinted')
        assert self.node(tinted).get('value') == '1', 'native tinted selection'
        passed('native tinted Home Screen icon')
        self.tap(default)
        self.xy(180, 350)
        # No network-off substitute is silently reported as device-offline evidence.


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, default=Path('/private/tmp/infercat-096-proof'))
    args = parser.parse_args()
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    engine = os.environ.get('INFERCAT_PWA_ENGINE', '')
    if not re.fullmatch(r'http://127\.0\.0\.1:\d+', engine):
        raise RuntimeError('Set INFERCAT_PWA_ENGINE to your own running loopback model engine')
    xcode = run('xcodebuild', '-version')
    assert xcode == 'Xcode 26.6\nBuild version 17F113', xcode
    runtimes = json.loads(run('xcrun', 'simctl', 'list', 'runtimes', '--json'))['runtimes']
    assert any(r['identifier'] == RUNTIME and r['isAvailable'] and r['buildversion'] == '23F77'
               for r in runtimes), 'Install the founder-authorized iOS 26.5 runtime first'
    assert shutil.which('ffmpeg'), 'ffmpeg is required for actual launch-video frames'
    checks, devices = [], []
    host = runner = None
    result = {'xcode': xcode, 'runtime': 'iOS 26.5 (23F77)', 'device': 'iPhone 14, 390x844 @3x',
              'automation': 'WebDriverAgent 16.12.6 at ' + WDA, 'site': SITE,
              'base': run('git', 'rev-parse', 'HEAD'),
              'harness_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), 'passed': checks, 'failed': [],
              'skipped': ['en: device network-off unsupported', 'zh: device network-off unsupported']}
    with tempfile.TemporaryDirectory(prefix='infercat-096-') as tmp:
        work = Path(tmp)
        def cli(*args):
            return run(str(ROOT / 'bin/infercat'), *args, '--data-dir', work / 'host')
        try:
            result['site_html_sha256'] = hashlib.sha256(urllib.request.urlopen(urllib.request.Request(
                SITE, headers={'User-Agent': 'Infercat-iOS-proof/096'}), timeout=30).read()).hexdigest()
            source, build = work / 'wda', work / 'build'
            print('Building pinned native automation runner…', flush=True)
            run('git', '-c', 'http.version=HTTP/1.1', 'clone', '--depth', '1', '--branch',
                'v16.12.6', 'https://github.com/appium/WebDriverAgent.git', source)
            assert run('git', '-C', source, 'rev-parse', 'HEAD') == WDA
            run('xcodebuild', '-project', source / 'WebDriverAgent.xcodeproj', '-scheme',
                'WebDriverAgentRunner', '-sdk', 'iphonesimulator', '-destination',
                'generic/platform=iOS Simulator', '-derivedDataPath', build,
                'CODE_SIGNING_ALLOWED=NO', 'build-for-testing')
            template = next((build / 'Build/Products').glob('*.xctestrun'))
            with open(work / 'host.log', 'w') as log:
                host = subprocess.Popen([str(ROOT / 'bin/infercat'), 'serve', '--data-dir',
                    str(work / 'host'), '--upstream', engine, '--slots', '1', '--name',
                    '096 isolated host', '--console', 'off'], stdout=log, stderr=log)
            until(lambda: cli('status'), 'own host')
            invite = json.loads(cli('keys', 'add', 'ios-proof', '--json', '--max-output-tokens', '96'))['invite']
            for lang in ['en', 'zh']:
                print('Booting real iOS simulator:', lang, flush=True)
                device = run('xcrun', 'simctl', 'create', 'Infercat 096 ' + lang, DEVICE, RUNTIME)
                devices.append(device)
                run('xcrun', 'simctl', 'boot', device)
                run('xcrun', 'simctl', 'bootstatus', device, '-b')
                for key, flag, value in [('AppleLanguages', '-array', 'en' if lang == 'en' else 'zh-Hans'),
                                         ('AppleLocale', '-string', 'en_US' if lang == 'en' else 'zh_CN')]:
                    run('xcrun', 'simctl', 'spawn', device, 'defaults', 'write', 'NSGlobalDomain', key, flag, value)
                run('xcrun', 'simctl', 'shutdown', device)
                run('xcrun', 'simctl', 'boot', device)
                run('xcrun', 'simctl', 'bootstatus', device, '-b')
                with socket.socket() as sock:
                    sock.bind(('127.0.0.1', 0))
                    port = sock.getsockname()[1]
                config = plistlib.loads(template.read_bytes())
                config['WebDriverAgentRunner'].setdefault('EnvironmentVariables', {}).update(
                    USE_PORT=str(port), USE_IP='127.0.0.1')
                launch = template.with_name('096-' + lang + '.xctestrun')
                launch.write_bytes(plistlib.dumps(config))
                with open(work / ('runner-' + lang + '.log'), 'w') as log:
                    runner = subprocess.Popen(['xcodebuild', 'test-without-building', '-xctestrun',
                        str(launch), '-destination', 'id=' + device], stdout=log, stderr=log, env=native_env())
                Phone(device, port, lang, out).proof(invite, checks)
                stop(runner)
                run('xcrun', 'simctl', 'shutdown', device)
        except Exception as error:
            result['failed'].append(str(error))
            raise
        finally:
            stop(runner)
            if host:
                run(str(ROOT / 'bin/infercat'), 'keys', 'revoke', 'ios-proof', '--yes',
                    '--data-dir', work / 'host', check=False)
            stop(host)
            for device in devices:
                run('xcrun', 'simctl', 'shutdown', device, check=False)
                run('xcrun', 'simctl', 'delete', device, check=False)
            (out / 'result.json').write_text(json.dumps(result, indent=2, ensure_ascii=False) + '\n')
            print(f"iOS proof: {len(checks)} passed / {len(result['failed'])} failed / {len(result['skipped'])} skipped", flush=True)


if __name__ == '__main__':
    main()
