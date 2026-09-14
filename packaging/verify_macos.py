"""Silent-skip guard for GoReleaser's thin Darwin binaries, not Apple trust validation."""
import json
import os
from pathlib import Path
import struct
import subprocess
import sys


def part(data, offset, size):
    if offset < 0 or size < 0 or offset + size > len(data):
        raise ValueError('truncated signature or Mach-O data')
    return data[offset:offset + size]


def developer_id_cms(data):
    if part(data, 0, 4) != b'\xcf\xfa\xed\xfe':
        raise ValueError('expected a thin little-endian 64-bit Mach-O')
    count, size = struct.unpack('<II', part(data, 16, 8))
    commands = part(data, 32, size)
    offset, signatures = 0, []
    for _ in range(count):
        command, size = struct.unpack('<II', part(commands, offset, 8))
        if size < 8:
            raise ValueError('invalid Mach-O command size')
        entry = part(commands, offset, size)
        if command == 0x1D:  # LC_CODE_SIGNATURE; Go arm64 also has this when ad-hoc signed.
            if size != 16:
                raise ValueError('invalid signature command')
            start, length = struct.unpack('<II', entry[8:])
            if start < 32 + len(commands):
                raise ValueError('signature overlaps load commands')
            signatures.append(part(data, start, length))
        offset += size
    if offset != len(commands) or len(signatures) != 1:
        raise ValueError('expected one embedded signature')
    blob = signatures[0]
    magic, length, count = struct.unpack('>III', part(blob, 0, 12))
    if magic != 0xFADE0CC0:
        raise ValueError('invalid signature superblob')
    blob = part(blob, 0, length)
    index = part(blob, 12, count * 8)
    cms = []
    for i in range(count):
        slot, start = struct.unpack_from('>II', index, i * 8)
        if start < 12 + len(index):
            raise ValueError('signature slot overlaps index')
        magic, length = struct.unpack('>II', part(blob, start, 8))
        if length < 8:
            raise ValueError('invalid signature slot length')
        payload = part(blob, start + 8, length - 8)
        if slot == 0x10000:  # CSSLOT_SIGNATURESLOT: CMS, absent from Go's ad-hoc signature.
            if magic != 0xFADE0B01:
                raise ValueError('invalid CMS wrapper')
            cms.append(payload)
    return (len(cms) == 1 and cms[0].startswith(b'\x30')
            and b'Developer ID Application:' in cms[0])


def verify(root=Path('.')):
    required = bool(os.environ.get('MACOS_SIGN_P12')) or os.environ.get('MACOS_SIGNING_EXPECTED') == 'true'
    if not required:
        print('signing disabled')
        return
    artifacts = json.loads((root / 'dist/artifacts.json').read_text())
    binaries = [a for a in artifacts if a.get('goos') == 'darwin'
                and a.get('type') == 'Binary' and a.get('extra', {}).get('ID') == 'cli']
    if sorted(a.get('goarch', '') for a in binaries) != ['amd64', 'arm64']:
        raise ValueError('expected cli Darwin binaries for amd64 and arm64')
    for artifact in binaries:
        path = root / artifact['path']
        try:
            if not developer_id_cms(path.read_bytes()):
                raise ValueError('no Developer ID CMS signature')
        except (ValueError, struct.error) as error:
            raise ValueError(f'{path}: {error}') from error
        if sys.platform == 'darwin':
            info = subprocess.check_output(['codesign', '-dv', '--verbose=4', str(path)],
                                           stderr=subprocess.STDOUT, text=True)
            if not any(line.startswith('Authority=Developer ID Application:') for line in info.splitlines()):
                raise ValueError(f'{path}: no Developer ID authority')
            subprocess.check_output(['codesign', '--verify', '--strict', str(path)], stderr=subprocess.STDOUT)
        print(f'{path}: Developer ID signature present')
    print('darwin binaries: signature guard passed (not notarization/trust validation)')


if __name__ == '__main__':
    try:
        verify()
    except (OSError, ValueError, KeyError, TypeError, struct.error, subprocess.CalledProcessError) as error:
        sys.exit(f'macOS signature guard: {error}')
