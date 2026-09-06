// Ticket 005 integration runner: the REAL web client against the REAL host (infercat serve),
// in headless chromium. Direct mode through the dev listener, Tunnel mode through the public relay
// with the built bundle, then the limits story (429 countdown, revoke, pause/resume). Screenshots
// land in dev/evidence/int-*.png; the invite textarea is masked in every shot that shows it.
//
//   INVITE_ALICE=ic1.… INVITE_BOB=ic1.… INFERCAT_BIN=/path/infercat INFERCAT_DATA_DIR=/path/data \
//   DIRECT_URL=http://127.0.0.1:9090 node dev/int-check.mjs [direct|tunnel|limits|all]
//
// Prints TTFT (DOM-observed: first reasoning/content text, raf polling, ±16 ms) and tokens/s
// (completion tokens from the usage chunk over first-token → complete). Never prints a secret.
import { spawn, spawnSync } from 'node:child_process';
import { mkdirSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, '..');
const shots = join(here, 'evidence'); // real-host evidence: viewed at landing, not tracked (037)
const WEB_PORT = Number(process.env.WEB_PORT ?? 59173);
const PREVIEW_PORT = Number(process.env.PREVIEW_PORT ?? 59174);
const DIRECT_URL = process.env.DIRECT_URL ?? 'http://127.0.0.1:9090';
const { INVITE_ALICE, INVITE_BOB, INFERCAT_BIN, INFERCAT_DATA_DIR } = process.env;
const BOB = process.env.BOB_NAME ?? 'bob'; // the --rpm 2 key's name, for revoke
const only = process.argv[2] ?? 'all';
const PROMPT = 'Why is the sky blue? Answer in three sentences.';

const children = [];
function start(name, args, env) {
  const child = spawn('node', args, { cwd: web, env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
  child.stdout.on('data', (d) => process.stdout.write(`[${name}] ${d}`));
  child.stderr.on('data', (d) => process.stderr.write(`[${name}] ${d}`));
  children.push(child);
  return child;
}
const stopAll = () => children.forEach((c) => c.kill('SIGTERM'));
process.on('exit', stopAll);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function waitFor(url, what) {
  for (let i = 0; i < 150; i++) {
    try {
      if ((await fetch(url)).ok) return;
    } catch { /* not up yet */ }
    await sleep(200);
  }
  throw new Error(`${what} never came up at ${url}`);
}

const problems = [];
function watch(page, label) {
  page.on('console', (m) => m.type() === 'error' && problems.push(`${label}: console ${m.text()}`));
  page.on('pageerror', (e) => problems.push(`${label}: pageerror ${e.message}`));
}
async function shot(page, name, opts = {}) {
  const file = join(shots, `int-${name}.png`);
  await page.screenshot({ path: file, ...opts });
  console.log(`  ${file.replace(`${web}/`, '')}  ${Math.round(statSync(file).size / 1024)} KB`);
}
const masked = (page) => ({ mask: [page.locator('textarea')] });

function keys(...args) {
  const r = spawnSync(INFERCAT_BIN, ['keys', ...args, '--data-dir', INFERCAT_DATA_DIR], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`keys ${args.join(' ')}: ${r.stderr}`);
  console.log(`  $ infercat keys ${args.join(' ')} → ${r.stdout.trim().split('\n')[0]}`);
}

// Sends one message and measures it. Returns {ttftMs, totalMs, out, tokPerS, meta}.
async function chat(page, label, text, { thinkingShot, completeShot } = {}) {
  const before = await page.locator('.row.assistant').count();
  await page.locator('.composer textarea').fill(text);
  const t0 = performance.now();
  await page.getByRole('button', { name: 'Send' }).click();
  // First token: the NEW assistant row (not an earlier one) shows reasoning or content text.
  await page.waitForFunction(
    (n) => {
      const rows = document.querySelectorAll('.row.assistant');
      if (rows.length <= n) return false;
      const last = rows[rows.length - 1];
      const t = last.querySelector('.thinking-body')?.textContent ?? '';
      const c = last.querySelector('.md')?.textContent ?? '';
      return t.length + c.length > 0;
    },
    before,
    { polling: 'raf', timeout: 120_000 },
  );
  const ttftMs = performance.now() - t0;
  if (thinkingShot) {
    await page.waitForSelector('.thinking.open .thinking-body', { timeout: 30_000 }).catch(() => {});
    await sleep(500);
    await shot(page, thinkingShot);
  }
  // Complete: that row carries the usage line ("N in / M out") from the final usage chunk.
  await page.waitForFunction(
    (n) => {
      const rows = document.querySelectorAll('.row.assistant');
      const last = rows[rows.length - 1];
      return rows.length > n && /\d+\s*out/.test(last.textContent ?? '');
    },
    before,
    { polling: 'raf', timeout: 180_000 },
  );
  const totalMs = performance.now() - t0;
  const meta = (await page.locator('.row.assistant').last().locator('.meta-text').allTextContents()).join(' ');
  const out = Number(/(\d+)\s*out/.exec(meta)?.[1] ?? 0);
  const tokPerS = out / ((totalMs - ttftMs) / 1000);
  const r = { label, ttftMs: Math.round(ttftMs), totalMs: Math.round(totalMs), out, tokPerS: Math.round(tokPerS * 10) / 10, meta: meta.trim() };
  console.log(`  ${label}: ttft ${r.ttftMs} ms, total ${r.totalMs} ms, ${out} out → ${r.tokPerS} tok/s  (${r.meta})`);
  if (completeShot) await shot(page, completeShot);
  return r;
}

async function connectTunnel(page, invite, base, shotPrefix) {
  await page.goto(base);
  await page.waitForSelector('.connect-card h1');
  if (shotPrefix) await shot(page, `${shotPrefix}-connect`, masked(page));
  await page.locator('.connect textarea').fill(invite);
  const t0 = performance.now();
  await page.getByRole('button', { name: 'Connect' }).click();
  if (shotPrefix) {
    await page.waitForSelector('.steps li.now');
    await sleep(300);
    await shot(page, `${shotPrefix}-connecting`, masked(page));
  }
  await page.waitForSelector('.composer textarea', { timeout: 90_000 });
  const connectMs = Math.round(performance.now() - t0);
  const path = (await page.locator('.path').textContent())?.trim();
  console.log(`  connected in ${connectMs} ms; header says "${path}"`);
  return { connectMs, path };
}

const results = { direct: null, tunnel: null, limits: null };

async function runDirect(browser) {
  console.log('\n== Direct mode (vite dev, VITE_DIRECT_URL=' + DIRECT_URL + ')');
  start('vite', ['node_modules/vite/bin/vite.js', '--port', String(WEB_PORT)], {
    WEB_PORT: String(WEB_PORT),
    VITE_DIRECT_URL: DIRECT_URL,
  });
  const base = `http://127.0.0.1:${WEB_PORT}`;
  await waitFor(base, 'vite dev server');
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 } });
  const page = await ctx.newPage();
  watch(page, 'direct');
  await page.goto(`${base}/?direct&invite=${encodeURIComponent(INVITE_ALICE)}&autoconnect`);
  await page.waitForSelector('.composer textarea', { timeout: 30_000 });
  console.log(`  header says "${(await page.locator('.path').textContent())?.trim()}"`);
  const r = await chat(page, 'direct', PROMPT, { thinkingShot: 'direct-thinking', completeShot: 'direct-complete' });
  results.direct = r;
  await ctx.close();
}

let alicePage = null;
async function runTunnel(browser) {
  console.log('\n== Tunnel mode (built bundle via vite preview, wasm bridge, public relay)');
  start('preview', ['node_modules/vite/bin/vite.js', 'preview', '--port', String(PREVIEW_PORT)], { WEB_PORT: String(WEB_PORT) });
  const base = `http://127.0.0.1:${PREVIEW_PORT}`;
  await waitFor(base, 'vite preview');
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 } });
  const page = await ctx.newPage();
  watch(page, 'tunnel');
  const conn = await connectTunnel(page, INVITE_ALICE, base, 'tunnel');
  await shot(page, 'tunnel-connected');
  const r = await chat(page, 'tunnel', PROMPT, { thinkingShot: 'tunnel-thinking', completeShot: 'tunnel-complete' });
  await sleep(800); // /me refresh after the request → usage bar
  await page.locator('header.topbar').screenshot({ path: join(shots, 'int-tunnel-topbar.png') });
  await page.locator('.path').screenshot({ path: join(shots, 'int-tunnel-status-pill.png') });
  await page.locator('.meters').screenshot({ path: join(shots, 'int-tunnel-usage-bar.png') });
  const meters = (await page.locator('.meters').textContent())?.trim();
  console.log(`  status pill "${(await page.locator('.path').textContent())?.trim()}"; usage "${meters}"`);
  results.tunnel = { ...r, ...conn, meters };
  alicePage = page;
  return base;
}

async function runLimits(browser, base) {
  console.log('\n== Limits: bob (--rpm 2) → 429 countdown → revoke; alice paused → resumed');
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 } });
  const bob = await ctx.newPage();
  watch(bob, 'bob');
  await connectTunnel(bob, INVITE_BOB, base);
  await chat(bob, 'bob #1', 'Say hi in five words.');
  await chat(bob, 'bob #2', 'Say hi in five words.');
  await bob.locator('.composer textarea').fill('Say hi in five words.');
  await bob.getByRole('button', { name: 'Send' }).click();
  await bob.waitForSelector('.banner', { timeout: 30_000 });
  await sleep(1200); // let the countdown tick once
  const banner = (await bob.locator('.banner').textContent())?.replace(/\s+/g, ' ').trim();
  console.log(`  bob #3 → banner: "${banner}"`);
  await shot(bob, '429-countdown');
  if (!/Try again in \d+s/.test(banner ?? '')) problems.push(`bob: no countdown in the banner: ${banner}`);

  keys('revoke', BOB);
  await sleep(1200); // same hot-reload window before the revoke is visible to the gateway
  await bob.getByRole('button', { name: 'Dismiss' }).click();
  await bob.locator('.composer textarea').fill('Still there?');
  await bob.getByRole('button', { name: 'Send' }).click();
  await bob.waitForSelector('.connect .notice', { timeout: 30_000 });
  const notice = (await bob.locator('.connect .notice').textContent())?.trim();
  console.log(`  bob after revoke → connect screen, notice "${notice}"`);
  await shot(bob, 'revoked', masked(bob));
  if (!/revoked/i.test(notice ?? '')) problems.push(`bob: notice after revoke was "${notice}"`);

  const alice = alicePage;
  await chat(alice, 'alice after bob revoked', 'One word: are you still there?', { completeShot: 'alice-after-revoke' });

  keys('pause', 'alice');
  await sleep(1200); // the host re-reads keys.json at most once a second; a paused key works until then
  await alice.locator('.composer textarea').fill('Paused yet?');
  await alice.getByRole('button', { name: 'Send' }).click();
  await alice.waitForSelector('.connect .notice', { timeout: 30_000 });
  const paused = (await alice.locator('.connect .notice').textContent())?.trim();
  console.log(`  alice after pause → connect screen, notice "${paused}"`);
  await shot(alice, 'paused', masked(alice));
  if (!/paused/i.test(paused ?? '')) problems.push(`alice: notice after pause was "${paused}"`);

  keys('resume', 'alice');
  await sleep(1200); // the host re-reads keys.json at most once a second (a human is slower than this)
  await alice.getByRole('button', { name: 'Connect' }).click(); // the remembered invite is prefilled
  const back = await Promise.race([
    alice.waitForSelector('.composer textarea', { timeout: 90_000 }).then(() => 'chat'),
    alice.waitForSelector('.failure', { timeout: 90_000 }).then(() => 'failure'),
  ]);
  if (back !== 'chat') throw new Error(`reconnect after resume: ${await alice.locator('.failure').textContent()}`);
  const r = await chat(alice, 'alice after resume', 'One word: back?', { completeShot: 'resumed' });
  results.limits = { banner, revokedNotice: notice, pausedNotice: paused, resumed: r };
  await ctx.close();
}

// Reconnect in the same page: Disconnect → Connect (remembered invite + persisted identity), then
// once more after "Forget this invite" (fresh identity). Reports what the connect screen says.
async function runReconnect(browser, base) {
  console.log('\n== Reconnect in the same page');
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 } });
  const page = await ctx.newPage();
  watch(page, 'reconnect');
  page.on('console', (m) => /bridge|tunnel|handshake|DERP|wasm/i.test(m.text()) && console.log(`  [console] ${m.text()}`));
  await connectTunnel(page, INVITE_ALICE, base);
  for (const variant of ['same identity', 'fresh identity']) {
    await page.getByRole('button', { name: 'Disconnect' }).click();
    await page.waitForSelector('.connect-card h1');
    if (variant === 'fresh identity') {
      await page.getByRole('button', { name: 'Forget this invite' }).click();
      await page.locator('.connect textarea').fill(INVITE_ALICE);
    }
    const t0 = performance.now();
    await page.getByRole('button', { name: 'Connect' }).click();
    const outcome = await Promise.race([
      page.waitForSelector('.composer textarea', { timeout: 75_000 }).then(() => 'chat'),
      page.waitForSelector('.failure', { timeout: 75_000 }).then(() => 'failure'),
    ]).catch(() => 'timeout');
    const ms = Math.round(performance.now() - t0);
    if (outcome === 'chat') {
      console.log(`  ${variant}: reconnected in ${ms} ms; header "${(await page.locator('.path').textContent())?.trim()}"`);
    } else {
      const steps = await page.locator('.steps li').allTextContents().catch(() => []);
      const failure = await page.locator('.failure').textContent().catch(() => null);
      console.log(`  ${variant}: ${outcome} after ${ms} ms; steps ${JSON.stringify(steps)}; failure ${JSON.stringify(failure?.trim())}`);
      await shot(page, `reconnect-${variant.replace(' ', '-')}-${outcome}`, masked(page));
      problems.push(`reconnect (${variant}): ${outcome}`);
    }
  }
  await ctx.close();
}

async function main() {
  for (const [k, v] of Object.entries({ INVITE_ALICE, INVITE_BOB, INFERCAT_BIN, INFERCAT_DATA_DIR })) {
    if (!v && (only === 'all' || k.startsWith('INVITE') || only === 'limits')) throw new Error(`${k} is not set`);
  }
  mkdirSync(shots, { recursive: true });
  const browser = await chromium.launch();
  if (only === 'direct' || only === 'all') await runDirect(browser);
  let base = null;
  if (only === 'tunnel' || only === 'limits' || only === 'reconnect' || only === 'all') base = await runTunnel(browser);
  if (only === 'limits' || only === 'all') await runLimits(browser, base);
  if (only === 'reconnect') await runReconnect(browser, base);
  await browser.close();
  stopAll();
  console.log('\nRESULTS ' + JSON.stringify(results));
  if (problems.length > 0) {
    console.error('\nProblems:');
    for (const p of problems) console.error(`  ${p}`);
    process.exit(1);
  }
  console.log('PASS');
}

main().catch((err) => {
  console.error(err);
  if (problems.length > 0) console.error('Problems so far:\n  ' + problems.join('\n  '));
  stopAll();
  process.exit(1);
});
