"""Constructed signature envelopes only: no Apple certificate or notarization is claimed."""
import contextlib
import io
import json
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import verify_macos as guard


def macho(cms=b'\x30\x00Developer ID Application: constructed fixture', *, signed=True):
    # The CodeDirectory-only form models Go's ad-hoc signature; CMS is deliberately synthetic.
    directory = struct.pack('>IIII', 0xFADE0C02, 16, 0x20400, 0 if cms else 2)
    entries = [(0, directory)]
    if cms:
        entries.append((0x10000, struct.pack('>II', 0xFADE0B01, 8 + len(cms)) + cms))
    start = 12 + len(entries) * 8
    index, blobs = b'', b''
    for slot, blob in entries:
        index += struct.pack('>II', slot, start + len(blobs))
        blobs += blob
    signature = struct.pack('>III', 0xFADE0CC0, start + len(blobs), len(entries)) + index + blobs
    header = struct.pack('<IIIIIIII', 0xFEEDFACF, 0x100000C, 0, 2, int(signed), 16 if signed else 0, 0, 0)
    return header + (struct.pack('<IIII', 0x1D, 16, 48, len(signature)) + signature if signed else b'')


class SignatureGuardTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'dist').mkdir()
        self.env = patch.dict(guard.os.environ, {'MACOS_SIGN_P12': '', 'MACOS_SIGNING_EXPECTED': ''})
        self.env.start()
        self.addCleanup(self.env.stop)

    def artifacts(self, binary=macho(), identity='cli'):
        rows = []
        for arch in ['amd64', 'arm64']:
            path = f'dist/cli_{arch}'
            (self.root / path).write_bytes(binary)
            rows.append(dict(goos='darwin', goarch=arch, type='Binary', extra={'ID': identity}, path=path))
        (self.root / 'dist/artifacts.json').write_text(json.dumps(rows))

    def run_guard(self):
        with contextlib.redirect_stdout(io.StringIO()) as output:
            guard.verify(self.root)
        return output.getvalue()

    def test_disabled_needs_no_artifacts(self):
        self.assertEqual(self.run_guard(), 'signing disabled\n')
        with patch.dict(guard.os.environ, {'MACOS_SIGNING_EXPECTED': 'false'}):
            self.assertEqual(self.run_guard(), 'signing disabled\n')

    def test_local_certificate_and_workflow_boolean_each_require_signatures(self):
        for variable in ['MACOS_SIGN_P12', 'MACOS_SIGNING_EXPECTED']:
            with self.subTest(variable=variable), patch.dict(guard.os.environ, {variable: 'true'}):
                with self.assertRaises(FileNotFoundError):
                    self.run_guard()

    def test_unsigned_adhoc_and_unrelated_cms_refused(self):
        for binary in [macho(signed=False), macho(cms=b''), macho(cms=b'\x30\x00Other Certificate')]:
            with self.subTest(binary=binary[:32]), patch.dict(guard.os.environ, {'MACOS_SIGNING_EXPECTED': 'true'}):
                self.artifacts(binary)
                with self.assertRaises(ValueError):
                    self.run_guard()

    def test_marker_outside_cms_is_not_a_signature(self):
        self.assertFalse(guard.developer_id_cms(macho(cms=b'') + b'Developer ID Application: decoy'))

    def test_missing_wrong_and_duplicate_artifacts_refused(self):
        with patch.dict(guard.os.environ, {'MACOS_SIGNING_EXPECTED': 'true'}):
            self.artifacts(identity='infercat')
            with self.assertRaisesRegex(ValueError, 'expected cli Darwin'):
                self.run_guard()
            self.artifacts()
            p = self.root / 'dist/artifacts.json'
            rows = json.loads(p.read_text())
            for changed in [rows[:1], [rows[0], rows[0]]]:
                p.write_text(json.dumps(changed))
                with self.assertRaisesRegex(ValueError, 'expected cli Darwin'):
                    self.run_guard()

    def test_truncated_and_malformed_lengths_refused(self):
        original = macho()
        for size in range(len(original)):
            with self.subTest(size=size), self.assertRaises((ValueError, struct.error)):
                guard.developer_id_cms(original[:size])
        for offset, value, endian in [(36, 0, '<'), (40, 0, '<'), (52, 8, '>'), (56, 0xFFFFFFFF, '>'), (64, 0, '>')]:
            broken = bytearray(original)
            struct.pack_into(endian + 'I', broken, offset, value)
            with self.subTest(offset=offset), self.assertRaises((ValueError, struct.error)):
                guard.developer_id_cms(broken)

    def test_constructed_cms_presence_on_linux(self):
        self.artifacts()
        with patch.dict(guard.os.environ, {'MACOS_SIGNING_EXPECTED': 'true'}), patch.object(guard.sys, 'platform', 'linux'):
            self.assertIn('signature guard passed', self.run_guard())

    def test_macos_also_checks_authority_and_codesign(self):
        self.artifacts()
        with patch.dict(guard.os.environ, {'MACOS_SIGNING_EXPECTED': 'true'}), patch.object(guard.sys, 'platform', 'darwin'):
            with patch.object(guard.subprocess, 'check_output', return_value='Signature=adhoc'):
                with self.assertRaisesRegex(ValueError, 'authority'):
                    self.run_guard()
            with patch.object(guard.subprocess, 'check_output', side_effect=subprocess.CalledProcessError(1, 'codesign')):
                with self.assertRaises(subprocess.CalledProcessError):
                    self.run_guard()
            with patch.object(guard.subprocess, 'check_output', return_value='Authority=Developer ID Application: fixture') as codesign:
                self.assertIn('signature guard passed', self.run_guard())
                self.assertEqual(sum(call.args[0][1] == '--verify' for call in codesign.call_args_list), 2)


if __name__ == '__main__':
    unittest.main()
