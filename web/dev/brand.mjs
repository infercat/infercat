// Renders every raster the product ships from two sources of truth — the SVG mark in
// public/favicon.svg and the real connect screen served from web/dist — so a rename or a new mark
// is one re-run, not a hunt through image files:
//
//   make brand                      # = make web && cd web && node dev/brand.mjs
//   BRAND_PORT=6832 pnpm brand      # port for the vite preview it starts
//
// Writes, in public/: favicon.png (96), apple-touch-icon.png (180, on the page ground — iOS ignores
// alpha), icon-192.png, icon-512.png, icon-maskable-512.png (mark inside the safe zone, on the page
// ground) and og.png (1200×630: the name, the one sentence, and the connect card as it renders);
// and ../.github/social-preview.png (1280×640, the same design at GitHub's size). The name and the
// sentence come from src/product.ts; the card is a screenshot, never a replica. No network.
import { spawn } from 'node:child_process';
import { readFileSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, '..');
const pub = join(web, 'public');
const PORT = Number(process.env.BRAND_PORT ?? 6832);
const BASE = `http://127.0.0.1:${PORT}`;

const product = readFileSync(join(web, 'src/product.ts'), 'utf8');
const NAME = /PRODUCT_NAME = '([^']+)'/.exec(product)?.[1] ?? 'app';
const DESCRIPTION = /DESCRIPTION =\s*'([^']+)'/.exec(product)?.[1] ?? '';
const mark = readFileSync(join(pub, 'favicon.svg'), 'utf8');

// Mirror of the light :root tokens in src/styles.css. The cards are always light: a social card is
// shown on the reader's timeline, not in their colour scheme.
const P = { bg: '#fdfcfb', raised: '#ffffff', text: '#1b1a18', muted: '#6f6b64', faint: '#716c65', line: '#e6e2dc' };
const FONT = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif";

// name, edge, ground (null = transparent), padding as a fraction of the edge on each side
const ICONS = [
  ['favicon.png', 96, null, 0],
  ['apple-touch-icon.png', 180, P.bg, 0.1],
  ['icon-192.png', 192, null, 0],
  ['icon-512.png', 512, null, 0],
  ['icon-maskable-512.png', 512, P.bg, 0.2],
];

function iconPage(edge, ground, pad) {
  const inner = Math.round(edge * (1 - 2 * pad));
  return `<!doctype html><meta charset="utf-8"><style>
    html, body { margin: 0; width: ${edge}px; height: ${edge}px; overflow: hidden; background: ${ground ?? 'transparent'}; }
    body { display: grid; place-items: center; }
    svg { width: ${inner}px; height: ${inner}px; display: block; }
  </style>${mark}`;
}

/** The card: the mark, the name, the sentence, three true words, and the connect screen itself. */
function cardPage(w, h, cardPng) {
  return `<!doctype html><meta charset="utf-8"><style>
    html, body { margin: 0; width: ${w}px; height: ${h}px; overflow: hidden; background: ${P.bg}; font-family: ${FONT}; }
    .left { position: absolute; left: 6%; top: 11%; width: 44%; }
    .mark { width: 52px; height: 52px; }
    h1 { font-size: 54px; letter-spacing: -0.02em; line-height: 1.1; margin: 22px 0 18px; font-weight: 600; color: ${P.text}; }
    p { font-size: 23px; line-height: 1.45; color: ${P.muted}; margin: 0; }
    .facts { position: absolute; left: 6%; bottom: 9%; font-size: 17px; color: ${P.faint}; }
    .shot {
      position: absolute; left: 55%; top: 10%; width: 41%; height: 100%;
      border: 1px solid ${P.line}; border-bottom: 0; border-radius: 16px 16px 0 0; background: ${P.bg};
      box-shadow: 0 12px 40px rgba(27, 26, 24, 0.10); overflow: hidden; padding: 30px 30px 0;
    }
    .shot img { width: 100%; display: block; }
  </style>
  <div class="left">
    <div class="mark">${mark}</div>
    <h1>${NAME}</h1>
    <p>${DESCRIPTION}</p>
  </div>
  <div class="facts">Self-hosted · end-to-end encrypted · MIT</div>
  <div class="shot"><img src="data:image/png;base64,${cardPng.toString('base64')}" alt=""></div>`;
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function waitFor(url) {
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(url)).ok) return;
    } catch {
      /* not up yet */
    }
    await sleep(200);
  }
  throw new Error(`nothing answered at ${url} — run \`make web\` first`);
}

function report(file) {
  console.log(`  ${file.replace(`${web}/`, '')}  ${Math.round(statSync(file).size / 1024)} KB`);
}

async function main() {
  const browser = await chromium.launch();

  // 1. Icons, from the mark.
  for (const [name, edge, ground, pad] of ICONS) {
    const ctx = await browser.newContext({ viewport: { width: edge, height: edge }, deviceScaleFactor: 1, colorScheme: 'light' });
    const page = await ctx.newPage();
    await page.setContent(iconPage(edge, ground, pad));
    const file = join(pub, name);
    await page.screenshot({ path: file, omitBackground: ground === null });
    report(file);
    await ctx.close();
  }

  // 2. The connect card, as the app renders it (light, 2× for crispness).
  const preview = spawn(process.execPath, ['node_modules/vite/bin/vite.js', 'preview', '--port', String(PORT), '--strictPort'], {
    cwd: web,
    stdio: ['ignore', 'ignore', 'inherit'],
  });
  try {
    await waitFor(`${BASE}/`);
    const ctx = await browser.newContext({ viewport: { width: 560, height: 900 }, deviceScaleFactor: 2, colorScheme: 'light' });
    const page = await ctx.newPage();
    await page.goto(`${BASE}/`);
    await page.waitForSelector('.connect-card');
    const cardPng = await page.locator('.connect-card').screenshot();
    await ctx.close();

    // 3. The two cards, composed from the pieces above.
    for (const [file, w, h] of [
      [join(pub, 'og.png'), 1200, 630],
      [join(web, '..', '.github', 'social-preview.png'), 1280, 640],
    ]) {
      const c = await browser.newContext({ viewport: { width: w, height: h }, deviceScaleFactor: 1, colorScheme: 'light' });
      const p = await c.newPage();
      await p.setContent(cardPage(w, h, cardPng));
      await p.evaluate(() => document.fonts.ready);
      await p.screenshot({ path: file });
      report(file);
      await c.close();
    }
  } finally {
    preview.kill('SIGTERM');
  }
  await browser.close();
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
