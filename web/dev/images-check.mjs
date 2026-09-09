// Ticket 073: run against an already-started Vite server; browser/API/IndexedDB evidence.
// IMAGE_CHECK_URL=http://127.0.0.1:49183 node web/dev/images-check.mjs
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import assert from 'node:assert/strict';
const base = process.env.IMAGE_CHECK_URL ?? 'http://127.0.0.1:49183';
const out = process.env.IMAGE_CHECK_OUT ?? '/tmp/infercat-073-screenshots';
mkdirSync(out, { recursive: true });
const invite = `ic1.tcIMAGEproofaddressIMAGEproofaddress.${'D'.repeat(43)}`;
let checks = 0;
const check = (condition, message) => { assert.ok(condition, message); checks++; };
const browser = await chromium.launch({ headless: true });
const errors = [];
async function connected(width, lang, vision = 'true', extra = '') {
  const context = await browser.newContext({ viewport: { width, height: 900 }, locale: lang === 'zh' ? 'zh-CN' : 'en-US', isMobile: width === 390, hasTouch: width === 390 });
  await context.addInitScript((language) => window.localStorage.setItem('bn.language', JSON.stringify(language)), lang);
  const page = await context.newPage();
  page.on('pageerror', (e) => errors.push(String(e)));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  await page.goto(`${base}/?fake&connectMs=20&tokenDelay=1&vision=${vision}&invite=${encodeURIComponent(invite)}&autoconnect${extra}`);
  await page.locator('.composer textarea').waitFor();
  await page.waitForFunction(() => !document.querySelector('.composer textarea')?.disabled);
  await page.evaluate(() => document.fonts.ready);
  return page;
}
async function attach(page, mode = 'picker', count = 2) {
  await page.evaluate(async ({ mode, count }) => {
    const c = document.createElement('canvas'); c.width = 2000; c.height = 1200;
    const x = c.getContext('2d'); x.fillStyle = '#fff'; x.fillRect(0, 0, 2000, 1200);
    x.fillStyle = '#e22'; x.fillRect(80, 80, 850, 1040); x.fillStyle = '#24c'; x.fillRect(1070, 80, 850, 1040);
    x.fillStyle = '#111'; x.font = '90px sans-serif'; x.fillText('RED     BLUE', 170, 660);
    const blob = await new Promise((r) => c.toBlob(r, 'image/png'));
    const dt = new window.DataTransfer();
    for (let n = 0; n < count; n++) dt.items.add(new window.File([blob], `colors-${n}.png`, { type: 'image/png' }));
    if (mode === 'picker') { const input = document.querySelector('input[type=file]'); input.files = dt.files; input.dispatchEvent(new window.Event('change', { bubbles: true })); }
    else if (mode === 'paste') document.querySelector('.composer textarea').dispatchEvent(new window.ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));
    else document.querySelector('.composer').dispatchEvent(new window.DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true }));
  }, { mode, count });
}
async function shot(page, name) {
  await page.evaluate(async () => { await document.fonts.ready; await Promise.all([...document.images].filter((i) => i.src).map((i) => i.decode().catch(() => {}))); });
  check(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${name}: horizontal overflow`);
  await page.screenshot({ path: `${out}/${name}.png` });
}
try {
  for (const width of [390, 1280]) for (const lang of ['en', 'zh']) {
    const page = await connected(width, lang);
    const label = `${width}-${lang}`;
    check(await page.locator('.attach').count() === 1, 'true capability exposes attach');
    await page.evaluate(() => {
      window.__bitmap = window.createImageBitmap;
      window.createImageBitmap = async (...args) => { const bitmap = await window.__bitmap(...args); await new Promise((resolve) => { window.__finishImage = resolve; }); return bitmap; };
    });
    await attach(page, 'picker', 1);
    await page.waitForFunction(() => Boolean(window.__finishImage));
    check(await page.locator('.attachment-reading').count() === 1, 'generic reading row appears');
    check(await page.locator('.composer .primary').isDisabled(), 'Send waits for reading');
    await shot(page, `reading-${label}`);
    await page.locator('.attachment-reading .thumb-x').click();
    await page.evaluate(() => { window.__finishImage(); window.createImageBitmap = window.__bitmap; });
    await page.waitForFunction(() => document.querySelectorAll('.thumb').length === 0);
    check(await page.locator('.thumb img').count() === 0, 'cancelled attachment never admitted');
    await attach(page, width === 390 ? 'picker' : 'paste');
    await page.waitForFunction(() => document.querySelectorAll('.attached .thumb img').length === 2);
    check((await page.locator('.attached-line').innerText()).includes('1024'), 'measured downscale line');
    check(await page.locator('.composer .primary').isEnabled(), 'images alone enable Send');
    await shot(page, `compose-${label}`);
    await page.evaluate(() => { const dt = new window.DataTransfer(); dt.items.add(new window.File(['x'], 'x.png', { type: 'image/png' })); document.querySelector('.composer').dispatchEvent(new window.DragEvent('dragover', { dataTransfer: dt, bubbles: true, cancelable: true })); });
    await shot(page, `drag-${label}`);
    await page.evaluate(() => document.querySelector('.composer').dispatchEvent(new window.DragEvent('dragleave', { bubbles: true })));
    await page.locator('.composer .primary').click();
    await page.locator('.row.assistant .meta-text').waitFor();
    await page.waitForFunction(() => document.querySelector('.meta-text')?.textContent.includes('1,292') && !document.querySelector('.row.streaming'));
    check((await page.locator('.meta-text').innerText()).includes(lang === 'zh' ? '2 张图片' : '2 images'), 'reply measured image count');
    await shot(page, `sent-${label}`);
    check(await page.evaluate(() => Object.entries(window.localStorage).filter(([k]) => k.startsWith('bn.conversations')).every(([, v]) => !v.includes('data:image') && !v.includes('"blob"'))), 'metadata only in window.localStorage');
    await page.locator('.shot').first().click();
    await shot(page, `sheet-${label}`);
    await page.keyboard.press('Escape');
    await page.reload();
    await page.locator('.shot img').first().waitFor();
    check(await page.locator('.shot img').count() === 2, 'JPEGs survive reload');
    await page.locator('.row.user .actions button').click();
    await page.locator('.editing .thumb-x').first().click();
    await shot(page, `edit-${label}`);
    await page.locator('.editing .primary').click();
    await page.waitForFunction(() => document.querySelector('.meta-text')?.textContent.includes('652') && !document.querySelector('.row.streaming'));
    check(await page.locator('.shot img').count() === 1, 'edit removes one image without re-encoding');
    await page.locator('.meters').click(); await shot(page, `limits-${label}`); await page.locator('.sheet .primary').click();
    await page.locator('.topbar > button').last().click(); await shot(page, `settings-${label}`); await page.locator('.sheet-actions .primary').click();
    await page.locator('.composer textarea').fill('/images'); await page.locator('.composer .primary').click();
    await page.locator('.ended.interrupted').waitFor();
    check((await page.locator('.ended.interrupted').innerText()).includes(lang === 'zh' ? '拒绝' : 'rejected'), 'named image rejection');
    await page.locator('.host-said summary').click(); await shot(page, `rejected-${label}`);
    // Remove the stored JPEG exactly as oldest-first eviction would, then reload and send.
    await page.evaluate(() => new Promise((resolve, reject) => { const r = window.indexedDB.open('infercat-images', 1); r.onsuccess = () => { const db = r.result; const tx = db.transaction('turns', 'readwrite'); tx.objectStore('turns').clear(); tx.oncomplete = () => { db.close(); resolve(); }; }; r.onerror = reject; }));
    await page.reload(); await page.locator('.missing-image').first().waitFor();
    await page.locator('.composer textarea').fill('Continue without the missing image'); await page.locator('.composer .primary').click();
    await page.waitForFunction(() => [...document.querySelectorAll('.row.assistant .meta-text')].at(-1)?.textContent.includes('tokens') || [...document.querySelectorAll('.row.assistant .meta-text')].at(-1)?.textContent.includes('token'));
    await page.locator('.image-notice').first().waitFor();
    check((await page.locator('.image-notice').first().innerText()).includes(lang === 'zh' ? '已不在' : 'no longer'), 'missing blob unblocks Send with notice');
    await shot(page, `missing-${label}`);
    await page.context().close();
    for (const capability of ['false', 'null']) {
      const no = await connected(width, lang, capability);
      check(await no.locator('.attach').count() === 0, `${capability}: attach absent`);
      await attach(no, 'paste', 1); await no.locator('.image-notice').waitFor();
      check(await no.locator('.thumb').count() === 0, 'no-vision paste does not attach');
      await shot(no, `novision-${capability}-${label}`); await no.context().close();
    }
  }
  const p = await connected(1280, 'en');
  await attach(p, 'drop', 5); await p.waitForFunction(() => document.querySelectorAll('.thumb img').length === 4);
  check((await p.locator('.image-notice').innerText()).includes('4 images'), 'four-image cap');
  // Actual browser EXIF and IndexedDB transaction checks, using the same production functions.
  const actual = await p.evaluate(async () => {
    const { prepareImage } = await import('/src/images.ts');
    const { storeImages, readImages } = await import('/src/image-store.ts');
    const c = document.createElement('canvas'); c.width = 2000; c.height = 1200; const ctx = c.getContext('2d'); ctx.fillStyle = 'red'; ctx.fillRect(0, 0, 2000, 1200);
    const jpeg = await new Promise((r) => c.toBlob(r, 'image/jpeg'));
    const bytes = new Uint8Array(await jpeg.arrayBuffer());
    const exif = new Uint8Array([255,225,0,34,69,120,105,102,0,0,73,73,42,0,8,0,0,0,1,0,18,1,3,0,1,0,0,0,6,0,0,0,0,0,0,0]);
    const rotated = await prepareImage(new window.Blob([bytes.slice(0,2), exif, bytes.slice(2)], { type: 'image/jpeg' }));
    const blob = new window.Blob([new Uint8Array(8 * 1024 * 1024)]);
    const clock = Date.now; Date.now = () => 1000;
    try { for (const [scope, id] of [['cap','old'],['other','isolated'],['cap','middle'],['cap','new']]) await storeImages(scope, id, [{ id, w: 1, h: 1, bytes: blob.size, blob, data: '' }]); } finally { Date.now = clock; }
    const cap = await readImages('cap', ['old','middle','new'].map((id) => ({ id, images: [{ id }] })));
    const other = await readImages('other', [{ id: 'isolated', images: [{ id: 'isolated' }] }]);
    return { orientation: [rotated.w, rotated.h], cap: Object.keys(cap).sort(), other: Object.keys(other) };
  });
  check(JSON.stringify(actual.orientation) === '[614,1024]', 'real EXIF orientation 6 applied once before downscale');
  check(JSON.stringify(actual.cap) === '["middle","new"]', 'actual IndexedDB eviction transaction removes oldest');
  check(JSON.stringify(actual.other) === '["isolated"]', 'another host is not evicted');
  check(errors.length === 0, `browser errors: ${errors.join('\n')}`);
  console.log(`IMAGE CHECK: ${checks} passed / 0 failed / 0 skipped; screenshots ${out}`);
} finally { await browser.close(); }
