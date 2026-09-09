// Shared deterministic raster recipe for the chosen loaf mark (web/public/favicon.svg).
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { join } from 'node:path';
const root = fileURLToPath(new URL('../', import.meta.url));
export const ICONS = [
  ['favicon.png', 96, '#ffffff', 0],
  ['apple-touch-icon.png', 180, '#ffffff', 0.10],
  ['icon-192.png', 192, '#ffffff', 0.06],
  ['icon-512.png', 512, '#ffffff', 0.06],
  ['icon-maskable-512.png', 512, '#ffffff', 0.20],
  ['icon-maskable-192.png', 192, '#ffffff', 0.20],
  ['icon-monochrome-512.png', 512, null, 0.20],
];
export function iconHTML(mark, edge, ground, pad) {
  const inner = Math.round(edge * (1 - 2 * pad));
  return `<!doctype html><meta charset="utf-8"><style>
html,body{margin:0;width:${edge}px;height:${edge}px;overflow:hidden;background:${ground ?? 'transparent'}}
body{display:grid;place-items:center}svg{width:${inner}px;height:${inner}px;display:block}
</style>${mark}`;
}
export async function renderIcons() {
  const { chromium } = createRequire(join(root, 'web/package.json'))('playwright');
  const browser = await chromium.launch();
  try {
    const mark = readFileSync(join(root, 'web/public/favicon.svg'), 'utf8');
    for (const [name, edge, ground, pad] of ICONS.filter(([name]) => ['icon-192.png', 'icon-512.png', 'icon-maskable-192.png', 'icon-monochrome-512.png'].includes(name))) {
      const context = await browser.newContext({ viewport: { width: edge, height: edge }, deviceScaleFactor: 1, colorScheme: 'light' });
      const page = await context.newPage(); await page.setContent(iconHTML(mark, edge, ground, pad));
      await page.screenshot({ path: join(root, 'web/public', name), omitBackground: ground === null });
      await context.close(); console.log(`${name}: ${edge}x${edge}, pad ${pad}, ${ground === null ? 'alpha silhouette' : 'paper'}`);
    }
  } finally { await browser.close(); }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await renderIcons();
