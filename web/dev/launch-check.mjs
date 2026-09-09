// What a stranger's browser sees on the built app (web/dist), checked and photographed. Two modes:
//
//   pnpm launch-check                                  # the connect screen, from a vite preview it starts
//   INVITE=ic1.… APP=http://127.0.0.1:6831 pnpm launch-check
//                                                      # + the chat against a real host: the README recording
//
// Checks — any failure exits 1: nothing on the console at load (warnings included); title,
// description, Open Graph and Twitter metas, theme-color, manifest and apple-touch-icon links; the
// manifest parses and carries the product name; every icon and og.png is served; the connect screen
// asks no third party for anything, fonts included (038 promise 3, Protection 3); every control in
// the accessibility tree has a name; text contrast is 4.5:1 or better (3:1 for large text) in light
// scheme; Tab lands on the invite field first; no horizontal overflow at 390 px. Screenshots go to
// dev/screenshots/30-*.png (or LAUNCH_SHOTS); with INVITE, the recording goes to ../docs/media/friend-chat.gif (+ .png)
// and must stay under 2 MB. The GIF needs a full ffmpeg (`brew install ffmpeg`, or FFMPEG=path to
// one — Playwright's own ffmpeg records WebM and cannot write GIF).
import { spawn, spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
import { landingEvidence } from '../src/ui/landing/verify.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, '..');
const shots = process.env.LAUNCH_SHOTS || join(here, 'screenshots');
// Recording-only framing; the normal launch gate remains unchanged.
const demoAt = process.argv.indexOf('--demo-dir');
const demoDir = demoAt < 0 ? '' : process.argv[demoAt + 1];
if (demoAt >= 0 && !demoDir) throw new Error('--demo-dir needs an output directory');
const media = demoDir || join(web, '..', 'docs', 'media');
const PORT = Number(process.env.CHECK_PORT ?? 6833);
const APP = (process.env.APP ?? `http://127.0.0.1:${PORT}`).replace(/\/+$/, '');
const INVITE = process.env.INVITE ?? '';
const PUBLIC_ORIGIN = new URL(process.env.VITE_WEB_URL || /WebURL\s*=\s*"([^"]+)"/.exec(readFileSync(join(web, '../internal/product/product.go'), 'utf8'))?.[1] || APP).origin;
const NAME = /PRODUCT_NAME = '([^']+)'/.exec(readFileSync(join(web, 'src/product.ts'), 'utf8'))?.[1] ?? 'app';
const QUESTION = demoDir ? 'Why is the sky blue? Answer in one sentence.' : 'Why is the sky blue? Answer in three sentences.';

const problems = [];
const say = (s) => console.log(`  ${s}`);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function watch(page, label) {
  page.on('console', (m) => {
    if (m.type() === 'warning' || m.type() === 'error') problems.push(`${label}: console.${m.type()} ${m.text()}`);
  });
  page.on('pageerror', (e) => problems.push(`${label}: pageerror ${e.message}`));
}

/**
 * Every URL the page asks for, in order. Protection 3 says a friend's IP goes to the host and the
 * relay and nobody else, so the built app must ask no third party for anything — no font CDN, no
 * analytics, no map, no icon host. `thirdParty` turns that into a failure rather than a promise.
 */
function netWatch(page) {
  const seen = [];
  page.on('request', (r) => seen.push(r.url()));
  return seen;
}

/** The origins a request may legitimately have; everything else is a third party. */
function thirdParty(urls, allowed) {
  const ok = new Set(allowed);
  const out = new Set();
  for (const u of urls) {
    if (/^(data|blob|about):/.test(u)) continue;
    let origin;
    try {
      origin = new URL(u).origin;
    } catch {
      continue;
    }
    if (!ok.has(origin)) out.add(origin);
  }
  return [...out];
}

/** The connect screen is static: nothing may leave the app's own origin. */
async function firstParty(urls, label) {
  const strangers = thirdParty(urls, [new URL(APP).origin]);
  for (const o of strangers) problems.push(`${label}: the app requested ${o}, a third party (promise 3)`);
  say(`${label}: ${urls.length} requests, ${strangers.length} to a third party — ${strangers.length ? strangers.join(', ') : 'none, all same-origin'}`);
}

async function waitFor(url) {
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(url)).ok) return;
    } catch {
      /* not up yet */
    }
    await sleep(200);
  }
  throw new Error(`nothing answered at ${url}`);
}

function shot(file) {
  const kb = Math.round(statSync(file).size / 1024);
  say(`${file.replace(`${web}/`, '')}  ${kb} KB`);
  if (file.endsWith('.gif') && kb > 2048) problems.push(`${file} is ${kb} KB, over the 2 MB budget`);
}

/** Resolve the authored link; local preview checks its built copy, including absolute OG URLs. */
async function linkedAsset(href, base, expected, request = fetch) {
  if (!href?.trim()) throw new Error(`missing ${expected || 'image'} asset link`);
  let url = new URL(href, base);
  if (!process.env.APP && url.origin === PUBLIC_ORIGIN) url = new URL(url.pathname + url.search, APP);
  const response = await request(url.href);
  const type = (response.headers.get('content-type') ?? '').split(';')[0].trim().toLowerCase();
  const body = await response.arrayBuffer();
  const correct = expected === 'manifest' ? ['application/manifest+json', 'application/json'].includes(type) : expected ? type === expected.toLowerCase() : type.startsWith('image/');
  if (!response.ok || !correct || body.byteLength === 0) throw new Error(`${url.href} → ${response.status}, ${type || 'no content type'}, ${body.byteLength} bytes (expected ${expected || 'image/*'})`);
  return { url: response.url || url.href, body };
}

/** Regression fixtures: hashes/relative links work, a 200 HTML fallback or empty asset does not. */
async function assetFixtures() {
  let requested;
  const response = async (url) => { requested = url; return new globalThis.Response('<svg/>', { headers: { 'content-type': 'image/svg+xml' } }); };
  for (const href of ['/static/abc/favicon.svg', '../assets/renamed.svg', `${APP}/absolute.svg`]) {
    await linkedAsset(href, `${APP}/nested/index.html`, 'image/svg+xml', response);
    if (requested !== new URL(href, `${APP}/nested/index.html`).href) throw new Error('asset URL resolution fixture failed');
  }
  await linkedAsset(`${PUBLIC_ORIGIN}/static/og-hash.png`, APP, 'image/svg+xml', response);
  if (requested !== `${process.env.APP ? PUBLIC_ORIGIN : APP}/static/og-hash.png`) throw new Error('public asset preview fixture failed');
  for (const [status, type, body] of [[200, 'text/html', '<html/>'], [200, 'image/png', ''], [404, 'image/png', 'missing']]) {
    let refused = false;
    try { await linkedAsset('/broken.png', APP, '', async () => new globalThis.Response(body, { status, headers: { 'content-type': type } })); } catch { refused = true; }
    if (!refused) throw new Error('broken asset fixture was accepted');
  }
  say('asset fixtures: 7 passed / 0 failed / 0 skipped');
}

/** The page's head, as served: what a crawler, a social card and a phone's Add to Home Screen read. */
async function metas(page, label) {
  const html = await (await fetch(`${APP}/`)).text();
  for (const [needle, what] of [
    [`<title>${NAME}</title>`, 'the title'],
    ['name="description"', 'a description'],
    ['property="og:title"', 'og:title'],
    ['property="og:description"', 'og:description'],
    ['property="og:image"', 'og:image'],
    ['name="twitter:card"', 'twitter:card'],
    ['name="theme-color"', 'theme-color'],
  ]) {
    if (!html.includes(needle)) problems.push(`${label}: index.html lacks ${what}`);
  }
  if (!/<meta name="color-scheme" content="light"\s*\/>/.test(html)) problems.push(`${label}: color-scheme must be light`);
  const themes = html.match(/<meta name="theme-color"[^>]*>/g) ?? [];
  if (themes.length !== 1 || !themes[0].includes('content="#ffffff"') || themes[0].includes('media=')) problems.push(`${label}: expected one unconditional white theme-color`);
  if (html.includes('%PRODUCT') || html.includes('%WEB_URL%')) problems.push(`${label}: an unfilled placeholder is in index.html`);
  const links = await page.evaluate(() => ({
    base: document.baseURI,
    icons: [...document.querySelectorAll('link[rel~="icon"]')].map((el) => ({ href: el.getAttribute('href'), type: el.type })),
    manifest: document.querySelector('link[rel~="manifest"]')?.getAttribute('href'),
    apple: document.querySelector('link[rel~="apple-touch-icon"]')?.getAttribute('href'),
    og: document.querySelector('meta[property="og:image"]')?.getAttribute('content'),
  }));
  if (!links.icons.length) throw new Error(`${label}: no favicon links`);
  const fetched = await linkedAsset(links.manifest, links.base, 'manifest');
  const manifest = JSON.parse(new TextDecoder().decode(fetched.body));
  if (manifest.name !== NAME) problems.push(`${label}: the manifest names "${manifest.name}", not "${NAME}"`);
  if (manifest.display !== 'standalone') problems.push(`${label}: manifest display is ${manifest.display}`);
  if (!Array.isArray(manifest.icons) || !manifest.icons.length) throw new Error(`${label}: no manifest icons`);
  for (const icon of links.icons) await linkedAsset(icon.href, links.base, icon.type);
  for (const icon of manifest.icons) await linkedAsset(icon.src, fetched.url, icon.type);
  const apple = await linkedAsset(links.apple, links.base, '');
  const og = await linkedAsset(links.og, links.base, '');
  say(`${label}: head links resolved; manifest "${manifest.name}" (${manifest.icons.length} icons); ${links.icons.length + manifest.icons.length + 3} assets have valid content types and non-empty bodies`);
  return { apple: apple.url, og: og.url };

}

/** Every control the accessibility tree lists has a name — what a screen reader would say. */
async function names(page, label) {
  const snap = await page.locator('body').ariaSnapshot();
  const control = /^\s*-\s+(button|link|textbox|combobox|slider|checkbox|switch|menuitem|tab|radio|searchbox|spinbutton)\b(.*)$/;
  let n = 0;
  const unnamed = [];
  for (const line of snap.split('\n')) {
    const m = control.exec(line);
    if (!m) continue;
    n++;
    if (!/"[^"]+"/.test(m[2])) unnamed.push(line.trim());
  }
  for (const u of unnamed) problems.push(`${label}: control without an accessible name: ${u}`);
  say(`${label}: ${n} controls in the accessibility tree, ${unnamed.length} unnamed`);
}

/** Every visible run of text against the ground it actually sits on, WCAG AA. Disabled controls are exempt. */
async function contrast(page, label) {
  const rows = await page.evaluate(() => {
    const parse = (c) => {
      const m = /rgba?\(([\d.]+),\s*([\d.]+),\s*([\d.]+)(?:,\s*([\d.]+))?\)/.exec(c);
      return m ? [+m[1], +m[2], +m[3], m[4] === undefined ? 1 : +m[4]] : null;
    };
    const lum = ([r, g, b]) => {
      const f = (v) => ((v /= 255) <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const over = (top, bottom) => [0, 1, 2].map((i) => top[i] * top[3] + bottom[i] * (1 - top[3])).concat([1]);
    const ratio = (a, b) => {
      const x = lum(a), y = lum(b);
      return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
    };
    const groundOf = (el) => {
      const chain = [];
      for (let e = el; e; e = e.parentElement) chain.push(e);
      let bg = [255, 255, 255, 1];
      for (const e of chain.reverse()) {
        const p = parse(window.getComputedStyle(e).backgroundColor);
        if (p && p[3] > 0) bg = over(p, bg);
      }
      return bg;
    };
    const opacityOf = (el) => {
      let o = 1;
      for (let e = el; e; e = e.parentElement) o *= Number(window.getComputedStyle(e).opacity);
      return o;
    };
    const out = [];
    const seen = new Set();
    const walker = document.createTreeWalker(document.body, window.NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const text = n.textContent.trim();
      const el = n.parentElement;
      if (!text || !el || seen.has(el)) continue;
      seen.add(el);
      const cs = window.getComputedStyle(el);
      if (cs.visibility === 'hidden' || cs.display === 'none') continue;
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      if (el.matches(':disabled') || el.closest(':disabled')) continue;
      const fg0 = parse(cs.color);
      if (!fg0) continue;
      const bg = groundOf(el);
      const fg = over([fg0[0], fg0[1], fg0[2], fg0[3] * opacityOf(el)], bg);
      const size = parseFloat(cs.fontSize);
      const weight = Number(cs.fontWeight) || 400;
      const large = size >= 24 || (size >= 18.66 && weight >= 700);
      const cls = String(el.className || '').split(' ')[0];
      out.push({ text: text.slice(0, 36), sel: `${el.tagName.toLowerCase()}${cls ? `.${cls}` : ''}`, ratio: +ratio(fg, bg).toFixed(2), need: large ? 3 : 4.5, size });
    }
    return out;
  });
  const bad = rows.filter((r) => r.ratio < r.need);
  for (const b of bad) problems.push(`${label}: ${b.sel} "${b.text}" is ${b.ratio}:1, needs ${b.need}:1 at ${b.size}px`);
  const lowest = rows.reduce((m, r) => (r.ratio < m.ratio ? r : m), { ratio: 99 });
  say(`${label}: ${rows.length} text runs, lowest ${lowest.ratio}:1 (${lowest.sel}) — ${bad.length ? `${bad.length} below AA` : 'AA met'}`);
}

/** Where Tab goes, in order. */
async function tabOrder(page, label, max = 12) {
  // Start from the top of the document, not from wherever the page put its focus on mount.
  await page.evaluate(() => {
    document.body.tabIndex = -1;
    document.body.focus();
    document.body.removeAttribute('tabindex');
  });
  const order = [];
  for (let i = 0; i < max; i++) {
    await page.keyboard.press('Tab');
    const d = await page.evaluate(() => {
      const a = document.activeElement;
      if (!a || a === document.body) return null;
      const name = a.getAttribute('aria-label') || a.labels?.[0]?.textContent || a.textContent || a.getAttribute('placeholder') || '';
      return `${a.tagName.toLowerCase()}[${name.trim().replace(/\s+/g, ' ').slice(0, 26)}]`;
    });
    if (!d || order.includes(d)) break;
    order.push(d);
  }
  say(`${label}: Tab → ${order.join(' → ')}`);
  return order;
}

async function noOverflow(page, label) {
  const r = await page.evaluate(() => ({ over: document.documentElement.scrollWidth - window.innerWidth, w: window.innerWidth }));
  if (r.over > 0) problems.push(`${label}: the page scrolls horizontally by ${r.over}px at ${r.w}px`);
  else say(`${label}: no horizontal overflow at ${r.w}px`);
}

/** How the link looks pasted into a timeline: the served og.png under the served title and description. */
async function ogPreview(browser, assets) {
  const html = await (await fetch(`${APP}/`)).text();
  const title = /<title>([^<]*)<\/title>/.exec(html)?.[1] ?? '';
  const desc = /property="og:description" content="([^"]*)"/.exec(html)?.[1] ?? '';
  const ctx = await browser.newContext({ viewport: { width: 640, height: 480 }, deviceScaleFactor: 2, colorScheme: 'light' });
  const page = await ctx.newPage();
  await page.setContent(`<!doctype html><meta charset="utf-8"><style>
    body { margin: 0; background: #e9ecef; display: grid; place-items: center; height: 100vh; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; }
    .card { width: 520px; border: 1px solid #cfd4da; border-radius: 16px; overflow: hidden; background: #fff; }
    .card img { display: block; width: 100%; aspect-ratio: 1200 / 630; object-fit: cover; }
    .meta { padding: 12px 14px 14px; }
    .domain { color: #6c757d; font-size: 13px; }
    .title { font-weight: 600; font-size: 15px; margin: 2px 0; color: #111; }
    .desc { color: #495057; font-size: 14px; line-height: 1.35; }
  </style>
  <div class="card"><img src="${assets.og}" alt=""><div class="meta">
    <div class="domain">TODO(F3) web app URL</div><div class="title">${title}</div><div class="desc">${desc}</div>
  </div></div>`);
  await page.waitForFunction(() => document.images[0]?.complete);
  const file = join(shots, '30-og-preview.png');
  await page.screenshot({ path: file });
  shot(file);
  await ctx.close();
}

/** A mock of a phone home screen with the app's icon on it: the tile a phone makes from apple-touch-icon.png. */
async function homeScreen(browser, assets) {
  const ctx = await browser.newContext({ viewport: { width: 390, height: 300 }, deviceScaleFactor: 2 });
  const page = await ctx.newPage();
  const tile = (src, label) => `<div class="tile"><img src="${src}"><span>${label}</span></div>`;
  await page.setContent(`<!doctype html><meta charset="utf-8"><style>
    body { margin: 0; height: 100vh; background: linear-gradient(160deg, #2b3a55, #0f1622); display: flex; gap: 22px; justify-content: center; align-items: center;
           font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; }
    .tile { display: grid; justify-items: center; gap: 7px; width: 76px; }
    .tile img { width: 60px; height: 60px; border-radius: 13.5px; box-shadow: 0 2px 6px rgba(0,0,0,.35); }
    .tile span { color: #fff; font-size: 11px; text-align: center; text-shadow: 0 1px 2px rgba(0,0,0,.6); white-space: nowrap; }
    .blank img { background: #6b7280; }
  </style>
  ${tile(assets.apple, NAME)}
  <div class="tile blank"><img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"><span>Photos</span></div>
  <div class="tile blank"><img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"><span>Notes</span></div>`);
  await page.waitForFunction(() => [...document.images].every((i) => i.complete));
  const file = join(shots, '30-home-screen-icon.png');
  await page.screenshot({ path: file });
  shot(file);
  await ctx.close();
}

async function connectScreen(browser) {
  let assets;
  for (const [name, viewport, scheme] of [
    ['30-connect-desktop', { width: 1280, height: 800 }, 'light'],
    ['30-connect-phone', { width: 390, height: 844 }, 'light'],
  ]) {
    const ctx = await browser.newContext({ viewport, colorScheme: scheme, deviceScaleFactor: viewport.width < 500 ? 2 : 1 });
    const page = await ctx.newPage();
    watch(page, name);
    const asked = netWatch(page);
    await page.goto(`${APP}/`);
    await page.waitForSelector('.connect-card');
    if (!assets) assets = await metas(page, 'connect');
    await page.waitForTimeout(300);
    // After the fonts have settled, so a font that was fetched from a CDN would be in the log.
    await page.evaluate(() => document.fonts.ready);
    // And after the mark has arrived: it is an `<img>` (039 comfort 1), and a shot taken before it
    // lands shows the page with its anchor missing, which is a lie about the page.
    await page.waitForFunction(() => [...document.images].every((i) => i.complete));
    await firstParty(asked, name);
    await names(page, name);
    await contrast(page, name);
    const order = await tabOrder(page, name, 6);
    // The field is the first thing past the chrome. Below 900 px there is no chrome and Tab lands
    // on it outright; above, the page carries two destination links (039) and the language pair (043); only that chrome
    // may come first. A friend with a code never tabs through a site map.
    const HEADER = ['link[Host your own]', 'link[Source]', 'a[Host your own]', 'a[Source]', 'a[EN]', 'a[中文]'];
    const chrome = order.findIndex((d) => !HEADER.includes(d));
    const first = order[chrome === -1 ? 0 : chrome];
    if (!first?.startsWith('textarea[Invite code]')) {
      problems.push(`${name}: Tab reaches ${first ?? 'nothing'} before the invite field (order: ${order.join(' → ')})`);
    }
    if (chrome > 4) problems.push(`${name}: ${chrome} controls in the header, and the design has two destinations and two language links`);
    // The shot below is what a reader meets, so it must not carry the checker's own cursor: with
    // more than six controls on the page Tab now stops on one of them and leaves a focus ring
    // there, where it used to run off the end of the document and leave none.
    await page.evaluate(() => document.activeElement?.blur?.());
    if (viewport.width < 500) await noOverflow(page, name);
    const file = join(shots, `${name}.png`);
    await page.screenshot({ path: file });
    shot(file);
    await ctx.close();
  }
  await ogPreview(browser, assets);
  await homeScreen(browser, assets);
}

function ffmpeg() {
  const bin = process.env.FFMPEG ?? 'ffmpeg';
  const probe = spawnSync(bin, ['-hide_banner', '-muxers'], { encoding: 'utf8' });
  if (probe.status !== 0) throw new Error(`no ffmpeg at "${bin}": brew install ffmpeg, or set FFMPEG=/path/to/ffmpeg`);
  if (!/^\s*E\s+gif\b/m.test(probe.stdout)) throw new Error(`the ffmpeg at "${bin}" cannot write GIF (Playwright's cannot); set FFMPEG to a full build`);
  return bin;
}

/** A friend's first chat, against a real host, recorded. Then the same on a phone. */
// 047: real connect states from local storage and the real parser; no host is needed.
async function chineseCards(browser) {
  let count = 0, failed = 0;
  for (const width of [1280, 390]) for (const state of ['idle', 'invalid', 'returning']) {
    const before = problems.length;
    const label = `047-${width}-${state}-zh`;
    const ctx = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: 'light', locale: 'zh-CN' });
    const page = await ctx.newPage();
    watch(page, label);
    const asked = netWatch(page);
    await page.addInitScript(({ state }) => {
      window.localStorage.setItem('bn.language', JSON.stringify('zh'));
      if (state === 'returning') {
        window.localStorage.setItem('bn.invite', JSON.stringify(`ic1.tcDEMOaddressDEMOaddressDEMOaddressDEMO.${'D'.repeat(43)}`));
        window.localStorage.setItem('bn.lastHost', JSON.stringify({ name: "Max's laptop", scope: '047', left: true }));
        window.localStorage.setItem('bn.conversations.047', JSON.stringify(['047-chat']));
      }
    }, { state });
    await page.goto(APP);
    await page.locator('.connect-card').waitFor();
    if (state === 'invalid') await page.locator('textarea').fill('bad');
    await page.evaluate(() => document.fonts.ready);
    const card = await page.locator('.connect-card').innerText();
    // Names, format examples and the bilingual language links are machine/identity strings.
    const remainder = card.replaceAll("Max's laptop", '').replaceAll('Infercat', '').replaceAll('EN', '').replaceAll('GPU', '')
      .replace(/ic1\.[A-Za-z0-9_.…-]*/g, '').replace(/\btc[\w…]*/g, '').replaceAll('bad', '');
    if (/[A-Za-z]/.test(remainder)) problems.push(`${label}: untranslated card text: ${remainder}`);
    if (state === 'returning' && !card.includes('1')) problems.push(`${label}: missing retained chat count`);
    if (state === 'invalid' && await page.locator('.inline-error').count() !== 1) problems.push(`${label}: parser error not shown`);
    await names(page, label); await contrast(page, label); await noOverflow(page, label); await firstParty(asked, label);
    const file = join(shots, `${label}.png`); await page.screenshot({ path: file }); shot(file);
    await ctx.close(); count++; if (problems.length > before) failed++;
  }
  say(`047 Chinese cards: ${count - failed} passed / ${failed} failed / 0 skipped of ${count}`);
}

async function chat(browser) {
  mkdirSync(media, { recursive: true });
  const tmp = mkdtempSync(join(tmpdir(), 'bn-launch-'));
  const size = demoDir ? { width: 390, height: 720 } : { width: 880, height: 640 };
  const ctx = await browser.newContext({ viewport: size, isMobile: Boolean(demoDir), hasTouch: Boolean(demoDir), colorScheme: 'light', recordVideo: { dir: tmp, size } });
  const page = await ctx.newPage();
  const recordingStarted = demoDir ? Date.now() : 0;
  watch(page, 'chat');
  // Not an assertion: once connected the app is *meant* to reach the relay it was told about, which
  // Protection 3 allows by name. Printed so a reviewer can see exactly who that was.
  const asked = netWatch(page);
  const t0 = Date.now();
  await page.goto(`${APP}/#${INVITE}`);
  await page.waitForSelector('.composer textarea', { timeout: 90_000 });
  say(`chat: connected in ${((Date.now() - t0) / 1000).toFixed(1)} s (${await page.locator('.path').innerText().catch(() => '?')})`);
  await page.waitForTimeout(900);
  const box = page.locator('.composer textarea');
  await box.click();
  await box.pressSequentially(QUESTION, { delay: 30 });
  await page.waitForTimeout(400);
  if (demoDir) {
    await page.getByRole('button', { name: 'Send' }).click();
    writeFileSync(join(media, 'browser.marks.json'), JSON.stringify({ sendSeconds: (Date.now() - recordingStarted) / 1000 }));
  }
  else await page.keyboard.press('Enter');
  const stop = page.getByRole('button', { name: 'Stop' });
  await stop.waitFor({ timeout: 30_000 });
  if (demoDir) {
    // Capture the first rendered streamed token, including reasoning when it arrives first.
    await page.waitForFunction(() => [...document.querySelectorAll('.row.assistant .md')].some((e) => e.textContent.trim()));
    await page.screenshot({ path: join(media, 'browser-first-token.png') });
  }
  await stop.waitFor({ state: 'detached', timeout: 180_000 });
  await page.waitForTimeout(demoDir ? 2000 : 1800);
  if (demoDir) {
    const answer = await page.locator('.row.assistant > .md').innerText();
    if (!answer.trim()) throw new Error('demo: model produced no answer');
    await page.locator('.thinking-toggle').waitFor();
    await page.screenshot({ path: join(media, 'browser-answer.png') });
    const video = page.video();
    await ctx.close();
    copyFileSync(await video.path(), join(media, 'browser.webm'));
    rmSync(tmp, { recursive: true, force: true });
    say('demo: real phone chat, Thinking line, first-token poster captured');
    return;
  }
  const png = join(media, 'friend-chat.png');
  await page.screenshot({ path: png });
  shot(png);
  await names(page, 'chat');
  await contrast(page, 'chat (light)');
  await tabOrder(page, 'chat', 10);
  const offOrigin = thirdParty(asked, [new URL(APP).origin]);
  say(`chat: ${asked.length} requests, off-origin — ${offOrigin.length ? offOrigin.join(', ') : 'none'} (the relay is the only one Protection 3 allows)`);
  const desktop = join(shots, '30-chat-desktop.png');
  await page.screenshot({ path: desktop });
  shot(desktop);
  const video = page.video();
  await ctx.close();
  const webm = await video.path();
  const gif = join(media, 'friend-chat.gif');
  const r = spawnSync(
    ffmpeg(),
    ['-y', '-loglevel', 'error', '-i', webm, '-vf',
      'fps=8,scale=720:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4:diff_mode=rectangle',
      '-loop', '0', gif],
    { stdio: 'inherit' },
  );
  if (r.status !== 0) problems.push(`ffmpeg exited ${r.status}`);
  else shot(gif);
  rmSync(tmp, { recursive: true, force: true });

  // The phone: a touch keyboard's Return is a newline, so Send is the button (014 promise 6).
  const phone = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true, colorScheme: 'light' });
  const pp = await phone.newPage();
  watch(pp, 'chat-phone');
  await pp.goto(`${APP}/#${INVITE}`);
  await pp.waitForSelector('.composer textarea', { timeout: 90_000 });
  await pp.locator('.composer textarea').fill(QUESTION);
  await pp.getByRole('button', { name: 'Send' }).click();
  const stop2 = pp.getByRole('button', { name: 'Stop' });
  await stop2.waitFor({ timeout: 30_000 });
  await stop2.waitFor({ state: 'detached', timeout: 180_000 });
  await pp.waitForTimeout(1200);
  await names(pp, 'chat-phone');
  await contrast(pp, 'chat-phone');
  await noOverflow(pp, 'chat-phone');
  const pf = join(shots, '30-chat-phone.png');
  await pp.screenshot({ path: pf });
  shot(pf);
  await phone.close();


}

async function main() {
  await assetFixtures();
  if (demoDir && (!INVITE || !process.env.APP)) throw new Error('demo needs INVITE and APP');
  if (!demoDir) mkdirSync(shots, { recursive: true });
  let preview = null;
  if (!process.env.APP) {
    // Preview the same first-party media that deploy-web copies alongside the built app.
    for (const file of ['demo.mp4', 'demo.zh.mp4', 'demo-poster.png', 'demo-poster.zh.png']) copyFileSync(join(web, '../docs/media', file), join(web, 'dist', file));
    preview = spawn(process.execPath, ['node_modules/vite/bin/vite.js', 'preview', '--port', String(PORT), '--strictPort'], {
      cwd: web,
      stdio: ['ignore', 'ignore', 'inherit'],
    });
  }
  const browser = await chromium.launch();
  try {
    await waitFor(`${APP}/`);
    console.log(`launch-check on ${APP}`);
    if (!demoDir) {
      await connectScreen(browser);
      await chineseCards(browser);
      await landingEvidence(browser, APP, shots, async (page, label) => { await contrast(page, label); await names(page, label); });
    }
    if (INVITE) await chat(browser);
    else say('no INVITE: the chat was not exercised (set INVITE=ic1.… APP=… against a running host)');
  } finally {
    await browser.close();
    preview?.kill('SIGTERM');
  }
  if (problems.length) {
    console.error(`\n${problems.length} problem(s):`);
    for (const p of problems) console.error(`  - ${p}`);
    process.exit(1);
  }
  console.log('\nlaunch-check: OK');
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
