import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { brotliDecompressSync } from 'node:zlib';
import { copy } from './copy';
it('contains every table CJK glyph in the shipped WOFF2 cmap', () => {
  // Read the untransformed cmap: w3.org/TR/WOFF2/#table_dir_format. No font-tool dependency in tests.
  const file = readFileSync(new URL('./public/fonts/noto-sans-sc.woff2', import.meta.url));
  expect(file.toString('ascii', 0, 4)).toBe('wOF2');
  let at = 48, offset = 0, cmapOffset = -1, cmapLength = 0;
  function base128() {
    let value = 0, byte;
    do { byte = file.readUInt8(at++); value = value * 128 + (byte & 127); } while (byte & 128);
    return value;
  }
  for (let i = 0; i < file.readUInt16BE(12); i++) {
    const flags = file.readUInt8(at++), tag = flags & 63, version = flags >> 6;
    const custom = tag === 63 ? file.toString('ascii', at, at += 4) : '';
    const original = base128();
    const glyf = tag === 10 || tag === 11 || custom === 'glyf' || custom === 'loca';
    const length = (glyf ? version === 0 : version !== 0) ? base128() : original;
    if (tag === 0 || custom === 'cmap') { cmapOffset = offset; cmapLength = original; }
    offset += length;
  }
  expect(cmapOffset).toBeGreaterThanOrEqual(0);
  const data = brotliDecompressSync(file.subarray(at, at + file.readUInt32BE(20)));
  const cmap = data.subarray(cmapOffset, cmapOffset + cmapLength), maps = [];
  for (let i = 0; i < cmap.readUInt16BE(2); i++) {
    const table = cmap.subarray(cmap.readUInt32BE(8 + i * 8));
    if (table.readUInt16BE(0) === 4) maps.push(table);
  }
  expect(maps.length).toBeGreaterThan(0);
  function has(c, point) {
    const count = c.readUInt16BE(6) / 2, ends = 14, starts = ends + count * 2 + 2, deltas = starts + count * 2, ranges = deltas + count * 2;
    for (let i = 0; i < count; i++) {
      const start = c.readUInt16BE(starts + i * 2), end = c.readUInt16BE(ends + i * 2);
      if (point < start || point > end) continue;
      const delta = c.readInt16BE(deltas + i * 2), range = c.readUInt16BE(ranges + i * 2);
      const glyph = range ? c.readUInt16BE(ranges + i * 2 + range + (point - start) * 2) : point;
      return glyph !== 0 && ((glyph + delta) & 65535) !== 0;
    }
    return false;
  }
  const points = new Set(Object.values(copy).flat().join('').match(/[\u3400-\u9fff]/g));
  expect([...points].filter((c) => !maps.some((m) => has(m, c.charCodeAt(0))))).toEqual([]);
});
