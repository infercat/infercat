// Design-state evidence. Device/visibility/network flags here are emulated; pwa-check owns real SW/install proof.
import { chromium, devices } from 'playwright';
import { createServer } from 'vite';
import { execFile } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import assert from 'node:assert/strict';
const out = '/private/tmp/infercat-083-ui'; await mkdir(out, { recursive: true });
const server = await createServer({ server: { host: '127.0.0.1', port: 49184, strictPort: true } }); await server.listen();
const base = 'http://127.0.0.1:49184', invite = `ic1.tcDEMOaddressDEMOaddressDEMOaddressDEMO.${'D'.repeat(43)}`;
let browser, passed = 0; const errors = [];
async function agent(args) { return await new Promise((done, fail) => execFile('npx', ['--yes', 'agent-browser', '--session', 'infercat083ui', ...args], { timeout: 60_000 }, (error, stdout) => error ? fail(error) : done(stdout))); }
async function shot(page, name) { await page.evaluate(() => document.fonts.ready); await page.screenshot({ path: `${out}/${name}.png` }); passed++; console.log('PASS screenshot', name); }
async function ready(page) { await page.locator('.composer textarea').waitFor(); await page.waitForFunction(() => !document.querySelector('.composer textarea')?.disabled); }
async function language(page, lang) { await page.evaluate((lang) => window.localStorage.setItem('bn.language', JSON.stringify(lang)), lang); }
try {
  await agent(['open', base]); assert.match(await agent(['snapshot', '-i']), /Connect|Invite/); await agent(['screenshot', `${out}/initial-card.png`]); assert.equal((await agent(['errors'])).trim(), ''); await agent(['close']);
  browser = await chromium.launch();
  for (const lang of ['en', 'zh']) {
    const context = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, locale: lang === 'en' ? 'en-US' : 'zh-CN' });
    await context.addInitScript(() => {
      const original = window.matchMedia;
      window.matchMedia = (query) => query === '(display-mode: standalone)' ? { ...original(query), matches: window.localStorage.getItem('__proofStandalone') === 'true', addEventListener() {}, removeEventListener() {} } : original(query);
      Object.defineProperty(window.navigator, 'onLine', { get: () => window.localStorage.getItem('__proofOffline') !== 'true' });
    });
    const page = await context.newPage(); page.on('pageerror', (e) => errors.push(e.message)); page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
    await page.goto(`${base}/?fake&connectMs=20&tokenDelay=1&invite=${invite}`); await ready(page); await language(page, lang);
    await page.reload(); await ready(page);
    await page.locator('.composer textarea').fill(lang === 'en' ? 'How do I keep this chat handy?' : '如何方便地再次打开对话？'); await page.locator('.composer button.primary').click();
    await page.locator('.row.assistant .actions button').first().waitFor();
    await page.evaluate(() => { const event = new window.Event('beforeinstallprompt', { cancelable: true }); event.prompt = async () => {}; event.userChoice = Promise.resolve({ outcome: 'accepted' }); window.dispatchEvent(event); });
    await page.locator('.toast.install').waitFor(); await shot(page, `install-suggestion-390-${lang}`);
    await page.locator('.toast.install button').last().click(); assert.equal(await page.locator('.toast.install').count(), 0);
    await page.evaluate(() => window.localStorage.setItem('__proofStandalone', 'true'));
    await page.goto(`${base}/?fake&connectMs=1800&tokenDelay=1`);
    await page.locator('.path').filter({ hasText: lang === 'en' ? 'reconnecting' : '正在重连' }).waitFor();
    assert.equal(await page.locator('.composer textarea').isDisabled(), false); await shot(page, `returning-user-390-${lang}`); await ready(page);
    await page.waitForFunction(() => !document.querySelector('.path')?.textContent.includes('reconnecting') && !document.querySelector('.path')?.textContent.includes('正在重连'));
    await shot(page, `standalone-chat-390-${lang}`);
    await page.evaluate(() => { window.localStorage.setItem('__proofOffline', 'true'); window.dispatchEvent(new window.Event('offline')); });
    await page.reload(); await ready(page); await page.locator('.composer textarea').fill(lang === 'en' ? 'Draft for when the network returns' : '网络恢复后发送的草稿');
    assert.equal(await page.locator('.composer button.primary').isDisabled(), true); await shot(page, `offline-shell-390-${lang}`);
    await page.evaluate(() => { window.localStorage.removeItem('__proofOffline'); window.dispatchEvent(new window.Event('online')); });
    await page.waitForFunction(() => !document.querySelector('.composer button.primary')?.disabled); assert.notEqual(await page.locator('.composer textarea').inputValue(), '');
    await page.goto(`${base}/?fake&connectMs=20&tokenDelay=1&hostAsleep`); await ready(page);
    await page.clock.install();
    await page.locator('.composer textarea').fill(lang === 'en' ? 'Are you awake?' : '你醒着吗？'); await page.locator('.composer button.primary').click();
    await page.clock.runFor(200); await page.clock.fastForward(125_000);
    await page.locator('.row.assistant .ended.interrupted').waitFor({ timeout: 30_000 }); await shot(page, `host-asleep-standalone-390-${lang}`);
    await context.close();
    const ios = await browser.newContext({ ...devices['iPhone 13'], viewport: { width: 390, height: 844 }, locale: lang === 'en' ? 'en-US' : 'zh-CN', permissions: ['clipboard-read', 'clipboard-write'] });
    const iphone = await ios.newPage(); iphone.on('pageerror', (e) => errors.push(e.message));
    await iphone.goto(`${base}/?fake&connectMs=20&tokenDelay=1&invite=${invite}`); await ready(iphone); await language(iphone, lang); await iphone.reload(); await ready(iphone);
    await iphone.getByRole('button', { name: lang === 'en' ? 'Settings' : '设置', exact: true }).click();
    await iphone.getByRole('button', { name: lang === 'en' ? 'Add to Home Screen' : '添加到主屏幕', exact: true }).click();
    await shot(iphone, `ios-manual-path-390-${lang}`);
    await iphone.getByRole('button', { name: lang === 'en' ? 'Copy invite' : '复制邀请码', exact: true }).click();
    assert.equal(await iphone.evaluate(() => window.navigator.clipboard.readText()), invite); await ios.close();
  }
  assert.deepEqual(errors, []); await writeFile(`${out}/result.json`, JSON.stringify({ passed, failed: 0, skipped: 0, errors, deviceProof: 'UI emulation; real install and SW evidence in pwa-check' }, null, 2));
  console.log(`PWA design states: ${passed} screenshots passed / 0 failed / 0 skipped`);
} finally { await browser?.close(); await server.close(); }
