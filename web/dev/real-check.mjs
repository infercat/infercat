// Ticket 014's real-stack evidence: the built bundle, over the real relay, against a real
// `bunny-network serve` that this script starts, kills and restarts. The fakes cannot prove the
// three things this proves — what a send costs the friend's meter, what a host going to sleep
// looks like, and that a broken session is replaced rather than retried.
//
//   INVITE=bn1.… BN_BIN=/path/bunny-network BN_DATA_DIR=/path/data GW=http://127.0.0.1:6609 \
//     UPSTREAM=http://127.0.0.1:18080 node dev/real-check.mjs [cost|asleep|reconnect|all]
//
// It only ever kills processes it started itself.
import { spawn, spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, '..');
const shots = join(here, 'screenshots');
const { INVITE, BN_BIN, BN_DATA_DIR } = process.env;
const GW = process.env.GW ?? 'http://127.0.0.1:6609';
const UPSTREAM = process.env.UPSTREAM ?? 'http://127.0.0.1:18080';
const LISTEN = GW.replace(/^https?:\/\//, '').replace(/\/.*$/, '');
const PREVIEW = Number(process.env.PREVIEW_PORT ?? 6611);
const HOST_NAME = process.env.HOST_NAME ?? "Max's laptop";
const only = process.argv[2] ?? 'all';
const SECRET = (INVITE ?? '').split('.')[2];
if (!INVITE || !BN_BIN || !BN_DATA_DIR) throw new Error('INVITE, BN_BIN and BN_DATA_DIR are required');

const kids = [];
process.on('exit', () => kids.forEach((c) => c.kill('SIGTERM')));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const problems = [];

async function waitFor(url, what, tries = 300) {
  for (let i = 0; i < tries; i++) {
    try { if ((await fetch(url)).ok) return; } catch { /* not up yet */ }
    await sleep(200);
  }
  throw new Error(`${what} never came up at ${url}`);
}
/** Starts the host this script owns. Returns its child, which is the only thing we ever kill. */
function startHost() {
  const h = spawn(BN_BIN, ['--data-dir', BN_DATA_DIR, 'serve', '--upstream', UPSTREAM,
    '--dev-listen', LISTEN, '--name', HOST_NAME], { stdio: 'ignore' });
  kids.push(h);
  return h;
}
function killHost(child) {
  child.kill('SIGKILL');
}
const events = () => {
  try { return readFileSync(join(BN_DATA_DIR, 'usage.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse); }
  catch { return []; }
};
const since = (n) => events().slice(n).map((e) => `${e.endpoint} ${e.status}`);
async function usage() {
  const r = await fetch(`${GW}/me`, { headers: { authorization: `Bearer ${SECRET}` } });
  return (await r.json()).usage;
}
async function connected(browser) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });
  page.on('pageerror', (e) => problems.push(`pageerror ${e.message}`));
  page.on('console', (m) => m.type() === 'error' && problems.push(`console ${m.text()}`));
  await page.goto(`http://127.0.0.1:${PREVIEW}/#${encodeURIComponent(INVITE)}`);
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.waitForSelector('.empty', { timeout: 120_000 });
  return page;
}
const shot = (page, name) => page.screenshot({ path: join(shots, `14-real-${name}.png`) });

// --- what one message costs the friend (promise 5) --------------------------------------------
async function cost(browser) {
  let mark = events().length;
  const page = await connected(browser);
  await sleep(1500);
  console.log('\nCONNECT →', since(mark).join(' · ') || '(nothing)');
  console.log('  rpm_used after connect:', (await usage()).rpm_used);
  console.log('  meters:', JSON.stringify(await page.locator('.meters').innerText()));
  console.log('  path:  ', JSON.stringify(await page.locator('.path').innerText()));
  console.log('  model: ', JSON.stringify(await page.locator('.who .dim').innerText()));
  if (since(mark).some((e) => e.startsWith('/v1/models'))) {
    problems.push('connect asked for the model list even though /me carried it (promise 5)');
  }
  for (const n of [1, 2]) {
    mark = events().length;
    const before = await usage();
    await page.locator('.composer textarea').fill(`Say hi in three words. (${n})`);
    await page.getByRole('button', { name: 'Send' }).click();
    await page.waitForSelector('.composer button:has-text("Send")', { timeout: 180_000 });
    await sleep(2500);
    const after = await usage();
    const spent = after.rpm_used - before.rpm_used;
    console.log(`SEND #${n} →`, since(mark).join(' · '), `| rpm_used ${before.rpm_used} → ${after.rpm_used}`);
    console.log('  meters:', JSON.stringify(await page.locator('.meters').innerText()));
    if (spent !== 1) problems.push(`one message cost ${spent} requests, not 1 (promise 5)`);
  }
  await shot(page, 'stack');
  await page.close();
}

// --- a host that goes to sleep mid-send (promise 1) -------------------------------------------
async function asleep(browser, host) {
  const page = await connected(browser);
  killHost(host);
  await sleep(2000);
  const t0 = Date.now();
  await page.locator('.composer textarea').fill('Are you still there?');
  await page.getByRole('button', { name: 'Send' }).click();
  await page.waitForSelector('.waiting:has-text("Still waiting")', { timeout: 30_000 });
  const noticeS = (Date.now() - t0) / 1000;
  console.log(`\n"${await page.locator('.waiting').innerText()}" at ${noticeS.toFixed(0)} s`);
  await shot(page, 'asleep-waiting');
  await page.waitForSelector('.row.assistant .ended', { timeout: 60_000 });
  const failS = (Date.now() - t0) / 1000;
  const copy = await page.locator('.row.assistant .ended').innerText();
  console.log(`failed at ${failS.toFixed(0)} s: ${JSON.stringify(copy)}`);
  if (noticeS > 9) problems.push(`the "still waiting" line took ${noticeS.toFixed(0)} s`);
  if (failS > 22) problems.push(`the failure took ${failS.toFixed(0)} s, not ~15`);
  for (const raw of ['context deadline', 'dial port', 'closed inside the response']) {
    if (copy.includes(raw)) problems.push(`raw transport string in primary copy: ${raw}`);
  }
  await page.waitForSelector('.meter-label:has-text("—")', { timeout: 40_000 });
  console.log('  pending turn kept:', (await page.locator('.bubble.pending').count()) === 1);
  console.log('  action:', JSON.stringify(await page.locator('.row.assistant .actions button').last().innerText()));
  console.log('  header:', JSON.stringify(await page.locator('.degraded').innerText()));
  console.log('  path:  ', JSON.stringify(await page.locator('.path').innerText()));
  console.log('  meters:', JSON.stringify(await page.locator('.meters').innerText()));
  if ((await page.locator('.path').innerText()).includes('relayed via')) {
    problems.push('the path pill still shows a live latency for a host that is not answering');
  }
  await shot(page, 'asleep-failed');
  return page;
}

// --- and the reconnect once it comes back (promise 13) ----------------------------------------
async function reconnect(page) {
  const host = startHost();
  await waitFor(`${GW}/healthz`, 'the host coming back');
  await sleep(2000);
  const t0 = Date.now();
  await page.getByRole('button', { name: 'Reconnect' }).click();
  await page.waitForSelector('.composer textarea', { timeout: 120_000 });
  await page.waitForFunction(
    () => !document.querySelector('.path')?.textContent?.includes('not answering'),
    { timeout: 60_000 },
  );
  const s = (Date.now() - t0) / 1000;
  console.log(`\nreconnected in ${s.toFixed(1)} s — path ${JSON.stringify(await page.locator('.path').innerText())}`);
  if (s > 20) problems.push(`the reconnect took ${s.toFixed(0)} s`);
  await page.locator('.composer textarea').fill('Say hi in three words.');
  await page.getByRole('button', { name: 'Send' }).click();
  await page.waitForSelector('.meta-text:has-text("out")', { timeout: 120_000 });
  console.log('  and the thread works again.');
  await shot(page, 'reconnected');
  return host;
}

async function main() {
  spawnSync('node', ['node_modules/vite/bin/vite.js', 'build'], { cwd: web, stdio: 'inherit' });
  const prev = spawn('node', ['node_modules/vite/bin/vite.js', 'preview', '--port', String(PREVIEW), '--strictPort'],
    { cwd: web, stdio: ['ignore', 'ignore', 'inherit'] });
  kids.push(prev);
  await waitFor(`http://127.0.0.1:${PREVIEW}`, 'vite preview');

  let host = startHost();
  await waitFor(`${GW}/healthz`, 'the host');
  const browser = await chromium.launch();

  if (only === 'cost' || only === 'all') await cost(browser);
  if (only === 'asleep' || only === 'reconnect' || only === 'all') {
    const page = await asleep(browser, host);
    if (only === 'reconnect' || only === 'all') host = await reconnect(page);
  }

  await browser.close();
  kids.forEach((c) => c.kill('SIGTERM'));
  if (problems.length > 0) {
    console.error('\nProblems:');
    for (const p of problems) console.error(`  ${p}`);
    process.exit(1);
  }
  console.log('\nReal stack: every promise above held, and no console or page errors.');
}

main().catch((err) => {
  console.error(err);
  kids.forEach((c) => c.kill('SIGTERM'));
  process.exit(1);
});
