// Real-stack evidence for tickets 031 and 032: the built bundle, over the real relay, against a real
// `infercat serve --slots 1` on the shared llama-server, started and stopped by this script.
// What it shows: (031) the Thinking row says "model default" before the model has thought, becomes a
// switch after, and a reply asked not to think has no Thinking block, fewer tokens and says so;
// (032) every reply's footer carries ttft and tok/s measured from the device, a reply that waited for
// a slot says so as a floor, the limits sheet shows the chat's medians, and all of it survives a
// reload. Each reply's device numbers are printed beside the host's own usage line for the same
// request, so the relay hop can be seen rather than guessed at.
//
//   INFERCAT_BIN=/path/infercat INFERCAT_DATA_DIR=/path/data GW=http://127.0.0.1:6840 PREVIEW_PORT=6841 \
//     UPSTREAM=http://127.0.0.1:18080 node dev/footer-check.mjs
//
// It only ever kills processes it started itself; the upstream only receives requests.
import { spawn, spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, '..');
const shots = join(here, 'evidence'); // real-host evidence: viewed at landing, not tracked (037)
const { INFERCAT_BIN, INFERCAT_DATA_DIR } = process.env;
const GW = process.env.GW ?? 'http://127.0.0.1:6840';
const UPSTREAM = process.env.UPSTREAM ?? 'http://127.0.0.1:18080';
const LISTEN = GW.replace(/^https?:\/\//, '').replace(/\/.*$/, '');
const PREVIEW = Number(process.env.PREVIEW_PORT ?? 6841);
const HOST_NAME = process.env.HOST_NAME ?? "Max's laptop";
if (!INFERCAT_BIN || !INFERCAT_DATA_DIR) throw new Error('INFERCAT_BIN and INFERCAT_DATA_DIR are required');

const kids = [];
process.on('exit', () => kids.forEach((c) => c.kill('SIGTERM')));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); return ok; };

async function waitFor(url, what, tries = 300) {
  for (let i = 0; i < tries; i++) {
    try { if ((await fetch(url)).ok) return; } catch { /* not up yet */ }
    await sleep(200);
  }
  throw new Error(`${what} never came up at ${url}`);
}
function keys(...args) {
  const r = spawnSync(INFERCAT_BIN, ['keys', ...args, '--data-dir', INFERCAT_DATA_DIR], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`keys ${args.join(' ')}: ${r.stderr || r.stdout}`);
  return r.stdout;
}
const RUN = Date.now().toString(36).slice(-4);
/** The secret is the invite's third segment (docs/ARCHITECTURE.md, invite format); the CLI never prints it twice. */
const mint = (name) => { const k = JSON.parse(keys('add', `${name}-${RUN}`, '--json', '--no-qr')); k.secret = k.invite.split('.')[2]; return k; };
const events = () => {
  try { return readFileSync(join(INFERCAT_DATA_DIR, 'usage.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse); }
  catch { return []; }
};
/** The host's own line for the newest chat request of this key: the other side of every footer. */
const hostLine = (keyId) => events().filter((e) => e.key_id === keyId && e.endpoint === '/v1/chat/completions').at(-1);
const describeHost = (e) => e
  ? `host usage.jsonl: ttft_ms ${e.ttft_ms} · queued_ms ${e.queued_ms} · total_ms ${e.total_ms} · completion_tokens ${e.completion_tokens} → ${(e.completion_tokens / ((e.total_ms - e.ttft_ms) / 1000)).toFixed(0)} tok/s over the host's own ttft→end`
  : 'host usage.jsonl: (no line yet)';

/** Takes the one slot with a long, deterministic generation; resolves once the host has answered. */
async function hold(secret, tokens) {
  const ac = new globalThis.AbortController();
  const res = await fetch(`${GW}/v1/chat/completions`, {
    method: 'POST',
    headers: { authorization: `Bearer ${secret}`, 'content-type': 'application/json' },
    body: JSON.stringify({ stream: true, max_tokens: tokens, temperature: 0, chat_template_kwargs: { enable_thinking: false },
      messages: [{ role: 'user', content: `Count from 1 to ${tokens}, one number per line, nothing else.` }] }),
    signal: ac.signal,
  });
  if (!res.ok) throw new Error(`hold: ${res.status} ${await res.text()}`);
  const reader = res.body.getReader();
  await reader.read();
  const done = (async () => { while (!(await reader.read()).done) { /* keep it flowing */ } })();
  return { done, stop: () => ac.abort() };
}

const shot = (page, name) => page.screenshot({ path: join(shots, `${name}.png`) });
const lastRow = (page) => page.locator('.row.assistant').last();
const footer = async (page) => (await lastRow(page).locator('.meta-text').innerText()).replace(/\s+/g, ' ').trim();
const speedTitle = (page) => lastRow(page).locator('.speed').getAttribute('title');
async function ask(page, text) {
  await page.locator('.composer textarea').fill(text);
  await page.getByRole('button', { name: 'Send' }).click();
}
/** The footer is complete once tok/s has landed: that needs the usage chunk, i.e. the host's [DONE]. */
const answered = (page, n) => page.waitForFunction((n) => {
  const rows = document.querySelectorAll('.row.assistant .speed');
  return rows.length >= n && /tok\/s/.test(rows[rows.length - 1].textContent ?? '');
}, n, { timeout: 180_000 });
const Q = 'What is the capital of France? Answer in one short sentence.';

async function main() {
  spawnSync('node', ['node_modules/vite/bin/vite.js', 'build'], { cwd: web, stdio: 'inherit' });
  kids.push(spawn('node', ['node_modules/vite/bin/vite.js', 'preview', '--port', String(PREVIEW), '--strictPort'], { cwd: web, stdio: 'ignore' }));
  kids.push(spawn(INFERCAT_BIN, ['--data-dir', INFERCAT_DATA_DIR, 'serve', '--upstream', UPSTREAM, '--dev-listen', LISTEN, '--slots', '1', '--name', HOST_NAME], { stdio: 'ignore' }));
  await waitFor(`http://127.0.0.1:${PREVIEW}`, 'vite preview');
  await waitFor(`${GW}/healthz`, 'the host');
  const alice = mint('alice');
  const bob = mint('bob');

  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });
  page.on('pageerror', (e) => problems.push(`pageerror ${e.message}`));
  page.on('console', (m) => m.type() === 'error' && problems.push(`console ${m.text()}`));
  const t0 = Date.now();
  await page.goto(`http://127.0.0.1:${PREVIEW}/#${encodeURIComponent(alice.invite)}`);
  await page.waitForSelector('.composer textarea', { timeout: 120_000 });
  console.log(`connected by link in ${((Date.now() - t0) / 1000).toFixed(1)} s · path: ${await page.locator('.path').innerText()}`);

  // 031 promise 2, before the model has thought here: no switch, only what is in force.
  await page.getByRole('button', { name: 'Settings' }).click();
  const label0 = await page.locator('.field-label:has-text("Thinking")').innerText();
  console.log(`\nSETTINGS before any reply → "${label0}" · select present: ${await page.locator('.field:has(.field-label:has-text("Thinking")) select').count()}`);
  check(label0.includes('model default'), '031: the Thinking row should read "model default" before the model has thought');
  await shot(page, '31-settings-default');
  await page.getByRole('button', { name: 'Cancel' }).click();

  // 032 promise 1: the footer, from the device, beside the host's line for the same request.
  await ask(page, Q);
  await answered(page, 1);
  await sleep(1500);
  console.log(`\nREPLY 1 (model default) footer: "${await footer(page)}"`);
  console.log(`  hover: ${await speedTitle(page)}`);
  console.log(`  ${describeHost(hostLine(alice.key_id))}`);
  console.log(`  thinking blocks in the row: ${await lastRow(page).locator('.thinking').count()}`);
  check((await footer(page)).includes('ttft'), '032: the footer has no ttft');
  check((await footer(page)).includes('tok/s'), '032: the footer has no tok/s');
  await shot(page, '32-real-footer');

  // 031 promises 1–2: the switch, once the model has thought; off → no block, fewer tokens, said so.
  await page.getByRole('button', { name: 'Settings' }).click();
  const sel = page.locator('.field:has(.field-label:has-text("Thinking")) select');
  console.log(`\nSETTINGS after a thinking reply → select present: ${await sel.count()} · options: ${(await sel.locator('option').allInnerTexts()).join(' | ')}`);
  check((await sel.count()) === 1, '031: the switch did not appear after the model thought');
  await shot(page, '31-settings-thinking');
  await sel.selectOption('off');
  await page.getByRole('button', { name: 'Done' }).click();
  const before = hostLine(alice.key_id);
  await ask(page, Q);
  await answered(page, 2);
  await sleep(1500);
  const f2 = await footer(page);
  const after = hostLine(alice.key_id);
  console.log(`\nREPLY 2 (thinking off) footer: "${f2}"`);
  console.log(`  ${describeHost(after)}`);
  console.log(`  thinking blocks in the row: ${await lastRow(page).locator('.thinking').count()} · completion tokens ${before?.completion_tokens} → ${after?.completion_tokens}`);
  check((await lastRow(page).locator('.thinking').count()) === 0, '031: a Thinking block appeared with thinking off');
  check(f2.includes('thinking off'), '031: the footer does not say thinking off');
  check(f2.includes('fewer tokens'), '031: the footer does not say the tokens saved');
  check(after && before && after.completion_tokens < before.completion_tokens, '031: the host did not record a shorter completion');
  await shot(page, '31-real-thinking-off');

  // 032 promise 2: in line for a slot, and the footer says so as the floor it is.
  const holder = await hold(bob.secret, 2500);
  const tq = Date.now();
  await ask(page, 'Name three cities in France, one per line.');
  await page.waitForSelector('.waiting:has-text("free slot")', { timeout: 10_000 });
  console.log(`\nQUEUED: "${await page.locator('.waiting').innerText()}" at ${((Date.now() - tq) / 1000).toFixed(1)} s`);
  await answered(page, 3);
  await sleep(1500);
  const f3 = await footer(page);
  console.log(`  footer: "${f3}"`);
  console.log(`  hover: ${await speedTitle(page)}`);
  console.log(`  ${describeHost(hostLine(alice.key_id))}`);
  check(f3.includes('in line for a slot'), '032: a queued reply did not say it waited for a slot');
  await shot(page, '32-real-queued');
  holder.stop();

  // 032 promise 3: the chat's medians in the limits sheet.
  await page.locator('.meters').click();
  await page.waitForSelector('.sheet', { timeout: 5000 });
  const sheet = (await page.locator('.sheet').innerText()).replace(/\s+/g, ' ');
  const pace = sheet.match(/[^.]*to the first token[^.]*\.[^.]*\.[^.]*\.[^.]*\./)?.[0] ?? '(no speed paragraph)';
  console.log(`\nSHEET: ${pace}`);
  check(sheet.includes('to the first token') && sheet.includes('tok/s'), '032: the sheet has no medians');
  await shot(page, '32-sheet-medians');
  await page.getByRole('button', { name: 'Got it' }).click();

  // 032 promise 3: survives a reload with the chat.
  const beforeReload = await page.locator('.row.assistant .speed').allInnerTexts();
  await page.reload();
  await page.waitForSelector('.composer textarea', { timeout: 120_000 });
  await page.waitForFunction((n) => document.querySelectorAll('.row.assistant .speed').length >= n, beforeReload.length, { timeout: 20_000 });
  const afterReload = await page.locator('.row.assistant .speed').allInnerTexts();
  console.log(`\nRELOAD: footers before ${JSON.stringify(beforeReload)}\n        after  ${JSON.stringify(afterReload)}`);
  check(JSON.stringify(beforeReload) === JSON.stringify(afterReload), '032: the footers changed across a reload');
  await shot(page, '32-real-after-reload');

  await browser.close();
  console.log(problems.length ? `\nPROBLEMS:\n- ${problems.join('\n- ')}` : '\nevery promise above held, and no console or page errors');
  process.exitCode = problems.length ? 1 : 0;
}

main().catch((e) => { console.error(e); process.exitCode = 1; }).finally(() => kids.forEach((c) => c.kill('SIGTERM')));
