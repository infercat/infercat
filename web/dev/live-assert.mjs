// Post-deploy gate: node dev/live-assert.mjs [https://infercat.ai/try]
// INVITE optionally supplies an invite without printing it. No message is sent.
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
import { chromium } from 'playwright';

export async function assertLive(page, url) {
  const errors = [];
  const onError = (e) => errors.push(String(e));
  const onConsole = (m) => { if (m.type() === 'error') errors.push(m.text()); };
  page.on('pageerror', onError);
  page.on('console', onConsole);
  try {
    await page.goto(url);
    // This status is a paragraph below the host heading, not an h1.
    await page.locator('.empty').getByText(/is listening/).waitFor({ timeout: 120_000 });
    const composer = page.locator('.composer textarea');
    await composer.waitFor();
    assert.equal(await composer.isEnabled(), true, 'composer must be enabled');
    const accept = await page.locator('.composer input[type=file]').getAttribute('accept').catch(() => '');
    const observed = {
      composer: true,
      images: /image\//.test(accept ?? ''),
      microphone: await page.locator('.composer .mic').count() > 0,
      listen: await page.getByRole('button', { name: 'Listen', exact: true }).count() > 0,
    };
    assert.deepEqual(errors, [], 'browser errors');
    return observed;
  } finally {
    page.off('pageerror', onError);
    page.off('console', onConsole);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const url = new URL(process.argv[2] ?? 'https://infercat.ai/try');
  if (process.env.INVITE) { url.pathname = '/'; url.hash = process.env.INVITE; }
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ locale: 'en-US' });
    console.log('LIVE PASS', JSON.stringify(await assertLive(page, url.href)));
  } finally { await browser.close(); }
}
