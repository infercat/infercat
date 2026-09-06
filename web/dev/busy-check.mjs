// Ticket 018's real-stack evidence: the real web client (vite dev, Direct mode through the dev
// listener) against a real `infercat serve --slots 1` on the shared llama-server, with a second
// key holding the one slot. Proves the busy host is not called asleep: the pending line says
// "Waiting for a free slot on <host>…" within a second of Send, the reply follows on the same
// response, and — with the slot held past the queue's 30 s — the timeout arrives as "<host> is
// busy" with a countdown, the message kept and the session still connected.
//
//   INFERCAT_BIN=/path/infercat INFERCAT_DATA_DIR=/path/data UPSTREAM=http://127.0.0.1:18080 \
//     GW=http://127.0.0.1:6620 WEB_PORT=6621 node dev/busy-check.mjs [queued|timeout|all]
//
// It only ever kills processes it started itself; the upstream is never touched.
import { spawn, spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, '..');
const shots = join(here, 'evidence'); // real-host evidence: viewed at landing, not tracked (037)
const { INFERCAT_BIN, INFERCAT_DATA_DIR } = process.env;
const GW = process.env.GW ?? 'http://127.0.0.1:6620';
const UPSTREAM = process.env.UPSTREAM ?? 'http://127.0.0.1:18080';
const WEB_PORT = Number(process.env.WEB_PORT ?? 6621);
const LISTEN = GW.replace(/^https?:\/\//, '').replace(/\/.*$/, '');
const HOST_NAME = process.env.HOST_NAME ?? "Max's laptop";
const only = process.argv[2] ?? 'all';
if (!INFERCAT_BIN || !INFERCAT_DATA_DIR) throw new Error('INFERCAT_BIN and INFERCAT_DATA_DIR are required');

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
function start(name, cmd, args, env = {}) {
  const child = spawn(cmd, args, { cwd: web, env: { ...process.env, ...env }, stdio: ['ignore', 'ignore', 'inherit'] });
  kids.push(child);
  return child;
}
function keyAdd(name, ...limits) {
  const r = spawnSync(INFERCAT_BIN, ['keys', 'add', name, '--json', '--no-qr', '--data-dir', INFERCAT_DATA_DIR, ...limits], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`keys add ${name}: ${r.stderr}`);
  return JSON.parse(r.stdout);
}
const events = () => {
  try { return readFileSync(join(INFERCAT_DATA_DIR, 'usage.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse); }
  catch { return []; }
};
const shot = (page, name) => page.screenshot({ path: join(shots, `18-real-${name}.png`) });

/** Takes a place on the host with a long, deterministic generation; resolves once the host has
 *  answered — the first token when the slot was free, its `: queued` when it was not, so a second
 *  holder is known to be in line before anyone sends. `done` resolves when the generation ends.
 *  This engine's context caps one reply near 4 000 tokens (~24 s), so a wait past the queue's 30 s
 *  takes two holders back to back. */
async function hold(secret, tokens) {
  const ac = new globalThis.AbortController();
  const res = await fetch(`${GW}/v1/chat/completions`, {
    method: 'POST',
    headers: { authorization: `Bearer ${secret}`, 'content-type': 'application/json' },
    body: JSON.stringify({
      stream: true, max_tokens: tokens, temperature: 0,
      messages: [{ role: 'user', content: `Count from 1 to ${tokens}, one number per line, nothing else.` }],
    }),
    signal: ac.signal,
  });
  if (!res.ok) throw new Error(`hold: ${res.status} ${await res.text()}`);
  const reader = res.body.getReader();
  await reader.read(); // the first bytes: a token, or the `: queued` that says the place is taken
  const t0 = Date.now();
  const done = (async () => { while (!(await reader.read()).done) { /* keep the stream flowing */ } return (Date.now() - t0) / 1000; })();
  return { done, stop: () => ac.abort() };
}

async function connected(browser, invite) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });
  page.on('pageerror', (e) => problems.push(`pageerror ${e.message}`));
  page.on('console', (m) => m.type() === 'error' && problems.push(`console ${m.text()}`));
  await page.goto(`http://127.0.0.1:${WEB_PORT}/?direct&invite=${encodeURIComponent(invite)}&autoconnect`);
  await page.waitForSelector('.composer textarea', { timeout: 60_000 });
  return page;
}

// --- promise 2, the second silence: in line, and said so ------------------------------------
async function queued(browser, alice, bob) {
  const page = await connected(browser, alice.invite);
  const mark = events().length;
  const holder = await hold(bob.secret, 2500); // ~15 s on this engine: alice waits, then is served
  const t0 = Date.now();
  await page.locator('.composer textarea').fill('Say hi in three words.');
  await page.getByRole('button', { name: 'Send' }).click();
  await page.waitForSelector('.waiting:has-text("Waiting for a free slot")', { timeout: 10_000 });
  const lineS = (Date.now() - t0) / 1000;
  const line = await page.locator('.waiting').innerText();
  console.log(`\n"${line}" at ${lineS.toFixed(1)} s after Send`);
  await sleep(6000); // past the first keepalive and past 014's 5 s "still waiting" notice
  const lineAt6 = await page.locator('.waiting').innerText();
  console.log(`  at ${((Date.now() - t0) / 1000).toFixed(1)} s the line reads: "${lineAt6}"`);
  console.log('  header degraded:', (await page.locator('.degraded').count()) > 0);
  console.log('  path:  ', JSON.stringify(await page.locator('.path').innerText()));
  await shot(page, 'queued');
  if (lineS > 2) problems.push(`the queued line took ${lineS.toFixed(1)} s`);
  if (!lineAt6.includes('free slot')) problems.push(`the line changed while still queued: ${lineAt6}`);
  if (line.includes('didn’t answer') || line.includes('Still waiting')) problems.push(`wrong silence: ${line}`);
  await page.waitForSelector('.meta-text:has-text("out")', { timeout: 120_000 });
  const servedS = (Date.now() - t0) / 1000;
  const heldS = await holder.done;
  console.log(`  served on the same response at ${servedS.toFixed(1)} s (bob held the slot ${heldS.toFixed(1)} s)`);
  console.log('  reply:', JSON.stringify((await page.locator('.row.assistant .md').last().innerText()).slice(0, 80)));
  await shot(page, 'served');
  const mine = events().slice(mark).filter((e) => e.key_id === alice.key_id && e.endpoint === '/v1/chat/completions');
  console.log('  usage.jsonl:', mine.map((e) => `status ${e.status} code ${JSON.stringify(e.code ?? '')} queued_ms ${e.queued_ms} ttft_ms ${e.ttft_ms}`).join(' · '));
  if (!mine.some((e) => e.status === 200 && (e.code ?? '') === '' && e.queued_ms > 1000)) problems.push('alice was not recorded as queued then served');
  if ((await page.locator('.degraded').count()) > 0) problems.push('the header called a busy host degraded');
  await page.close();
}

// --- promise 2, the third silence: the line ran out ------------------------------------------
async function timeout(browser, alice, bob, carol) {
  const page = await connected(browser, alice.invite);
  const mark = events().length;
  const first = await hold(bob.secret, 4000); // bob runs (~24 s) …
  const second = await hold(carol.secret, 4000); // … and carol is in line ahead of alice (~24 s more)
  const holder = { stop: () => { first.stop(); second.stop(); }, done: Promise.all([first.done, second.done]) };
  const t0 = Date.now();
  await page.locator('.composer textarea').fill('Are you free yet?');
  await page.getByRole('button', { name: 'Send' }).click();
  await page.waitForSelector('.waiting:has-text("Waiting for a free slot")', { timeout: 10_000 });
  await page.waitForSelector('.banner', { timeout: 60_000 });
  const failS = (Date.now() - t0) / 1000;
  await sleep(1200); // let the countdown tick once
  const banner = (await page.locator('.banner').innerText()).replace(/\s+/g, ' ').trim();
  const note = await page.locator('.row.assistant .ended').innerText();
  console.log(`\nqueue timeout at ${failS.toFixed(1)} s: banner "${banner}"`);
  console.log(`  under the message: "${note}"`);
  console.log('  pending turn kept:', (await page.locator('.bubble.pending').count()) === 1);
  console.log('  action:', JSON.stringify(await page.locator('.row.assistant .actions button').last().innerText()));
  console.log('  header degraded:', (await page.locator('.degraded').count()) > 0);
  console.log('  path:  ', JSON.stringify(await page.locator('.path').innerText()));
  await shot(page, 'busy');
  if (failS < 28 || failS > 40) problems.push(`the timeout arrived at ${failS.toFixed(1)} s, not ~30`);
  if (!/is busy/.test(banner) || !/Try again in \d+s/.test(banner)) problems.push(`banner: ${banner}`);
  if ((await page.locator('.bubble.pending').count()) !== 1) problems.push('the pending turn was lost');
  if ((await page.locator('.degraded').count()) > 0) problems.push('a busy host was called degraded');
  holder.stop();
  await holder.done.catch(() => 0);
  await sleep(800);
  const mine = events().slice(mark).filter((e) => e.key_id === alice.key_id && e.endpoint === '/v1/chat/completions');
  console.log('  usage.jsonl:', mine.map((e) => `status ${e.status} code ${JSON.stringify(e.code)} queued_ms ${e.queued_ms}`).join(' · '));
  if (!mine.some((e) => e.status === 200 && e.code === 'queue_timeout')) problems.push('the queue timeout was not recorded as a counted row inside the stream');
  await page.close();
}

async function main() {
  start('host', INFERCAT_BIN, ['--data-dir', INFERCAT_DATA_DIR, 'serve', '--upstream', UPSTREAM, '--dev-listen', LISTEN, '--name', HOST_NAME, '--slots', '1']);
  await waitFor(`${GW}/healthz`, 'the host');
  const alice = keyAdd('alice');
  const bob = keyAdd('bob', '--max-output-tokens', '8000');
  const carol = keyAdd('carol', '--max-output-tokens', '8000');
  for (const k of [alice, bob, carol]) k.secret = k.invite.split('.')[2];
  console.log(`keys: alice ${alice.key_id}, bob ${bob.key_id} and carol ${carol.key_id} (the holders)`);
  start('vite', 'node', ['node_modules/vite/bin/vite.js', '--port', String(WEB_PORT), '--strictPort'], { VITE_DIRECT_URL: GW, WEB_PORT: String(WEB_PORT) });
  await waitFor(`http://127.0.0.1:${WEB_PORT}`, 'vite dev');
  await sleep(1500); // keys.json hot reload
  const browser = await chromium.launch();
  if (only === 'queued' || only === 'all') await queued(browser, alice, bob);
  if (only === 'timeout' || only === 'all') await timeout(browser, alice, bob, carol);
  await browser.close();
  kids.forEach((c) => c.kill('SIGTERM'));
  if (problems.length > 0) {
    console.error('\nProblems:');
    for (const p of problems) console.error(`  ${p}`);
    process.exit(1);
  }
  console.log('\nReal stack: a busy host was never called asleep, and no console or page errors.');
}

main().catch((err) => {
  console.error(err);
  kids.forEach((c) => c.kill('SIGTERM'));
  process.exit(1);
});
