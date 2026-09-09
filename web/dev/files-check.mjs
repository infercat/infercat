import { Buffer } from 'node:buffer';
// 076 full composer checks: real browser extraction, fake transport, captured production request.
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import assert from 'node:assert/strict';
const base = process.env.FILE_CHECK_URL ?? 'http://127.0.0.1:49176';
const out = process.env.FILE_CHECK_OUT ?? '/private/tmp/infercat-076-screenshots';
mkdirSync(out, { recursive: true });
const fixture = (name) => new URL(`./fixtures/files/${name}`, import.meta.url).pathname;
const invite = `ic1.tcFILEproofaddressFILEproofaddress.${'D'.repeat(43)}`;
let checks = 0;
const check = (ok, message) => { assert.ok(ok, message); checks++; };
const browser = await chromium.launch(); const errors = []; let currentPage;
async function connected(width, lang, vision = false) {
  const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 800 }, locale: lang === 'zh' ? 'zh-CN' : 'en-US', isMobile: width === 390, hasTouch: width === 390, permissions: ['clipboard-read', 'clipboard-write'] });
  await context.addInitScript((lang) => window.localStorage.setItem('bn.language', JSON.stringify(lang)), lang);
  const page = await context.newPage(); page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(`${base}/?fake&connectMs=20&tokenDelay=1&vision=${vision}&invite=${encodeURIComponent(invite)}&autoconnect`);
  await page.locator('.composer textarea').waitFor();
  await page.waitForFunction(() => !document.querySelector('.composer textarea').disabled);
  currentPage = page; return page;
}
async function shot(page, name) {
  await page.evaluate(() => document.fonts.ready);
  check(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${name}: overflow`);
  await page.screenshot({ path: `${out}/${name}.png` });
}
async function upload(page, names) {
  await page.locator('input[type=file]').setInputFiles(names.map(fixture));
  await page.waitForFunction((n) => document.querySelectorAll('.attached .chip').length === n && !document.querySelector('.attachment-reading'), names.length);
}
async function paste(page, text) {
  await page.locator('.composer textarea').evaluate((el, text) => {
    const data = new window.DataTransfer(); data.setData('text/plain', text);
    el.dispatchEvent(new window.ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }));
  }, text);
}
try {
  for (const width of [390, 1280]) for (const lang of ['en', 'zh']) {
    const page = await connected(width, lang), label = `${width}-${lang}`;
    check(await page.locator('.attach').count() === 1, 'text-only model exposes file picker');
    await shot(page, `empty-${label}`);
    await paste(page, 'x'.repeat(4000)); await page.locator('.attached .chip').waitFor();
    await shot(page, `long-paste-${label}`); await page.locator('.attached .thumb-x').click();
    await page.evaluate(() => { const data = new window.DataTransfer(); data.items.add(new window.File(['text'], 'guide.txt')); document.querySelector('.composer').dispatchEvent(new window.DragEvent('dragover', { dataTransfer: data, bubbles: true, cancelable: true })); });
    await shot(page, `drag-${label}`);
    await page.evaluate(() => document.querySelector('.composer').dispatchEvent(new window.DragEvent('dragleave', { bubbles: true })));

    // Delay a real file read to verify the cancellable reading state.
    await page.evaluate(() => { globalThis.fileRead = window.File.prototype.arrayBuffer; window.File.prototype.arrayBuffer = function () { return new Promise((resolve) => { globalThis.finishRead = () => globalThis.fileRead.call(this).then(resolve); }); }; });
    await page.locator('input[type=file]').setInputFiles(fixture('notes.txt'));
    await page.locator('.attachment-reading').waitFor();
    check(await page.locator('.composer .primary').isDisabled(), 'Send waits for extraction'); await shot(page, `reading-${label}`);
    await page.locator('.attachment-reading .thumb-x').click();
    await page.evaluate(() => { globalThis.finishRead(); window.File.prototype.arrayBuffer = globalThis.fileRead; });
    check(await page.locator('.attached .chip').count() === 0, 'cancelled extraction cannot attach');
    await upload(page, ['rate-limits.pdf', 'limiter.ts']);
    check(await page.locator('.composer .primary').isEnabled(), 'files alone enable Send'); await shot(page, `files-only-${label}`);
    await page.locator('.composer textarea').fill('Does the code implement the document’s burst rule?');
    await shot(page, `compose-${label}`);
    // Capture exactly what the real shared transport is handed, before the fake bridge handles it.
    await page.evaluate(async () => {
      const { TunnelTransport } = await import('/src/transport/index.ts');
      const original = TunnelTransport.prototype.fetch;
      TunnelTransport.prototype.fetch = function (path, init) { if (path === '/v1/chat/completions') globalThis.sentRequest = JSON.parse(init.body); return original.call(this, path, init); };
    });
    await page.locator('.composer .primary').click();
    await page.waitForFunction((lang) => document.querySelector('.composer .primary')?.textContent.trim() === (lang === 'zh' ? '发送' : 'Send') && Boolean(document.querySelector('.row.assistant .actions button')), lang);
    const sent = await page.evaluate(() => globalThis.sentRequest.messages.at(-1).content);
    check(typeof sent === 'string' && sent.indexOf('[file: rate-limits.pdf') < sent.indexOf('[file: limiter.ts') && sent.endsWith('Does the code implement the document’s burst rule?'), 'file blocks precede words in one text string');
    await shot(page, `sent-${label}`);
    await page.locator('.shots .chip').first().click();
    const block = await page.locator('.sheet.file pre').innerText();
    check(sent.startsWith(block), 'sheet shows the exact sent block');
    await page.locator('.sheet.file button').click();
    check(await page.evaluate(() => window.navigator.clipboard.readText()) === block, 'copy matches sent bytes');
    await shot(page, `sheet-${label}`); await page.keyboard.press('Escape');
    await page.reload(); await page.locator('.shots .chip').first().waitFor();
    check(await page.locator('.shots .chip').count() === 2, 'extracted text survives reload');
    await page.locator('.row.user .actions button').click();
    await page.locator('.editing .chip .thumb-x').first().click();
    await shot(page, `edit-${label}`);
    await page.locator('.editing .primary').click();
    await page.waitForFunction((lang) => document.querySelectorAll('.shots .chip').length === 1 && document.querySelector('.composer .primary')?.textContent.trim() === (lang === 'zh' ? '发送' : 'Send') && Boolean(document.querySelector('.row.assistant .actions button')), lang);
    await page.locator('.meters').click();
    check((await page.locator('.sheet').innerText()).includes(lang === 'zh' ? '文件以其文字发送' : 'A file is sent as its text'), 'limits paragraph present');
    await shot(page, `limits-${label}`); await page.locator('.sheet .primary').click();
    await page.locator('input[type=file]').setInputFiles(fixture('scan.pdf'));
    await page.locator('.hint.image-notice').waitFor();
    check((await page.locator('.hint.image-notice').innerText()).includes(lang === 'zh' ? '看不了图片' : 'can’t see images'), 'scan no-vision hint');
    await shot(page, `no-text-${label}`);
    await page.locator('input[type=file]').setInputFiles({ name: 'budget.txt', mimeType: 'text/plain', buffer: Buffer.from('a'.repeat(20_000)) });
    await page.locator('.attached-line.bad').waitFor(); await shot(page, `context-cap-${label}`);
    check(await page.locator('.attached .chip').count() === 0, 'oversized file refused');
    await page.locator('input[type=file]').setInputFiles({ name: 'archive.zip', mimeType: 'application/zip', buffer: Buffer.from('not a supported document') });
    await page.locator('.hint.image-notice').waitFor(); await shot(page, `unsupported-${label}`);
    await page.locator('input[type=file]').setInputFiles(Array.from({ length: 5 }, (_, n) => ({ name: `note-${n}.txt`, mimeType: 'text/plain', buffer: Buffer.from('The burst multiplier is two.') })));
    await page.locator('.attached-line.bad').waitFor();
    check(await page.locator('.attached .chip').count() === 4, 'count refusal preserves the four admitted files');
    await shot(page, `count-cap-${label}`);
    while (await page.locator('.attached .chip .thumb-x').count()) await page.locator('.attached .chip .thumb-x').first().click();
    // A retained file from an older/larger-context conversation spends the storage budget.
    await page.evaluate(() => {
      const key = Object.keys(window.localStorage).find((key) => { try { return key.startsWith('bn.conversations') && Array.isArray(JSON.parse(window.localStorage.getItem(key)).messages); } catch { return false; } });
      const conv = JSON.parse(window.localStorage.getItem(key));
      conv.messages = [{ id: 'retained', role: 'user', content: 'Stored file.', files: [{ name: 'retained.txt', kind: 'TXT', text: 's'.repeat(512 * 1024), source: 'file' }] }];
      window.localStorage.setItem(key, JSON.stringify(conv));
    });
    await page.reload(); await page.locator('.bubble.pending').waitFor(); await shot(page, `undelivered-${label}`);
    await page.locator('input[type=file]').setInputFiles(fixture('notes.txt')); await page.locator('.attached-line.bad').waitFor();
    check((await page.locator('.attached-line.bad').innerText()).includes('512 KiB'), 'retained extracted-text budget uses approved danger copy');
    await shot(page, `storage-cap-${label}`);
    await page.context().close();
    const mixed = await connected(width, lang, true);
    await upload(mixed, ['limiter.ts']);
    await mixed.evaluate(async () => {
      const canvas = document.createElement('canvas'); canvas.width = 64; canvas.height = 64;
      const ctx = canvas.getContext('2d'); ctx.fillStyle = 'red'; ctx.fillRect(0, 0, 64, 64);
      const blob = await new Promise((resolve) => canvas.toBlob(resolve));
      const data = new window.DataTransfer(); data.items.add(new window.File([blob], 'red.png', { type: 'image/png' }));
      document.querySelector('.composer').dispatchEvent(new window.DragEvent('drop', { dataTransfer: data, bubbles: true, cancelable: true }));
    });
    await mixed.locator('.attached .thumb img').waitFor();
    check(await mixed.locator('.attached > :first-child').evaluate((el) => el.classList.contains('chip')), 'mixed row retains file-then-image attach order');
    await shot(mixed, `mixed-${label}`);
    await mixed.locator('input[type=file]').setInputFiles(fixture('scan.pdf')); await mixed.locator('.hint.image-notice').waitFor();
    check((await mixed.locator('.hint.image-notice').innerText()).includes(lang === 'zh' ? '改为作为图片添加' : 'Attach it as an image instead'), 'scan vision hint');
    await shot(mixed, `no-text-vision-${label}`); await mixed.context().close();
  }
  const page = await connected(1280, 'en');
  await page.locator('.composer textarea').fill('Keep this draft.'); await paste(page, 'x'.repeat(4000));
  await page.locator('.attached .chip').waitFor();
  check(await page.locator('.composer textarea').inputValue() === 'Keep this draft.', 'long paste preserves draft');
  await shot(page, 'long-paste-1280-en');
  await page.locator('.attached .thumb-x').click();
  await page.locator('input[type=file]').setInputFiles(Array.from({ length: 5 }, (_, n) => ({ name: `note-${n}.txt`, mimeType: 'text/plain', buffer: Buffer.from('The burst multiplier is two.') })));
  await page.locator('.attached-line.bad').waitFor();
  check(await page.locator('.attached .chip').count() === 4, 'fifth file refused without removing accepted files');
  check((await page.locator('.attached-line.bad').innerText()).includes('Up to 4 files'), 'approved count danger copy');
  await shot(page, 'count-cap-1280-en');
  assert.deepEqual(errors, []); console.log(`File composer checks: ${checks} passed / 0 failed / 0 skipped`);
} catch (error) { if (currentPage && !currentPage.isClosed()) { console.error(await currentPage.locator('.composer').innerText()); await currentPage.screenshot({ path: `${out}/failure.png` }); } throw error; } finally { await browser.close(); }
