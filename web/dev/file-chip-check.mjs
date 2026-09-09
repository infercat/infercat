// Standalone browser contract check. Run against the 076 dev server before the 073 seam lands.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';
const base = process.env.WEB_BASE ?? 'http://127.0.0.1:49176';
const out = new URL('./evidence/076-components/', import.meta.url);
await mkdir(out, { recursive: true });
const browser = await chromium.launch();
let passed = 0;
try {
  for (const width of [390, 1280]) {
    const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 800 }, permissions: ['clipboard-read', 'clipboard-write'] });
    const page = await context.newPage(); const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(`${base}/dev/file-chip-preview.html`);
    await page.locator('.chip').first().waitFor(); await page.evaluate(() => document.fonts.ready);
    await page.screenshot({ path: new URL(`chips-${width}.png`, out).pathname, fullPage: true });
    await page.locator('.shots .chip').first().click();
    assert.equal(await page.getByRole('dialog').count(), 1); passed++;
    const block = await page.getByRole('dialog').locator('pre').innerText();
    assert.ok(block.startsWith('[file: rate-limits.pdf · PDF · 6 pages]\n')); passed++;
    await page.getByRole('button', { name: 'Copy', exact: true }).click();
    assert.equal(await page.evaluate(() => globalThis.navigator.clipboard.readText()), block); passed++;
    await page.screenshot({ path: new URL(`sheet-${width}.png`, out).pathname, fullPage: true });
    await page.keyboard.press('Escape'); assert.equal(await page.getByRole('dialog').count(), 0); passed++;
    await page.locator('.shots .chip').first().click(); await page.locator('.sheet-wrap').click({ position: { x: 1, y: 1 } });
    assert.equal(await page.getByRole('dialog').count(), 0); passed++;
    for (const [name, expected] of [['rate-limits.pdf', 'The burst multiplier is two.'], ['rate-limits.docx', 'The Word document says'], ['limiter.ts', 'BURST_MULTIPLIER']]) {
      await page.getByLabel('Extract fixture').setInputFiles(new URL(`./fixtures/files/${name}`, import.meta.url).pathname);
      await page.waitForFunction((name) => { const chips = document.querySelectorAll('.shots .chip'); return chips.length === 1 && chips[0].getAttribute('title') === name; }, name);
      await page.locator('.shots .chip').click();
      assert.ok((await page.getByRole('dialog').locator('pre').innerText()).includes(expected)); passed++;
      await page.keyboard.press('Escape');
    }
    await page.getByLabel('Extract fixture').setInputFiles(new URL('./fixtures/files/scan.pdf', import.meta.url).pathname);
    await page.getByRole('status').filter({ hasText: 'no_text' }).waitFor(); passed++;
    assert.equal(errors.length, 0, errors.join('\n')); passed++;
    await context.close();
  }
  console.log(`File component browser checks: ${passed} passed / 0 failed / 0 skipped`);
} finally { await browser.close(); }
