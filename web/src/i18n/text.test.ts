import { afterEach, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { brotliDecompressSync } from 'node:zlib';
import { en } from './en';
import { zh } from './zh';
import { appLanguage, tr } from './text';
import { describeError, GatewayError } from '../api';
import { KEYS } from '../storage';
afterEach(() => vi.unstubAllGlobals());
it('preserves named placeholders across both complete tables', () => {
  expect(Object.keys(zh).sort()).toEqual(Object.keys(en).sort());
  for (const key of Object.keys(en) as (keyof typeof en)[]) {
    const names = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
    expect(names(zh[key]), key).toEqual(names(en[key]));
  }
});
it('keeps English plurals and formats Chinese as whole sentences', () => {
  expect(tr('app_kept_chat', { count: 1, host: 'Max' }, 'en')).toBe('Your 1 chat with Max is still on this device.');
  expect(tr('app_kept_chats', { count: 2, host: 'Max' }, 'en')).toBe('Your 2 chats with Max are still on this device.');
  expect(tr('app_meter_message_left', { count: 1 }, 'zh')).toBe(zh.app_meter_message_left.replace('{count}', '1'));
  expect(tr('app_meter_messages_left', { count: 19 }, 'zh')).toBe(zh.app_meter_messages_left.replace('{count}', '19'));
  expect(tr('app_delete_chat', { title: '{private} <title>' }, 'zh')).toContain('{private} <title>');
});
it('reads the saved language for errors from modules already imported', () => {
  let saved: string | null = null;
  vi.stubGlobal('navigator', { language: 'zh-CN' });
  vi.stubGlobal('localStorage', { getItem: (key: string) => key === KEYS.language ? saved : null });
  expect(appLanguage()).toBe('zh');
  const err = new GatewayError(403, 'key_paused', 'permission_error', 'host evidence');
  expect(describeError(err, 'Max').title).toBe(zh.app_your_invite_is_paused);
  saved = JSON.stringify('en');
  expect(appLanguage()).toBe('en');
  expect(describeError(err, 'Max').title).toBe(en.app_your_invite_is_paused);
});
it('contains every table CJK glyph in the shipped WOFF2 cmap', () => {
  // Read the untransformed cmap: w3.org/TR/WOFF2/#table_dir_format. No font-tool dependency in tests.
  const file = readFileSync(new URL('../../public/fonts/noto-sans-sc.woff2', import.meta.url));
  expect(file.toString('ascii', 0, 4)).toBe('wOF2');
  let at = 48, offset = 0, cmapOffset = -1, cmapLength = 0;
  function base128(): number {
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
  const cmap = data.subarray(cmapOffset, cmapOffset + cmapLength), maps: Buffer[] = [];
  for (let i = 0; i < cmap.readUInt16BE(2); i++) {
    const table = cmap.subarray(cmap.readUInt32BE(8 + i * 8));
    if (table.readUInt16BE(0) === 4) maps.push(table);
  }
  expect(maps.length).toBeGreaterThan(0);
  function has(c: Buffer, point: number): boolean {
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
  const points = new Set([...Object.values(en), ...Object.values(zh)].join('').match(/[\u3400-\u9fff]/g));
  expect([...points].filter((c) => !maps.some((m) => has(m, c.charCodeAt(0))))).toEqual([]);
});
