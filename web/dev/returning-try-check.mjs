// With no URL, test built web/dist behind a real 302. With an origin, verify the deployed worker.
// BROWSERS=chromium,webkit enables the local Safari regression without expanding CI's install.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { resolve, extname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium, webkit } from 'playwright';
const root = fileURLToPath(new URL('../dist/', import.meta.url));
let server, origin = process.argv[2];
if (!origin) {
  server = createServer(async (req, res) => {
    const path = new URL(req.url, 'http://localhost').pathname;
    if (path === '/try') { res.writeHead(302, { Location: '/?from=try#ic2.fixture' }).end(); return; }
    const file = resolve(root, '.' + (path === '/' ? '/index.html' : path));
    if (!file.startsWith(root)) { res.writeHead(404).end(); return; }
    try { const bytes = await readFile(path === '/sw.js' && process.env.SW_FIXTURE ? process.env.SW_FIXTURE : file); res.setHeader('Content-Type', ({'.html':'text/html','.js':'text/javascript','.css':'text/css','.json':'application/json'})[extname(file)] ?? 'application/octet-stream'); res.end(bytes); }
    catch { res.writeHead(404).end(); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  origin = `http://127.0.0.1:${server.address().port}`;
}
let failed = false;
try {
  for (const name of (process.env.BROWSERS ?? 'chromium').split(',')) {
    const engine = { chromium, webkit }[name]; assert.ok(engine, 'Supported browser required');
    const browser = await engine.launch();
    try {
      const page = await browser.newPage();
      await page.goto(new URL('/', origin).href);
      await page.waitForFunction(() => window.navigator.serviceWorker.controller !== null, undefined, { timeout: 60000 });
      await page.evaluate(() => window.navigator.serviceWorker.ready.then(() => undefined));
      // Exercise a settled returning navigation, not the initial controller-claim race.
      await page.reload();
      await page.waitForFunction(() => window.navigator.serviceWorker.controller?.state === 'activated', undefined, { timeout: 60000 });
      let landed;
      // The app consumes and clears the fragment; capture the committed navigation first.
      page.on('framenavigated', frame => { if (frame === page.mainFrame()) { const url = new URL(frame.url()); if (url.searchParams.get('from') === 'try' && url.hash.startsWith('#ic')) landed = url; } });
      await page.goto(new URL('/try', origin).href);
      assert.ok(landed, 'redirect navigation carries attribution and invite before app consumption');
      assert.equal(landed.origin, new URL(origin).origin, 'same-origin landing');
      assert.equal(landed.searchParams.get('from'), 'try', 'redirect attribution');
      assert.ok(landed.hash.startsWith('#ic'), 'redirect carries invite');
      console.log(`RETURNING /try ${name} PASS (controlled page → redirect → invite)`);
    } catch (error) {
      // A real /try destination contains a credential: do not print browser URLs/errors.
      failed = true; console.error(`RETURNING /try ${name} FAIL (worker navigation or redirect assertion)`);
      if (server) console.error(String(error.message).split('\n')[0]);
    } finally { await browser.close(); }
  }
} finally { if (server) await new Promise(resolve => server.close(resolve)); }
if (failed) process.exitCode = 1;
