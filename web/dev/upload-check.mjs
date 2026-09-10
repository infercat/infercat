// Ticket 132. Run against Vite: UPLOAD_URL=http://127.0.0.1:49132 node dev/upload-check.mjs
import { Buffer } from 'node:buffer';
import { chromium, firefox, webkit } from 'playwright';
import assert from 'node:assert/strict';
import { mkdirSync, readFileSync } from 'node:fs';
const base = process.env.UPLOAD_URL ?? 'http://127.0.0.1:49132';
const out = process.env.UPLOAD_OUT ?? '/tmp/infercat-132-screenshots';
mkdirSync(out, { recursive: true });
const rasters = ['jpg', 'png', 'webp', 'gif', 'avif', 'bmp', 'ico'].map((ext) => ({ name: `color.${ext}`, bytes: [...readFileSync(new URL(`./fixtures/upload/color.${ext}`, import.meta.url))] }));
let checks = 0;
const check = (value, why) => { assert.ok(value, why); checks++; };
const svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 2000 1000"><rect width="2000" height="1000" fill="#d4372c"/><circle cx="1000" cy="500" r="330" fill="#fff"/></svg>';
for (const [engine, launcher] of Object.entries({ chromium, firefox, webkit })) {
  const browser = await launcher.launch();
  try {
    const page = await browser.newPage();
    const errors = [], external = [];
    page.on('pageerror', (e) => errors.push(String(e)));
    await page.route('https://svg-reference.invalid/**', (route) => { external.push(route.request().url()); return route.abort(); });
    await page.goto(`${base}/?fake`);
    const results = await page.evaluate(async ({ source, rasters }) => {
      const { admit, sentContent, attachmentFields } = await import('/src/attachments.ts');
      const { MAX_SVG_BYTES } = await import('/src/images.ts');
      const cases = [
        ['declared', source.replace('viewBox=', 'width="2000" height="1000" viewBox='), 1024, 512],
        ['viewBox', source, 1024, 512],
        ['width-only', source.replace('viewBox=', 'width="500" viewBox='), 500, 250],
        ['height-only', source.replace('viewBox=', 'height="250" viewBox='), 500, 250],
        ['no-size', source.replace(' viewBox="0 0 2000 1000"', ''), 300, 150],
        ['external', source.replace('</svg>', '<image href="https://svg-reference.invalid/image.png" width="20" height="20"/><script>window.__svgRan=true</script><style>@import url("https://svg-reference.invalid/style.css");</style></svg>'), 1024, 512],
        ['animated', source.replace('</svg>', '<animate attributeName="opacity" from="1" to="0" dur="10s" repeatCount="indefinite"/></svg>'), 1024, 512],
      ];
      const results = [];
      for (const [name, text, w, h] of cases) {
        const attachments = await admit([new window.File([text], `${name}.svg`)], undefined, { vision: true });
        const image = attachments[0].image;
        const content = sentContent({ content: 'Describe it', ...attachmentFields(attachments) }, { [image.id]: image.data });
        results.push({ name, okay: image.w === w && image.h === h && image.blob.type === 'image/jpeg' && content[0].image_url.url.startsWith('data:image/jpeg;') });
      }
      for (const { name, bytes } of rasters) {
        const attachments = await admit([new window.File([new Uint8Array(bytes)], name)], undefined, { vision: true });
        const image = attachments[0].image;
        results.push({ name, okay: image.w === 16 && image.h === 16 && image.blob.type === 'image/jpeg' });
      }
      for (const [name, data, type] of [['invalid.svg', '<svg', 'image/svg+xml'], ['too-big.svg', ' '.repeat(MAX_SVG_BYTES + 1), 'image/svg+xml'], ['renamed.png', source, 'image/png'], ['photo.heic', 'unreadable', 'image/heic']]) {
        try { await admit([new window.File([data], name, { type })], undefined, { vision: true }); results.push({ name, okay: false }); }
        catch (e) { results.push({ name, okay: e.reason === 'cant_read' && e.message.includes(name.split('.').at(-1).toUpperCase()) }); }
      }
      results.push({ name: 'script inert', okay: !window.__svgRan });
      return results;
    }, { source: svg, rasters });
    for (const row of results) check(row.okay, `${engine}: ${row.name}`);
    check(external.length === 0, `${engine}: external SVG resource fetched`);
    check(errors.length === 0, `${engine}: ${errors.join('; ')}`);
    console.log(`${engine}: ${results.length + 2} passed / 0 failed / 0 skipped`);
    if (engine !== 'chromium') continue;
    for (const width of [390, 1280]) {
      await page.setViewportSize({ width, height: 900 });
      const invite = `ic1.tcIMAGEproofaddressIMAGEproofaddress.${'D'.repeat(43)}`;
      await page.goto(`${base}/?fake&connectMs=20&tokenDelay=1&vision=true&invite=${encodeURIComponent(invite)}&autoconnect`);
      await page.locator('.composer textarea').waitFor();
      await page.waitForFunction(() => !document.querySelector('.composer textarea')?.disabled);
      // Real composer events, one SVG per input path, identical extension-only metadata.
      for (const [index, mode] of ['picker', 'paste', 'drop'].entries()) {
        await page.evaluate(({ source, mode }) => {
          const dt = new window.DataTransfer(); dt.items.add(new window.File([source], `${mode}.SVG`));
          if (mode === 'picker') { const el = document.querySelector('input[type=file]'); el.files = dt.files; el.dispatchEvent(new window.Event('change', { bubbles: true })); }
          else if (mode === 'paste') document.querySelector('.composer textarea').dispatchEvent(new window.ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));
          else document.querySelector('.composer').dispatchEvent(new window.DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true }));
        }, { source: svg, mode });
        await page.waitForFunction((count) => document.querySelectorAll('.attached .thumb img').length === count, index + 1);
        check(true, `${mode} SVG admitted`);
      }
      check((await page.locator('input[type=file]').getAttribute('accept')).includes('.svg'), 'picker offers SVG');
      await page.evaluate(() => document.fonts.ready);
      check(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'composer overflow');
      await page.screenshot({ path: `${out}/svg-compose-${width}.png` });
      await page.locator('input[type=file]').setInputFiles({ name: 'unsupported.tiff', mimeType: 'image/tiff', buffer: Buffer.from([0]) });
      await page.waitForFunction(() => document.querySelector('.image-notice')?.textContent.includes('TIFF'));
      check(await page.locator('.attached .thumb img').count() === 3, 'kind refusal leaves accepted images intact');
      await page.screenshot({ path: `${out}/svg-rejected-${width}.png` });
      await page.locator('.composer .primary').click();
      await page.locator('.row.assistant .meta-text').waitFor();
      await page.locator('.shot').first().click();
      await page.locator('.sheet').waitFor();
      await page.screenshot({ path: `${out}/svg-sheet-${width}.png` });
      check(await page.locator('.sheet img').count() > 0, 'sheet displays rasterized SVG');
      await page.keyboard.press('Escape');
      // Start the next size with no retained fixture turns.
      await page.evaluate(() => window.localStorage.clear());
    }
  } finally { await browser.close(); }
}
console.log(`UPLOAD CHECK: ${checks} passed / 0 failed / 0 skipped`);
