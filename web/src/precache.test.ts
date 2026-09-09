import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { PNG } from 'pngjs';
import { precacheURLs, staticAssetPath, publicAssetURL } from '../dev/precache';
it('collects hashed entry/dynamic assets and public shell files deterministically', () => {
  expect(precacheURLs({ a: { file: 'assets/app-1.js', css: ['assets/style-1.css'] }, b: { file: 'assets/chat-2.js', assets: ['assets/worker-3.mjs'] } }, ['fonts/app.woff2', 'assets/style-1.css'])).toEqual([
    '/assets/app-1.js', '/assets/chat-2.js', '/assets/style-1.css', '/assets/worker-3.mjs', '/fonts/app.woff2', '/index.html',
  ]);
});
it('refuses network URLs, traversal, and query-bearing precache entries', () => {
  for (const file of ['https://host/me', '../me', 'api?invite=secret', '/absolute']) expect(() => precacheURLs({ a: { file } }, [])).toThrow();
});
it.each([['icon-192.png', 192], ['icon-512.png', 512], ['icon-maskable-192.png', 192], ['icon-maskable-512.png', 512], ['icon-monochrome-512.png', 512]] as const)('audits %s dimensions, margin and mask geometry', (name, size) => {
  const png = PNG.sync.read(readFileSync(new URL(`../public/${name}`, import.meta.url)));
  expect([png.width, png.height]).toEqual([size, size]);
  const mono = name.includes('monochrome'), mask = name.includes('maskable') || mono;
  let ink = 0;
  for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) {
    const at = (y * size + x) * 4;
    const drawn = mono ? png.data[at + 3]! > 64 : png.data[at]! < 200;
    if (!drawn) continue; ink++;
    if (mask) expect(Math.hypot(x + 0.5 - size / 2, y + 0.5 - size / 2)).toBeLessThan(size * 0.4);
    else expect(Math.min(x, y, size - 1 - x, size - 1 - y)).toBeGreaterThanOrEqual(Math.floor(size * 0.06) - 1);
  }
  expect(ink).toBeGreaterThan(size * size * 0.08);
  expect(png.data[3]).toBe(mono ? 0 : 255);
});

it('public shell URLs change with bytes, so an older worker cannot serve a new shell stale assets', () => {
  expect(staticAssetPath('fonts/app.woff2', 'old')).not.toEqual(staticAssetPath('fonts/app.woff2', 'new'));
  expect(staticAssetPath('fonts/app.woff2', 'old')).toEqual(staticAssetPath('fonts/app.woff2', 'old'));
  expect(publicAssetURL('icon-192.png')).toMatch(/^\/static\/[a-f0-9]{16}\/icon-192\.png$/);
  expect(publicAssetURL('me')).toBeUndefined(); expect(publicAssetURL('favicon.svg?invite=secret')).toBeUndefined();
});
