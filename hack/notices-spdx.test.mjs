import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { lockedPackages, normalizePackages, packageLicenses, selectLicenses } from './notices-spdx.mjs';
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

const canvasLicense = "MIT License\n\nCopyright (c) 2020 lynweklm@gmail.com\n\nPermission is hereby granted, free of charge, to any person obtaining a copy\nof this software and associated documentation files (the \"Software\"), to deal\nin the Software without restriction, including without limitation the rights\nto use, copy, modify, merge, publish, distribute, sublicense, and/or sell\ncopies of the Software, and to permit persons to whom the Software is\nfurnished to do so, subject to the following conditions:\n\nThe above copyright notice and this permission notice shall be included in all\ncopies or substantial portions of the Software.\n\nTHE SOFTWARE IS PROVIDED \"AS IS\", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR\nIMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,\nFITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE\nAUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER\nLIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,\nOUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE\nSOFTWARE.\n";
function nativeFixture() {
  const root = mkdtempSync(join(tmpdir(), 'notices-native-'));
  const parent = join(root, 'canvas'), native = join(root, 'native');
  for (const path of [parent, native]) {
    mkdirSync(path); writeFileSync(join(path, 'package.json'), '{"version":"1.0.8"}');
  }
  writeFileSync(join(parent, 'LICENSE'), canvasLicense);
  const names = ['@napi-rs/canvas-darwin-arm64', '@napi-rs/canvas-linux-x64-gnu'];
  const lock = {
    lockfileVersion: '9.0', importers: { '.': { dependencies: { 'pdfjs-dist': { version: '6.3.289' } }, devDependencies: { harness: { version: '1.0.0' } } } },
    packages: { 'pdfjs-dist@6.3.289': {}, '@napi-rs/canvas@1.0.8': {}, ...Object.fromEntries(names.map((name) => [`${name}@1.0.8`, {}])) },
    snapshots: {
      'pdfjs-dist@6.3.289': { optionalDependencies: { '@napi-rs/canvas': '1.0.8' } },
      '@napi-rs/canvas@1.0.8': { optionalDependencies: Object.fromEntries(names.map((name) => [name, '1.0.8'])) },
      ...Object.fromEntries(names.map((name) => [`${name}@1.0.8`, { optional: true }])),
    },
  };
  const installed = { MIT: [
    { name: 'pdfjs-dist', versions: ['6.3.289'], paths: [] },
    { name: '@napi-rs/canvas', versions: ['1.0.8'], paths: [parent] },
    { name: names[0], versions: ['1.0.8'], paths: [native] },
    { name: 'unrelated-dev-package', versions: ['9.0.0'], paths: [] },
  ] };
  return { root, parent, native, names, lock, installed };
}
function renderLocked(lock, installed) {
  const { groups, familyText } = lockedPackages(lock, installed);
  return { ...normalizePackages(groups, allowed), familyText };
}
test('declared optional production closure is identical with macOS, Linux or no native variant installed', () => {
  const f = nativeFixture();
  try {
    const mac = renderLocked(f.lock, f.installed);
    assert.match(mac.tsv, /canvas-darwin-arm64/); assert.match(mac.tsv, /canvas-linux-x64-gnu/);
    assert.doesNotMatch(mac.tsv, /harness|unrelated-dev/);
    assert.equal(mac.familyText, canvasLicense);
    // A native package may omit LICENSE, or supply the identical reviewed family text.
    writeFileSync(join(f.native, 'LICENSE'), canvasLicense);
    assert.deepEqual(renderLocked(f.lock, f.installed), mac);
    f.installed.MIT[2].name = f.names[1];
    assert.deepEqual(renderLocked(f.lock, f.installed), mac);
    rmSync(f.native, { recursive: true }); f.installed.MIT.splice(2, 1);
    assert.deepEqual(renderLocked(f.lock, f.installed), mac);
  } finally { rmSync(f.root, { recursive: true }); }
});
test('native text or metadata drift fails closed instead of changing inventory by platform', () => {
  const f = nativeFixture();
  try {
    writeFileSync(join(f.native, 'LICENSE'), 'different text');
    assert.throws(() => renderLocked(f.lock, f.installed), /native LICENSE differs/);
    rmSync(join(f.native, 'LICENSE'));
    f.installed.GPL = [f.installed.MIT.splice(2, 1)[0]];
    assert.throws(() => renderLocked(f.lock, f.installed), /licence changed/);
    delete f.installed.GPL;
    writeFileSync(join(f.parent, 'LICENSE'), 'different family');
    assert.throws(() => renderLocked(f.lock, f.installed), /family LICENSE changed/);
  } finally { rmSync(f.root, { recursive: true }); }
});
test('unresolved or unreviewed optional edges and unsupported locks cannot silently disappear', () => {
  const f = nativeFixture();
  try {
    const canvas = f.lock.snapshots['@napi-rs/canvas@1.0.8'];
    canvas.optionalDependencies.other = '2.0.0';
    assert.throws(() => renderLocked(f.lock, f.installed), /Missing locked package/);
    f.lock.packages['other@2.0.0'] = {}; f.lock.snapshots['other@2.0.0'] = { optional: true };
    assert.throws(() => renderLocked(f.lock, f.installed), /No licence metadata/);
    f.lock.lockfileVersion = '10.0';
    assert.throws(() => renderLocked(f.lock, f.installed), /Unsupported pnpm lockfile/);
  } finally { rmSync(f.root, { recursive: true }); }
});
test('peer-qualified snapshots are traversed separately but package notices are deduplicated', () => {
  const lock = { lockfileVersion: '9.0', importers: { '.': { dependencies: { a: { version: '1(peer@1)' }, b: { version: '1' } } } },
    packages: { 'a@1': {}, 'b@1': {}, 'peer@1': {}, 'peer@2': {} }, snapshots: {
      'a@1(peer@1)': { dependencies: { peer: '1' } }, 'a@1(peer@2)': { dependencies: { peer: '2' } },
      'b@1': { dependencies: { a: '1(peer@2)' } }, 'peer@1': {}, 'peer@2': {},
    } };
  const installed = { MIT: [{ name: 'a', versions: ['1'] }, { name: 'b', versions: ['1'] }, { name: 'peer', versions: ['1', '2'] }] };
  assert.equal(renderLocked(lock, installed).tsv, 'a\t1\tMIT\nb\t1\tMIT\npeer\t1\tMIT\npeer\t2\tMIT\n');
});
test('workspace links traverse locked production imports without dev packages or cycles',()=>{
 const lock={lockfileVersion:'9.0',importers:{'.':{dependencies:{console:{version:'link:../console'}}},'../console':{dependencies:{runtime:{version:'1'},root:{version:'link:../web'}},devDependencies:{dev:{version:'9'}}},'../web':{dependencies:{console:{version:'link:../console'}}}},packages:{'runtime@1':{}},snapshots:{'runtime@1':{}}};
 const installed={MIT:[{name:'runtime',versions:['1'],paths:[]}]};assert.match(renderLocked(lock,installed).tsv,/runtime/);assert.doesNotMatch(renderLocked(lock,installed).tsv,/console|root|dev/);
 delete lock.importers['../console'];assert.throws(()=>renderLocked(lock,installed),/Missing locked workspace/);
});
