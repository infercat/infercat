// Real Connect → Chat rendering against captured /me responses, no relay or model required.
import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';
import { createServer } from 'vite';
import { chromium } from 'playwright';
import { assertLive } from './live-assert.mjs';
const invite = `ic1.tcCOMPATproofaddressCOMPATproofaddress.${'D'.repeat(43)}`;
const server = await createServer({ server: { port: 0, strictPort: false } });
await server.listen();
const base = server.resolvedUrls.local[0];
const browser = await chromium.launch({ headless: true });
let passed = 0;
try {
  for (const version of ['0.1.0', '0.1.1', 'current']) {
    const fixture = JSON.parse(readFileSync(new URL(`../src/fixtures/me/${version}.json`, import.meta.url), 'utf8'));
    const context = await browser.newContext({ locale: 'en-US' });
    try {
      const page = await context.newPage();
      let reads = 0;
      const respond = async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === '/me') reads++;
        const body = path === '/me' ? fixture : path === '/v1/models' ? { data: fixture.host.models.map((id) => ({ id })) } : { status: 'ok' };
        await route.fulfill({ json: body, headers: { 'access-control-allow-origin': '*' } });
      };
      await context.route('http://127.0.0.1:49090/**', respond);
      const observed = await assertLive(page, `${base}?direct#${invite}`);
      assert.ok(reads > 0, 'Connect verified the fixture through /me');
      assert.equal(observed.images, fixture.host.vision?.[fixture.host.models[0]] === true);
      assert.equal(observed.microphone, false);
      assert.equal(observed.listen, false);
      // The + remains available for files on old hosts; image MIME types must be absent.
      assert.equal(await page.locator('.composer .attach').count(), 1);
      console.log(`COMPAT ${version} PASS ${JSON.stringify(observed)}`);
      if (process.env.COMPAT_SHOTS) await page.screenshot({ path: `${process.env.COMPAT_SHOTS}/compat-${version}.png` });
      passed++;
      await page.close();
      if (version === '0.1.0') {
        for (const failure of ['console', 'pageerror']) {
          const probeContext = await browser.newContext({ locale: 'en-US' });
          await probeContext.route('http://127.0.0.1:49090/**', respond);
          const probe = await probeContext.newPage();
          await probe.addInitScript((kind) => {
            if (kind === 'console') console.error('compatibility gate injected error');
            else throw new Error('compatibility gate injected exception');
          }, failure);
          await assert.rejects(assertLive(probe, `${base}?direct#${invite}`), /browser errors/);
          await probeContext.close();
          console.log(`LIVE rejects ${failure} PASS`);
          passed++;
        }
      }
    } finally { await context.close(); }
  }
  console.log(`Host compatibility: ${passed} passed / 0 failed / 0 skipped`);
} finally { await browser.close(); await server.close(); }
