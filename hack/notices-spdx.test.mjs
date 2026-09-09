import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { normalizePackages, packageLicenses, selectLicenses } from './notices-spdx.mjs';
const allowed = new Set(['MIT', 'Zlib', 'BSD-2-Clause']);
test('OR selects the first allowed alternative, including nested branches', () => {
  assert.deepEqual(selectLicenses('MIT OR GPL-3.0-or-later', allowed), ['MIT']);
  assert.deepEqual(selectLicenses('GPL-3.0-or-later OR (Zlib AND MIT)', allowed), ['Zlib', 'MIT']);
  assert.deepEqual(selectLicenses('(MIT OR Zlib) AND BSD-2-Clause', allowed), ['MIT', 'BSD-2-Clause']);
});
test('AND keeps every obligation and follows SPDX precedence', () => {
  assert.deepEqual(selectLicenses('MIT AND Zlib', allowed), ['MIT', 'Zlib']);
  assert.deepEqual(selectLicenses('GPL-3.0-or-later AND MIT OR Zlib', allowed), ['Zlib']);
  assert.throws(() => selectLicenses('MIT AND GPL-3.0-or-later', allowed));
});
test('non-SPDX overrides are exact package, version and metadata matches', () => {
  assert.deepEqual(packageLicenses('duck', '0.1.12', 'BSD', allowed), ['BSD-2-Clause']);
  for (const args of [['duck','0.1.13','BSD'],['other','0.1.12','BSD'],['duck','0.1.12','UNKNOWN']]) {
    assert.throws(() => packageLicenses(...args, allowed));
  }
});
test('malformed, unsupported, or wholly refused expressions fail even after an allowed left branch', () => {
  for (const value of ['', 'MIT OR', '(MIT', 'MIT OR (Zlib AND)', 'MIT WITH exception', 'GPL-3.0-only', 'MIT / Zlib', 'MIT Zlib', 'MIT OR MIT)']) {
    assert.throws(() => selectLicenses(value, allowed), value);
  }
});
test('the generator includes both conjunctive texts and refuses missing text provenance', () => {
  const root = mkdtempSync(join(tmpdir(), 'notices-spdx-'));
  try {
    mkdirSync(join(root, 'lib/zlib'), { recursive: true });
    writeFileSync(join(root, 'package.json'), '{"version":"1.0.11"}');
    writeFileSync(join(root, 'LICENSE'), 'Fixture MIT copyright and grant.\n');
    writeFileSync(join(root, 'lib/zlib/README'), 'Fixture Zlib copyright and restrictions.\n');
    const data = { '(MIT AND Zlib)': [{ name: 'pako', versions: ['1.0.11'], paths: [root] }] };
    const result = normalizePackages(data, allowed);
    assert.equal(result.tsv, 'pako\t1.0.11\tMIT\npako\t1.0.11\tZlib\n');
    assert.match(result.markdown, /Fixture MIT copyright and grant/);
    assert.match(result.markdown, /Fixture Zlib copyright and restrictions/);
    rmSync(join(root, 'lib/zlib/README'));
    assert.throws(() => normalizePackages(data, allowed));
    assert.throws(() => normalizePackages({ 'MIT AND Zlib': [{ name: 'unreviewed', versions: ['1'], paths: [] }] }, allowed));
  } finally { rmSync(root, { recursive: true }); }
});

test('missing package versions cannot silently disappear from the notice inventory', () => {
  assert.throws(() => normalizePackages({ MIT: [{ name: 'missing-version' }] }, allowed));
});
