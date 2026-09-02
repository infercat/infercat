// Real-stack evidence for tickets 014 and 020: the built bundle, over the real relay, against a
// real `bunny-network serve` that this script starts, kills and restarts. The fakes cannot prove
// what this proves — what a send costs the friend's meter, what a host going to sleep looks like,
// that a broken session is replaced rather than retried (014), and (020) that a paused invite marks
// exactly one turn, that a host dying mid-reply ends the reply on its own, that the context wall
// says "context", that a revoked invite stays in the thread, that a second tab follows, and that
// the phone drawer's delete is undoable.
//
//   BN_BIN=/path/bunny-network BN_DATA_DIR=/path/data GW=http://127.0.0.1:6670 PREVIEW_PORT=6671 \
//     UPSTREAM=http://127.0.0.1:18080 node dev/real-check.mjs [cost|asleep|reconnect|paused|wall|stall|tabs|phone|revoke|all]
//
// Keys are minted in BN_DATA_DIR (alice for everything, bob for the revoke) unless INVITE is set.
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
if (!BN_BIN || !BN_DATA_DIR) throw new Error('BN_BIN and BN_DATA_DIR are required');

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
/** The host's own CLI, on the host's own data dir: what the host does to a key is done here. */
function keys(...args) {
  const r = spawnSync(BN_BIN, ['keys', ...args, '--data-dir', BN_DATA_DIR], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`keys ${args.join(' ')}: ${r.stderr || r.stdout}`);
  return r.stdout;
}
/** Run-unique names: a data dir that has seen a run before holds a paused alice and a revoked bob. */
const RUN = Date.now().toString(36).slice(-4);
const mint = (name) => JSON.parse(keys('add', `${name}-${RUN}`, '--json', '--no-qr'));
const events = () => {
  try { return readFileSync(join(BN_DATA_DIR, 'usage.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse); }
  catch { return []; }
};
const since = (n) => events().slice(n).map((e) => `${e.endpoint} ${e.status}`);
async function usage(secret) {
  const r = await fetch(`${GW}/me`, { headers: { authorization: `Bearer ${secret}` } });
  return (await r.json()).usage;
}
function watch(page) {
  page.on('pageerror', (e) => problems.push(`pageerror ${e.message}`));
  page.on('console', (m) => m.type() === 'error' && problems.push(`console ${m.text()}`));
}
/** A fresh friend: a new browser context (its own storage, its own Web Locks), the invite by link. */
async function connected(browser, invite, viewport = { width: 1280, height: 860 }) {
  const ctx = await browser.newContext({ viewport, ...(viewport.width < 500 ? { isMobile: true, hasTouch: true, deviceScaleFactor: 2 } : {}) });
  const page = await ctx.newPage();
  watch(page);
  const t0 = Date.now();
  await page.goto(`http://127.0.0.1:${PREVIEW}/#${encodeURIComponent(invite)}`);
  // 020 promise 8: the link is the consent; nothing is clicked.
  await page.waitForSelector('.composer textarea', { timeout: 120_000 });
  console.log(`  connected by link in ${((Date.now() - t0) / 1000).toFixed(1)} s, nothing clicked`);
  await sleep(300);
  const state = await page.evaluate(() => ({
    follower: document.querySelectorAll('.degraded.follower').length,
    degraded: [...document.querySelectorAll('.degraded')].map((e) => e.textContent?.trim()),
    disabled: document.querySelector('.composer textarea')?.disabled,
  }));
  if (state.follower || state.degraded.length || state.disabled) console.log('  state:', JSON.stringify(state));
  return page;
}
const shot = (page, name, prefix = '14-real') => page.screenshot({ path: join(shots, `${prefix}-${name}.png`) });
const shot20 = (page, name) => shot(page, name, '20-real');
async function ask(page, text) {
  await page.locator('.composer textarea').fill(text);
  await page.getByRole('button', { name: 'Send' }).click();
}
const answered = (page, n) => page.waitForFunction((n) => document.querySelectorAll('.row.assistant .meta-text').length >= n && [...document.querySelectorAll('.row.assistant .meta-text')].slice(-1)[0]?.textContent?.includes('out'), n, { timeout: 180_000 });
const marks = (page) => page.locator('.bubble.pending').count();

// --- what one message costs the friend (014 promise 5) ------------------------------------------
async function cost(browser, key) {
  let mark = events().length;
  const page = await connected(browser, key.invite);
  await sleep(1500);
  console.log('\nCONNECT →', since(mark).join(' · ') || '(nothing)');
  console.log('  rpm_used after connect:', (await usage(key.secret)).rpm_used);
  console.log('  meters:', JSON.stringify(await page.locator('.meters').innerText()));
  console.log('  path:  ', JSON.stringify(await page.locator('.path').innerText()));
  console.log('  model: ', JSON.stringify(await page.locator('.who .dim').innerText()));
  check(!since(mark).some((e) => e.startsWith('/v1/models')), 'connect asked for the model list even though /me carried it (promise 5)');
  for (const n of [1, 2]) {
    mark = events().length;
    const before = await usage(key.secret);
    await ask(page, `Say hi in three words. (${n})`);
    await answered(page, n);
    await sleep(2500);
    const after = await usage(key.secret);
    const spent = after.rpm_used - before.rpm_used;
    console.log(`SEND #${n} →`, since(mark).join(' · '), `| rpm_used ${before.rpm_used} → ${after.rpm_used}`);
    console.log('  meters:', JSON.stringify(await page.locator('.meters').innerText()));
    check(spent === 1, `one message cost ${spent} requests, not 1 (promise 5)`);
  }
  await shot(page, 'stack');
  await page.context().close();
}

// --- a host that goes to sleep mid-send (014 promise 1; 020 promise 4 for the header) ------------
async function asleep(browser, key, host) {
  const page = await connected(browser, key.invite);
  killHost(host);
  await sleep(2000);
  const t0 = Date.now();
  await ask(page, 'Are you still there?');
  await page.waitForSelector('.waiting:has-text("Still waiting")', { timeout: 30_000 });
  const noticeS = (Date.now() - t0) / 1000;
  console.log(`\n"${await page.locator('.waiting').innerText()}" at ${noticeS.toFixed(0)} s`);
  await shot(page, 'asleep-waiting');
  await page.waitForSelector('.row.assistant .ended', { timeout: 60_000 });
  const failS = (Date.now() - t0) / 1000;
  const copy = await page.locator('.row.assistant .ended').innerText();
  console.log(`failed at ${failS.toFixed(0)} s: ${JSON.stringify(copy)}`);
  check(noticeS <= 9, `the "still waiting" line took ${noticeS.toFixed(0)} s`);
  check(failS <= 22, `the failure took ${failS.toFixed(0)} s, not ~15`);
  for (const raw of ['context deadline', 'dial port', 'closed inside the response']) {
    check(!copy.includes(raw), `raw transport string in primary copy: ${raw}`);
  }
  await page.waitForSelector('.meter-label:has-text("—")', { timeout: 40_000 });
  console.log('  pending turn kept:', (await marks(page)) === 1);
  console.log('  action:', JSON.stringify(await page.locator('.row.assistant .actions button').last().innerText()));
  // 020 promise 4: a request that got no answer says nothing about the engine. The pill says the
  // host is not answering; there is no second cause above a Reconnect.
  const engineLines = await page.locator('.degraded.engine, .degraded.both').count();
  console.log('  engine banner:', engineLines === 0 ? 'none (one health source)' : 'PRESENT');
  check(engineLines === 0, 'a failed request asserted the engine is down (promise 4)');
  console.log('  path:  ', JSON.stringify(await page.locator('.path').innerText()));
  console.log('  meters:', JSON.stringify(await page.locator('.meters').innerText()));
  check(!(await page.locator('.path').innerText()).includes('relayed via'), 'the path pill still shows a live latency for a host that is not answering');
  await shot(page, 'asleep-failed');
  return page;
}

// --- and the reconnect once it comes back (014 promise 13) ----------------------------------------
async function reconnect(page) {
  const host = startHost();
  await waitFor(`${GW}/healthz`, 'the host coming back');
  await sleep(2000);
  const t0 = Date.now();
  await page.getByRole('button', { name: 'Reconnect' }).click();
  await page.waitForSelector('.composer textarea', { timeout: 120_000 });
  await page.waitForFunction(() => !document.querySelector('.path')?.textContent?.includes('not answering'), { timeout: 60_000 });
  const s = (Date.now() - t0) / 1000;
  console.log(`\nreconnected in ${s.toFixed(1)} s — path ${JSON.stringify(await page.locator('.path').innerText())}`);
  check(s <= 20, `the reconnect took ${s.toFixed(0)} s`);
  await ask(page, 'Say hi in three words.');
  await page.waitForSelector('.meta-text:has-text("out")', { timeout: 120_000 });
  console.log('  and the thread works again.');
  await shot(page, 'reconnected');
  await page.context().close();
  return host;
}

// --- 020 promise 1: three answered turns, a pause, a fourth send: exactly one mark ----------------
async function paused(browser, key) {
  const page = await connected(browser, key.invite);
  for (let n = 1; n <= 3; n++) {
    await ask(page, `Reply with the single word: pong. (${n})`);
    await answered(page, n);
  }
  // Let turn 3's own /me land first (it says active). The pause then falls between polls — the
  // case the reviewer hit: the app learns of it from the send that fails, not from a poll. (When
  // a poll learns first, the composer is simply off with the banner and no turn is marked.)
  await sleep(1500);
  keys('pause', key.name);
  await sleep(1500); // the host re-reads keys.json at most once a second
  await ask(page, 'Are you still with me?');
  await page.waitForSelector('.degraded.key', { timeout: 30_000 });
  await sleep(500);
  const marked = await marks(page);
  const banner = await page.locator('.degraded.key').innerText();
  console.log(`\nPAUSED after three answered turns → banner ${JSON.stringify(banner)}`);
  console.log(`  bubbles marked "Not delivered": ${marked} · composer disabled: ${await page.locator('.composer textarea').isDisabled()}`);
  check(marked === 1, `${marked} bubbles marked after a pause, not exactly 1 (promise 1)`);
  check(await page.locator('.composer textarea').isDisabled(), 'the composer is enabled while paused (promise 5)');
  check((await page.locator('.row.assistant .actions button:has-text("Try again")').count()) === 0, 'Try again offered while paused');
  await shot20(page, 'paused-one-mark');
  keys('resume', key.name);
  const t0 = Date.now();
  // The 30 s /me poll clears the banner by itself (promise 4); the mark clears when the turn is delivered.
  await page.waitForSelector('.degraded.key', { state: 'detached', timeout: 60_000 });
  console.log(`  resumed: banner cleared by itself ${((Date.now() - t0) / 1000).toFixed(0)} s after resume`);
  // Reload: what is persisted still marks exactly that turn, and nothing under an answer.
  await page.reload();
  await page.waitForSelector('.composer textarea', { timeout: 120_000 });
  const afterReload = await marks(page);
  console.log(`  after reload: ${afterReload} marked`);
  check(afterReload === 1, `${afterReload} bubbles marked after reload, not exactly 1 (promise 1)`);
  await page.locator('.row.assistant .actions button:has-text("Try again")').click();
  await answered(page, 4);
  await sleep(300);
  const after = await marks(page);
  console.log(`  Try again → delivered · marks left: ${after}`);
  check(after === 0, `${after} marks left after the turn was delivered (promise 1)`);
  await shot20(page, 'paused-resumed');
  await page.context().close();
}

// --- 020 promise 3: the context wall says "context", with a meter --------------------------------
async function wall(browser, key) {
  const page = await connected(browser, key.invite);
  await ask(page, 'Write an exhaustive 5000-word essay on the history of the internet, with many detailed sections. Do not summarise; be as long as you possibly can.');
  await page.waitForSelector('.ended.capped', { timeout: 300_000 });
  await sleep(500);
  const line = await page.locator('.ended.capped').innerText();
  const meta = await page.locator('.row.assistant .meta-text').last().innerText();
  const meters = await page.locator('.meters').innerText();
  console.log(`\nLONG ANSWER ended: ${JSON.stringify(line)}`);
  console.log(`  footer: ${JSON.stringify(meta)}`);
  console.log(`  meters: ${JSON.stringify(meters)}`);
  const [, tin, tout] = /(\d+) tokens in · (\d+) out/.exec(meta) ?? [];
  console.log(`  arithmetic: ${tin} + ${tout} = ${Number(tin) + Number(tout)}`);
  check(/filled the .* memory on/.test(line), `the ending does not say the context filled: ${line}`);
  check(!line.includes('reply limit'), 'the ending still blames the reply limit (promise 3)');
  check((await page.getByRole('button', { name: 'New chat' }).count()) >= 2, 'no New chat button on the context wall');
  check((await page.locator('.ended.capped button:has-text("Continue")').count()) === 0, 'Continue offered at the context wall');
  check(/context/.test(meters), 'no context meter in the header');
  await shot20(page, 'context-wall');
  await page.context().close();
}

// --- 020 promise 2: a host that dies mid-reply; the reply ends on its own ------------------------
async function stall(browser, key, host) {
  const page = await connected(browser, key.invite);
  await ask(page, 'Write an exhaustive 3000-word essay on TCP congestion control, section by section.');
  await page.waitForSelector('.row.assistant > .md p', { timeout: 120_000 }); // the answer, not the thinking
  await sleep(1500);
  killHost(host);
  const t0 = Date.now();
  console.log('\nHOST KILLED mid-reply; waiting for the reply to end on its own…');
  await page.waitForSelector('.row.assistant .ended', { timeout: 90_000 });
  const endS = (Date.now() - t0) / 1000;
  const copy = await page.locator('.row.assistant .ended').innerText();
  console.log(`  ended at ${endS.toFixed(0)} s: ${JSON.stringify(copy)}`);
  check(copy.includes('stopped answering mid-reply'), `the ending is ${JSON.stringify(copy)}`);
  check(!copy.includes('You stopped'), 'the app blamed the reader for the stop (promise 2)');
  check(endS <= 40, `the reply took ${endS.toFixed(0)} s to end, not ~25`);
  await page.waitForSelector('.composer button:has-text("Send")', { timeout: 10_000 });
  console.log('  composer back: Send');
  await page.waitForSelector('.meter-label:has-text("—")', { timeout: 40_000 });
  console.log('  meters:', JSON.stringify(await page.locator('.meters').innerText()));
  console.log('  action:', JSON.stringify(await page.locator('.row.assistant .actions button').last().innerText()));
  const engineLines = await page.locator('.degraded.engine, .degraded.both').count();
  check(engineLines === 0, 'a stalled request asserted the engine is down (promise 4)');
  check((await page.locator('.row.assistant > .md').last().innerText()).length > 50, 'the partial answer is gone');
  await shot20(page, 'stalled');
  return page;
}

// --- 020 promise 6: a second tab follows, and can take over --------------------------------------
async function tabs(browser, key) {
  const a = await connected(browser, key.invite);
  await ask(a, 'Reply with the single word: pong.');
  await answered(a, 1);
  const b = await a.context().newPage();
  watch(b);
  await b.goto(`http://127.0.0.1:${PREVIEW}/#${encodeURIComponent(key.invite)}`);
  await b.waitForSelector('.degraded.follower', { timeout: 120_000 });
  console.log(`\nTAB B: ${JSON.stringify(await b.locator('.degraded.follower').innerText())}`);
  console.log(`  B composer disabled: ${await b.locator('.composer textarea').isDisabled()} · A follower banner: ${await a.locator('.degraded.follower').count()}`);
  check(await b.locator('.composer textarea').isDisabled(), 'the follower can send');
  check((await a.locator('.degraded.follower').count()) === 0, 'the leader shows the follower banner');
  // A sends; B reads it live over the storage channel.
  await ask(a, 'Reply with the single word: ping.');
  await answered(a, 2);
  await b.waitForFunction(() => document.querySelectorAll('.row.assistant').length >= 2, null, { timeout: 30_000 });
  await sleep(2500);
  const metersA = await a.locator('.meters').innerText();
  const metersB = await b.locator('.meters').innerText();
  console.log(`  B saw A's exchange · meters A ${JSON.stringify(metersA)} · B ${JSON.stringify(metersB)}`);
  check(metersA === metersB, 'the two tabs disagree about the meters (promise 6)');
  await shot20(b, 'follower-tab');
  await b.getByRole('button', { name: 'Use this tab instead' }).click();
  await a.waitForSelector('.degraded.follower', { timeout: 10_000 });
  await b.waitForSelector('.degraded.follower', { state: 'detached', timeout: 10_000 });
  console.log(`  after "Use this tab instead": A follows, B leads · B composer disabled: ${await b.locator('.composer textarea').isDisabled()}`);
  await shot20(a, 'tab-taken-over');
  await a.context().close();
}

// --- 020 promise 7: the phone drawer's delete is undoable ---------------------------------------
async function phone(browser, key) {
  const page = await connected(browser, key.invite, { width: 390, height: 844 });
  await ask(page, 'Reply with the single word: pong.');
  await answered(page, 1);
  await page.getByRole('button', { name: 'Conversations' }).click();
  await sleep(400);
  await page.locator('.conv.current .conv-del').click();
  await page.waitForSelector('.toast:has-text("Undo")', { timeout: 5_000 });
  const box = await page.locator('.toast').boundingBox();
  const drawerOpen = await page.locator('.drawer-open').count();
  console.log(`\nPHONE drawer delete → toast at y=${box?.y.toFixed(0)} h=${box?.height.toFixed(0)} (viewport 844) · drawer still open: ${drawerOpen === 1}`);
  check(box !== null && box.y + box.height <= 844 && box.y >= 0, 'the undo toast is not on screen');
  await shot20(page, 'phone-drawer-undo');
  await page.getByRole('button', { name: 'Undo' }).click();
  await sleep(300);
  check((await page.locator('.row.assistant').count()) === 1, 'Undo did not bring the chat back');
  console.log('  Undo → chat back');
  await page.context().close();
}

// --- 020 promise 5: revoke stays in the thread --------------------------------------------------
async function revoke(browser, key) {
  const page = await connected(browser, key.invite);
  await ask(page, 'Write three short paragraphs about lighthouses.');
  await page.waitForSelector('.row.assistant .md p', { timeout: 120_000 });
  keys('revoke', key.name, '--yes');
  await page.waitForSelector('.degraded.key', { timeout: 90_000 });
  await sleep(500);
  const banner = await page.locator('.degraded.key').innerText();
  console.log(`\nREVOKED mid-chat → banner ${JSON.stringify(banner)}`);
  console.log(`  still in the thread: ${await page.locator('.connect-card').count() === 0} · composer disabled: ${await page.locator('.composer textarea').isDisabled()} · answer on screen: ${(await page.locator('.row.assistant > .md').last().innerText()).length} chars`);
  check(banner.includes('revoked'), 'the banner does not say revoked');
  check((await page.locator('.connect-card').count()) === 0, 'revoke ejected to the connect screen (promise 5)');
  check(await page.locator('.composer textarea').isDisabled(), 'the composer is enabled after a revoke');
  await shot20(page, 'revoked-in-thread');
  await page.getByRole('button', { name: 'Paste a new code' }).click();
  await page.waitForSelector('.connect-card', { timeout: 10_000 });
  const field = await page.locator('.connect textarea').inputValue();
  console.log(`  Paste a new code → fresh card, field ${JSON.stringify(field)}, notice: ${await page.locator('.notice').count()}, welcome back: ${await page.locator('.pitch:has-text("Welcome back")').count()}`);
  check(field === '', 'the new-code card still holds the revoked code');
  check((await page.locator('.notice').count()) === 0, '"Invite from your link is ready" above a revoked code');
  await shot20(page, 'revoked-new-code');
  await page.context().close();
}

async function main() {
  spawnSync('node', ['node_modules/vite/bin/vite.js', 'build'], { cwd: web, stdio: 'inherit' });
  const prev = spawn('node', ['node_modules/vite/bin/vite.js', 'preview', '--port', String(PREVIEW), '--strictPort'],
    { cwd: web, stdio: ['ignore', 'ignore', 'inherit'] });
  kids.push(prev);
  await waitFor(`http://127.0.0.1:${PREVIEW}`, 'vite preview');

  let host = startHost();
  await waitFor(`${GW}/healthz`, 'the host');
  const alice = INVITE ? { name: 'alice', invite: INVITE, secret: INVITE.split('.')[2] } : { ...mint('alice'), secret: undefined };
  alice.secret = alice.invite.split('.')[2];
  const bob = INVITE ? null : { ...mint('bob') };
  await sleep(1500);
  const browser = await chromium.launch();
  const want = (s) => only === s || only === 'all';

  if (want('cost')) await cost(browser, alice);
  if (want('paused')) await paused(browser, alice);
  if (want('wall')) await wall(browser, alice);
  if (want('stall')) {
    const page = await stall(browser, alice, host);
    host = await reconnect(page);
  }
  if (want('asleep') || only === 'reconnect') {
    const page = await asleep(browser, alice, host);
    if (want('reconnect')) host = await reconnect(page);
  }
  if (want('tabs')) await tabs(browser, alice);
  if (want('phone')) await phone(browser, alice);
  if (want('revoke') && bob) await revoke(browser, bob);

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
