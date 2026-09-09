// 083: production worker, real Chrome install, and an owned host/key/data directory.
import { chromium, devices } from 'playwright';
import { createServer } from 'node:http';
import { spawn, execFile, execFileSync } from 'node:child_process';
import { mkdtemp, mkdir, readFile, writeFile, rm, cp } from 'node:fs/promises';
import { join, extname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
const repo = fileURLToPath(new URL('../../', import.meta.url)), web = join(repo, 'web');
const out = process.env.PWA_PROOF_OUT ?? '/private/tmp/infercat-083-proof'; await mkdir(out, { recursive: true });
const work = await mkdtemp('/private/tmp/infercat-083-proof-'), data = join(work, 'host'), profile = join(work, 'chrome');
const a = join(work, 'a'), b = join(work, 'b'); await cp(join(web, 'dist'), a, { recursive: true });
execFileSync('pnpm', ['build', '--outDir', b], { cwd: web, env: { ...process.env, VITE_APP_VERSION: '0.1.2-083-update' }, stdio: ['ignore', 'pipe', 'pipe'] });
const configA = JSON.parse(await readFile(join(a, 'precache.json'))), configB = JSON.parse(await readFile(join(b, 'precache.json')));
let directory = a, failRuntime = false, host, context, cdp, pwa, installed = false, passed = 0;
const requests = [], errors = [];
const types = { '.html': 'text/html', '.js': 'text/javascript', '.mjs': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.webmanifest': 'application/manifest+json', '.png': 'image/png', '.svg': 'image/svg+xml', '.woff2': 'font/woff2', '.gz': 'application/gzip' };
const server = createServer(async (req, res) => {
  const pathname = decodeURIComponent(new URL(req.url, 'http://localhost').pathname); requests.push(pathname);
  if (failRuntime && pathname === configB.runtime[1]) { res.writeHead(503); res.end('fixture interrupted update'); return; }
  const path = resolve(directory, '.' + (pathname === '/' ? '/index.html' : pathname));
  if (!path.startsWith(directory + '/')) { res.writeHead(404); res.end(); return; }
  try { const body = await readFile(path); res.writeHead(200, { 'content-type': types[extname(path)] ?? 'application/octet-stream', 'cache-control': 'no-store' }); res.end(body); }
  catch { res.writeHead(404); res.end(); }
});
await new Promise((ready) => server.listen(49183, '127.0.0.1', ready)); const base = 'http://127.0.0.1:49183/';
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(read, name, timeout = 30_000) { const end = Date.now() + timeout; while (Date.now() < end) { if (await read()) return; await wait(50); } throw new Error('Timed out: ' + name); }
function cli(args) { return execFileSync(join(repo, 'bin/infercat'), [...args, '--data-dir', data], { cwd: repo, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }); }
function check(name, value = true) { assert.ok(value, name); passed++; console.log(`PASS ${name}`); }
async function shot(page, name) { await page.evaluate(() => document.fonts.ready); await page.screenshot({ path: join(out, name + '.png'), fullPage: false }); }
async function agent(args) { return await new Promise((done, fail) => execFile('npx', ['--yes', 'agent-browser', '--session', 'infercat083', ...args], { cwd: web, timeout: 60_000 }, (error, stdout) => error ? fail(error) : done(stdout))); }
async function settle(page) { await page.locator('.composer textarea').waitFor({ timeout: 90_000 }); await page.waitForFunction(() => !document.querySelector('.composer textarea').disabled, null, { timeout: 90_000 }); }
try {
  // Required immediate dev-server gut-check, before other integration work.
  await agent(['open', base]);
  const snapshot = await agent(['snapshot', '-i']);
  assert.match(snapshot, /Connect|Invite/);
  await agent(['screenshot', join(out, 'initial-card.png')]);
  const initialErrors = await agent(['errors']);
  assert.equal(initialErrors.trim(), ''); check('agent-browser production page renders without page errors');
  await agent(['close']);
  const engine = process.env.INFERCAT_PWA_ENGINE;
  assert.match(engine ?? '', /^http:\/\/127\.0\.0\.1:\d+$/);
  host = spawn(join(repo, 'bin/infercat'), ['serve', '--data-dir', data, '--upstream', engine, '--slots', '1', '--name', '083 isolated host'], { cwd: repo, stdio: ['ignore', 'pipe', 'pipe'] });
  host.stdout.resume(); host.stderr.resume();
  for (let i = 0; ; i++) { try { cli(['status']); break; } catch { if (i >= 60 || host.exitCode !== null) throw new Error('Own host failed to start'); await wait(1000); } }
  const { invite } = JSON.parse(cli(['keys', 'add', 'pwa-proof', '--json', '--max-output-tokens', '96']));
  context = await chromium.launchPersistentContext(profile, { channel: 'chrome', headless: false, viewport: { width: 1280, height: 900 }, locale: 'en-US' });
  await context.addInitScript(() => { window.__installSeen = false; window.addEventListener('beforeinstallprompt', () => { window.__installSeen = true; }); });
  const page = context.pages()[0]; page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(base + '#' + invite); await settle(page);
  await page.evaluate(() => { for (const k of Object.keys(window.localStorage).filter((k) => k.startsWith('bn.settings.'))) window.localStorage.setItem(k, JSON.stringify({ ...JSON.parse(window.localStorage.getItem(k)), thinking: 'off' })); });
  await page.reload(); await settle(page);
  await page.locator('.composer textarea').fill('Say READY in one word.'); await page.locator('.composer button.primary').click();
  await page.locator('.row.assistant .actions button').first().waitFor({ timeout: 180_000 });
  await page.waitForFunction(() => document.querySelector('.composer button.primary')?.textContent.trim() === 'Send');
  check('own host completed a reply over the production wasm bridge');
  console.log('OWN HOST PATH', await page.locator('.path').innerText());
  await page.evaluate(() => window.navigator.serviceWorker.ready); await page.waitForFunction(() => window.navigator.serviceWorker.controller !== null);
  await page.waitForFunction(() => window.__installSeen === true, null, { timeout: 30_000 });
  check('native beforeinstallprompt observed and held');
  cdp = await context.newCDPSession(page);
  const installability = await cdp.send('Page.getInstallabilityErrors'); assert.deepEqual(installability.installabilityErrors, []); check('CDP installability errors empty');
  const manifest = await cdp.send('Page.getAppManifest'); assert.deepEqual(manifest.errors, []);
  const parsed = JSON.parse(manifest.data); assert.equal(parsed.id, '/'); assert.equal(parsed.display, 'standalone'); assert.equal(parsed.icons.length, 5); check('manifest validates with all five icon declarations');
  // Chrome device emulation, explicitly not an Android OS installation.
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  await cdp.send('Emulation.setTouchEmulationEnabled', { enabled: true });
  await cdp.send('Network.setUserAgentOverride', { userAgent: devices['Pixel 7'].userAgent });
  await page.reload(); await settle(page); await page.locator('.toast.install').waitFor({ timeout: 30_000 }); await shot(page, 'install-suggestion-390');
  await page.getByRole('button', { name: 'Not now', exact: true }).click();
  await page.reload(); await settle(page); assert.equal(await page.locator('.toast.install').count(), 0); check('Android Chrome emulation dismisses the suggestion once');
  // iOS manual path uses the same app under explicit UA/device emulation, not a real iOS claim.
  await cdp.send('Network.setUserAgentOverride', { userAgent: devices['iPhone 13'].userAgent });
  await page.reload(); await settle(page); await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('button', { name: 'Add to Home Screen', exact: true }).click();
  await page.getByRole('button', { name: 'Copy invite', exact: true }).waitFor(); await shot(page, 'ios-manual-path-390');
  check('iOS emulation shows copy-invite three-step sheet');
  await cdp.send('Emulation.clearDeviceMetricsOverride'); await cdp.send('Emulation.setTouchEmulationEnabled', { enabled: false });
  await cdp.send('Network.setUserAgentOverride', { userAgent: '' });
  await page.reload(); await settle(page);
  pwa = await context.browser().newBrowserCDPSession();
  await pwa.send('PWA.install', { manifestId: base, installUrlOrBundleUrl: base }); installed = true;
  await pwa.send('PWA.changeAppUserSettings', { manifestId: base, displayMode: 'standalone' });
  await cdp.send('PWA.openCurrentPageInApp', { manifestId: base });
  const app = page;
  await app.waitForFunction(() => window.matchMedia('(display-mode: standalone)').matches, null, { timeout: 30_000 });
  await settle(app);
  check('real desktop PWA window reports standalone', await app.evaluate(() => window.matchMedia('(display-mode: standalone)').matches));
  await app.getByRole('button', { name: 'Settings', exact: true }).click();
  await app.getByText('Installed as an app — this is its window.', { exact: true }).waitFor(); await shot(app, 'desktop-install-1280'); await app.keyboard.press('Escape');
  const appNetwork = await context.newCDPSession(app);
  let networkOffline = false;
  await appNetwork.send('Runtime.enable');
  appNetwork.on('Runtime.executionContextCreated', () => { if (networkOffline) void appNetwork.send('Network.overrideNetworkState', { offline: true, latency: 0, downloadThroughput: -1, uploadThroughput: -1 }).catch(() => {}); });
  const offline = async (value) => {
    networkOffline = value;
    await context.setOffline(value);
    await appNetwork.send('Network.emulateNetworkConditions', { offline: value, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
    await appNetwork.send('Network.overrideNetworkState', { offline: value, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
    assert.equal(await app.evaluate(() => window.navigator.onLine), !value);
  };
  await offline(true); await app.reload(); await settle(app);
  await app.getByText('No network on this device', { exact: true }).waitFor();
  await app.locator('.composer textarea').fill('Draft kept through offline recovery');
  assert.equal(await app.locator('.composer button.primary').isDisabled(), true); await app.locator('.composer textarea').press('Enter');
  assert.equal(await app.locator('.composer textarea').inputValue(), 'Draft kept through offline recovery');
  check('real worker offline reload restores readable history and refuses Send'); await shot(app, 'offline-shell-1280');
  await app.getByRole('button', { name: 'Edit', exact: true }).click();
  await app.locator('.bubble.editing textarea').fill('Edited while offline');
  assert.equal(await app.locator('.edit-actions .primary').isDisabled(), true);
  await shot(app, 'edited-draft-offline');
  await offline(false); await app.waitForFunction(() => !document.querySelector('.composer button.primary')?.disabled, null, { timeout: 90_000 });
  assert.equal(await app.locator('.composer textarea').inputValue(), 'Draft kept through offline recovery'); check('online redial preserves the draft without replay');
  assert.equal(await app.locator('.bubble.editing textarea').inputValue(), 'Edited while offline');
  assert.equal(await app.locator('.edit-actions .primary').isDisabled(), false);
  check('blocked replacement preserves edited text through recovery');
  await app.locator('.edit-actions .ghost').click();
  const cacheBefore = await app.evaluate(async () => { const result = {}; for (const name of await window.caches.keys()) result[name] = (await (await window.caches.open(name)).keys()).map((r) => new URL(r.url).pathname); return result; });
  assert.deepEqual(cacheBefore[`infercat-runtime-${configA.version}`].sort(), configA.runtime.sort());
  for (const urls of Object.values(cacheBefore)) for (const path of urls) assert.ok([...configA.shell, ...configA.runtime].includes(path));
  check('CacheStorage contains only the generated static allowlist and matching runtime pair');
  const runtimeReads = requests.filter((p) => configA.runtime.includes(p)).length;
  await app.reload(); await settle(app); assert.equal(requests.filter((p) => configA.runtime.includes(p)).length, runtimeReads); check('same-version reload reuses the cached runtime pair');
  directory = b; failRuntime = true;
  await app.evaluate(async () => { const reg = await window.navigator.serviceWorker.getRegistration(); await reg.update(); }); await wait(2000);
  assert.equal(await app.evaluate(() => window.caches.has('infercat-runtime-0.1.2-dev')), true);
  check('interrupted new-runtime install leaves the old worker usable');
  failRuntime = false;
  await app.evaluate(async () => { const reg = await window.navigator.serviceWorker.getRegistration(); await reg.update(); });
  await until(() => app.evaluate(async () => (await window.navigator.serviceWorker.getRegistration())?.waiting?.state === 'installed'), 'new worker installed and waiting');
  check('new worker waits while the old app remains open');
  let nextWorker;
  for (const worker of context.serviceWorkers()) { try { if (await worker.evaluate('shellName') === `infercat-shell-${configB.revision}`) nextWorker = worker; } catch { /* A failed installer may have exited. */ } }
  assert.ok(nextWorker, 'waiting worker has the new build identity');
  await app.close();
  await until(() => nextWorker.evaluate('!self.registration.waiting && self.registration.active?.state === "activated"'), 'new worker activates after the old app closes');
  // Closing every controlled window allows the new worker to activate without skipWaiting.
  const updated = await context.newPage(); await updated.goto(base); await settle(updated);
  await until(() => updated.evaluate(async (revision) => !(await window.caches.keys()).includes('infercat-shell-' + revision), configA.revision), 'old cache retired after activation');
  const updatedNetwork = await context.newCDPSession(updated);
  const unavailable = { offline: true, latency: 0, downloadThroughput: -1, uploadThroughput: -1 };
  await updatedNetwork.send('Runtime.enable');
  updatedNetwork.on('Runtime.executionContextCreated', () => { void updatedNetwork.send('Network.overrideNetworkState', unavailable).catch(() => {}); });
  await context.setOffline(true); await updatedNetwork.send('Network.emulateNetworkConditions', unavailable); await updatedNetwork.send('Network.overrideNetworkState', unavailable);
  await updated.reload(); await settle(updated);
  const finalCaches = await updated.evaluate(() => window.caches.keys()); assert.deepEqual(finalCaches.sort(), [`infercat-shell-${configB.revision}`, `infercat-runtime-${configB.version}`].sort());
  check('after old windows close the updated shell opens offline');
  await shot(updated, 'updated-offline'); assert.deepEqual(errors, []);
  await writeFile(join(out, 'result.json'), JSON.stringify({ passed, failed: 0, skipped: 0, installability, versions: [configA.version, configB.version], finalCaches, android: 'Chrome device emulation', ios: 'UA/device emulation only; founder verifies actual phone' }, null, 2));
  console.log(`PWA proof: ${passed} passed / 0 failed / 0 skipped`);
} catch (error) {
  if (context) { for (const [i, page] of context.pages().entries()) { try { console.log('DIAGNOSTIC', await page.evaluate(() => ({ online: window.navigator.onLine, standalone: window.matchMedia('(display-mode: standalone)').matches, path: document.querySelector('.path')?.textContent, card: !!document.querySelector('.connect-card'), sw: window.navigator.serviceWorker.controller?.scriptURL }))); await shot(page, `failure-${i}`); } catch { /* A closing page has no screenshot. */ } } }
  throw error;
} finally {
  if (installed) try { await pwa?.send('PWA.uninstall', { manifestId: base }); } catch { /* context close follows */ }
  await context?.close();
  if (host && host.exitCode === null) { host.kill('SIGTERM'); await new Promise((done) => { const timer = setTimeout(() => host.kill('SIGKILL'), 10_000); host.once('exit', () => { globalThis.clearTimeout(timer); done(); }); }); }
  server.closeAllConnections(); await new Promise((done) => server.close(done)); await rm(work, { recursive: true, force: true });
  console.log('Own host, test key, Chrome profile and temporary builds removed.');
}
