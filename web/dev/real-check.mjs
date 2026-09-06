// Real-stack evidence for tickets 014, 020 and 022: the built bundle, over the real relay, against
// a real `infercat serve` that this script starts, kills and restarts. The fakes cannot prove
// what this proves — what a send costs the friend's meter, what a host going to sleep looks like,
// that a broken session is replaced rather than retried (014), (020) that a paused invite marks
// exactly one turn, that a host dying mid-reply ends the reply on its own, that the context wall
// says "context", that a revoked invite stays in the thread, that a second tab follows, that the
// phone drawer's delete is undoable, and (022) that a session heals by itself once the host is
// back, that a turn's mark clears when a later send carries it (ZEBRA), that the wall's copy is
// what happens next, that the phone header fits and its Disconnect is reachable and durable, and
// that a revoked invite's card keeps every chat.
//
//   BN_BIN=/path/infercat BN_DATA_DIR=/path/data GW=http://127.0.0.1:6720 PREVIEW_PORT=6721 \
//     UPSTREAM=http://127.0.0.1:18080 node dev/real-check.mjs [cost|asleep|reconnect|heal|race|paused|wall|stall|tabs|phone|revoke|meter|emptydeath|all]
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
const shot22 = (page, name) => shot(page, name, '22-real');
const secs = (since) => ((Date.now() - since) / 1000).toFixed(0);
/** The header's two lines, in one string. */
const header = async (page) => `${await page.locator('.path').innerText()} · ${(await page.locator('.meters').innerText()).replace(/\n/g, ' · ')}`;
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
  // 022 promise 1: the session may already have healed by itself in those 2 s; then there is no
  // Reconnect to press, and the measurement below is of the self-probe.
  const button = page.getByRole('button', { name: 'Reconnect' });
  if (await button.count()) await button.click();
  else console.log('  (healed by itself before Reconnect could be pressed — 022 promise 1)');
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

// --- 022 promise 1: the host went away and came back; the session heals by itself ---------------
async function heal(browser, key, host) {
  const page = await connected(browser, key.invite);
  await ask(page, 'Reply with the single word: pong.');
  await answered(page, 1);
  killHost(host);
  const dead = Date.now();
  await ask(page, 'Are you still there?');
  await page.waitForSelector('.row.assistant .ended', { timeout: 60_000 });
  console.log(`\nHOST KILLED · the send failed at ${secs(dead)} s: ${JSON.stringify(await page.locator('.row.assistant .ended').last().innerText())}`);
  await page.waitForSelector('button:has-text("Reconnect")', { timeout: 20_000 });
  await page.waitForFunction(() => document.querySelector('.path')?.textContent?.includes('not answering'), null, { timeout: 20_000 });
  console.log(`  ${await header(page)} · action: Reconnect`);
  await sleep(Math.max(0, 40_000 - (Date.now() - dead))); // dead for 40 s in all, as the ticket's test says
  host = startHost();
  await waitFor(`${GW}/healthz`, 'the host coming back');
  const back = Date.now();
  console.log(`HOST BACK after ${secs(dead)} s dead — waiting; nothing is clicked`);
  await page.waitForFunction(() => document.querySelector('.path')?.textContent?.includes('relayed via'), null, { timeout: 90_000 });
  const healS = (Date.now() - back) / 1000;
  await sleep(500);
  const action = await page.locator('.row.assistant .actions button').last().innerText();
  console.log(`  healed by itself ${healS.toFixed(0)} s after the host came back · ${await header(page)}`);
  console.log(`  action on the failed exchange: ${JSON.stringify(action)} · marked: ${await marks(page)} · degraded lines: ${await page.locator('.degraded').count()}`);
  check(healS <= 35, `the session took ${healS.toFixed(0)} s to heal after the host came back (promise 1: within one backoff step)`);
  check(action === 'Try again', `the healed exchange still offers ${JSON.stringify(action)}, not Try again`);
  check((await page.locator('.degraded').count()) === 0, 'a degraded line is still up after the heal');
  await shot22(page, 'healed-by-itself');
  await page.locator('.row.assistant .actions button:has-text("Try again")').click();
  await answered(page, 2);
  await sleep(300);
  console.log(`  Try again → delivered over the healed session · marks left: ${await marks(page)}`);
  check((await marks(page)) === 0, 'the turn is still marked after it was delivered');
  await page.context().close();
  return host;
}

// --- 023: Reconnect pressed while the self-probe's dial is in flight joins it ---------------------
async function race(browser, key, host) {
  const page = await connected(browser, key.invite);
  await ask(page, 'Reply with the single word: pong.');
  await answered(page, 1);
  killHost(host);
  const dead = Date.now();
  await ask(page, 'Are you still there?');
  await page.waitForSelector('button:has-text("Reconnect")', { timeout: 60_000 });
  console.log(`\nHOST KILLED · Reconnect offered at ${secs(dead)} s (the self-probe dials 5 s later and retries its handshake for up to 60 s)`);
  await sleep(6_000); // squarely inside that dial
  await page.getByRole('button', { name: 'Reconnect' }).click();
  console.log(`  Reconnect pressed at ${secs(dead)} s, host still dead: ${JSON.stringify(await page.locator('.pitch').innerText().catch(() => '(chat)'))}`);
  host = startHost();
  await waitFor(`${GW}/healthz`, 'the host coming back');
  const back = Date.now();
  console.log(`HOST BACK at ${secs(dead)} s — waiting`);
  // Waits long enough to print the number (a host's own relay re-registration after a kill can
  // take ~30 s, and an in-flight bridge dial has a 60 s bound); the ≤ 30 s check below is the promise.
  await page.waitForFunction(() => document.querySelector('.path')?.textContent?.includes('relayed via'), null, { timeout: 120_000 });
  const s = (Date.now() - back) / 1000;
  await sleep(300);
  console.log(`  connected ${s.toFixed(0)} s after the host came back · ${await header(page)} · action: ${JSON.stringify(await page.locator('.row.assistant .actions button').last().innerText())}`);
  check(s <= 30, `Reconnect pressed inside the probe's dial took ${s.toFixed(0)} s to connect after the host came back (023: ≤ 30 s)`);
  // Confirmation, not the promise: the healed session carries a request. A slow model must not sink
  // the run, so this waits for the last row to reach any terminal state and reports what it was.
  await page.locator('.row.assistant .actions button:has-text("Try again")').click();
  await page.waitForFunction(() => { const rows = document.querySelectorAll('.row.assistant'); const last = rows[rows.length - 1]; return rows.length >= 2 && last && (last.querySelector('.ended') || last.querySelector('.meta-text')?.textContent?.includes('out')); }, null, { timeout: 120_000 }).catch(() => {});
  await sleep(300);
  const delivered = (await marks(page)) === 0;
  console.log(`  Try again over the healed session → marks left: ${await marks(page)} · last row: ${JSON.stringify((await page.locator('.row.assistant .meta-text').last().innerText().catch(() => '')).slice(0, 60))}`);
  check(delivered, 'Try again over the healed session did not deliver the turn');
  await page.context().close();
  return host;
}

// --- 020 promise 1: three answered turns, a pause, a fourth send: exactly one mark ----------------
// --- 022 promise 2: the ZEBRA test — the mark clears when a later send carries the turn ----------
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
  await ask(page, 'Please remember the secret word ZEBRA.');
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
  // 022 promise 2: not Try again — the next question. Its history carries ZEBRA, so the model
  // recalls it, and nothing on that turn may still say it was not delivered.
  await ask(page, 'What was the secret word I asked you to remember? Reply with just the word.');
  await answered(page, 4);
  await sleep(300);
  const after = await marks(page);
  const recalled = (await page.locator('.row.assistant > .md').last().innerText()).toUpperCase().includes('ZEBRA');
  const rows = await page.locator('.row.assistant .meta-text').allInnerTexts();
  const notPart = rows.filter((r) => r.includes('not part of the next question')).length;
  const softened = await page.locator('.ended.carried').allInnerTexts();
  console.log(`  next send → the model recalls ZEBRA: ${recalled} · marks left: ${after} · "not part of the next question" lines: ${notPart} · row under ZEBRA: ${JSON.stringify(softened)}`);
  check(softened.length === 1 && /carried into the next question/.test(softened[0] ?? ''), 'the refused row under ZEBRA still says "Not sent" (024 promise 3)');
  check(recalled, 'the model did not recall ZEBRA (was the turn in the history?)');
  check(after === 0, `${after} marks left after a later send carried the turn (promise 2)`);
  check(notPart === 0, '"not part of the next question" still under a turn the next question carried (promise 2)');
  await shot22(page, 'zebra-recalled');
  await page.context().close();
}

// --- 020 promise 3: the context wall says "context", with a meter --------------------------------
async function wall(browser, key) {
  const page = await connected(browser, key.invite);
  await ask(page, 'Write an exhaustive 5000-word essay on the history of the internet, with many detailed sections. Do not summarise; be as long as you possibly can.');
  await page.waitForSelector('.ended.wall', { timeout: 300_000 });
  await sleep(500);
  const line = await page.locator('.ended.wall').innerText();
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
  check((await page.locator('.ended button:has-text("Continue")').count()) === 0, 'Continue offered at the context wall');
  check(/context/.test(meters), 'no context meter in the header');
  // 022 promise 3: the copy says what happens next, and the composer is not disabled: the next
  // message goes out; the host keeps every visible turn (nothing is trimmed) and shrinks the reply.
  check(/no longer fits/.test(line), `the ending does not say what happens next: ${line}`);
  check(!(await page.locator('.composer textarea').isDisabled()), 'the composer is disabled at the context wall');
  await shot20(page, 'context-wall');
  await shot22(page, 'context-wall');
  await ask(page, 'Reply with the single word: pong.');
  await page.waitForFunction(() => { const rows = document.querySelectorAll('.row.assistant'); const last = rows[rows.length - 1]; return rows.length >= 2 && (last.querySelector('.ended') || last.querySelector('.meta-text')?.textContent?.includes('out')); }, null, { timeout: 180_000 });
  await sleep(500);
  const next = await page.locator('.row.assistant .meta-text').last().innerText();
  const [, nin] = /(\d+) tokens in/.exec(next) ?? [];
  console.log(`  next send after the wall → footer ${JSON.stringify(next)} · meters ${JSON.stringify((await page.locator('.meters').innerText()).replace(/\n/g, ' · '))}`);
  console.log(`  (prompt ${nin} tokens: the whole visible thread went back; the thinking never does)`);
  check(/tokens in/.test(next), `the chat did not go on after the wall: ${next}`);
  await shot22(page, 'after-wall-send');
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

/** No horizontal overflow, and nothing in the header past the right edge (022 promise 4). */
async function fits(page, label) {
  const r = await page.evaluate(() => ({
    over: document.documentElement.scrollWidth - window.innerWidth,
    edge: Math.max(0, ...[...document.querySelectorAll('.topbar, .truth, .meters, .meter, .path')].map((e) => Math.ceil(e.getBoundingClientRect().right - window.innerWidth))),
    labels: [...document.querySelectorAll('.meter-label')].map((e) => e.textContent),
  }));
  console.log(`  ${label}: horizontal overflow ${r.over} px · header past the edge ${r.edge} px · ${r.labels.join(' · ')}`);
  check(r.over <= 0, `${label}: the page scrolls sideways by ${r.over} px`);
  check(r.edge <= 0, `${label}: the header runs ${r.edge} px past the edge`);
}

// --- 024: the context meter is what the next question will carry; an oversized turn is left out ---
async function meter(browser, key) {
  const page = await connected(browser, key.invite);
  const read = async () => (await page.locator('.meter-label').allInnerTexts()).find((t) => t.includes('context')) ?? '(no context meter)';
  const values = [await read()];
  console.log(`\nMETER before anything: ${values[0]}`);
  for (const [n, q] of [[1, 'Reply with the single word: pong.'], [2, 'Reply with the single word: ping.'], [3, 'In one sentence, what is a relay?']]) {
    await ask(page, q);
    await answered(page, n);
    await sleep(400);
    values.push(await read());
    console.log(`  after turn ${n}: ${values[values.length - 1]}`);
  }
  const nums = values.slice(1).map((v) => Number(/^([\d.]+)(k?)\//.exec(v)?.[1] ?? 0) * (/^[\d.]+k\//.test(v) ? 1000 : 1));
  check(nums.every((v, i) => i === 0 || v >= (nums[i - 1] ?? 0)), `the meter went down as the chat grew: ${values.join(' → ')}`);
  await shot(page, 'meter-three-turns', '24-real');
  // A paste that alone does not fit the memory: the host refuses it once, honestly; the next question
  // leaves it out and its reply says so; the meter is what that question carried.
  await ask(page, `Please keep this for later.\n\n${'The quick brown fox jumps over the lazy dog. '.repeat(700)}`);
  await page.waitForSelector('.row.assistant .ended', { timeout: 120_000 });
  const refused = await page.locator('.row.assistant .ended').last().innerText();
  console.log(`  paste (~7.9k tokens) → ${JSON.stringify(refused.slice(0, 90))} · meter ${await read()}`);
  check(/no longer fits|too long/.test(refused), `the paste was not refused: ${refused}`);
  await ask(page, 'What is the capital of France? One word.');
  await answered(page, 4);
  await sleep(400);
  const note = await page.locator('.row.assistant').last().locator('.ended').innerText().catch(() => '');
  const meterAfter = await read();
  console.log(`  next question → ${JSON.stringify(await page.locator('.row.assistant > .md').last().innerText())} · note ${JSON.stringify(note)} · meter ${meterAfter}`);
  check(/left out of this question/.test(note), `the reply does not say the paste was left out: ${JSON.stringify(note)}`);
  check(!/^[5-9]\.\dk|^\d\dk/.test(meterAfter), `the meter still counts the paste: ${meterAfter}`);
  await shot(page, 'meter-left-out', '24-real');
  await page.context().close();
}

// --- 020 promise 7: the phone drawer's delete is undoable ---------------------------------------
// --- 022 promise 4/5: the header fits, the drawer's Disconnect is reachable, and it sticks -------
async function phone(browser, key) {
  const page = await connected(browser, key.invite, { width: 390, height: 844 });
  await ask(page, 'Reply with the single word: pong.');
  await answered(page, 1);
  await sleep(2500); // the post-request /me: the meters carry numbers, the longest labels there are
  console.log('\nPHONE 390 px');
  await fits(page, 'after one answer');
  await shot22(page, 'phone-header');
  await page.getByRole('button', { name: 'Conversations' }).click();
  await sleep(400);
  const foot = await page.evaluate(() => { const b = document.querySelector('.sidebar-foot')?.getBoundingClientRect(); return b ? { y: b.y, bottom: b.bottom, vv: window.visualViewport?.height ?? window.innerHeight } : null; });
  console.log(`  drawer: Disconnect at y=${foot?.y.toFixed(0)}–${foot?.bottom.toFixed(0)} · visual viewport ${foot?.vv}`);
  check(foot !== null && foot.bottom <= foot.vv, 'the drawer\'s Disconnect is below the visible viewport (promise 4)');
  await shot22(page, 'phone-drawer');
  await page.getByRole('button', { name: 'Disconnect' }).click();
  await page.waitForSelector('.connect-card', { timeout: 10_000 });
  await page.reload();
  await sleep(4000); // long enough for an auto-connect to have shown "Connecting to…" if it were going to
  const face = { card: await page.locator('.connect-card').count(), connecting: await page.locator('.pitch:has-text("Connecting")').count(), composer: await page.locator('.composer').count(), pitch: await page.locator('.pitch').innerText().catch(() => '') };
  console.log(`  Disconnect → reload: card ${face.card} · connecting ${face.connecting} · composer ${face.composer} · ${JSON.stringify(face.pitch)}`);
  check(face.card === 1 && face.connecting === 0 && face.composer === 0, 'a reload after Disconnect dialled the host again (promise 5)');
  check(/Welcome back\. Your 1 chat with .* is still on this device\./.test(face.pitch), `the returning card's sentence is ${JSON.stringify(face.pitch)}`);
  await shot22(page, 'phone-after-disconnect-reload');
  await page.getByRole('button', { name: 'Reconnect' }).click();
  await page.waitForSelector('.composer textarea', { timeout: 120_000 });
  check((await page.locator('.row.assistant').count()) === 1, 'the chat did not come back after Reconnect');
  console.log('  Reconnect → the chat is back');
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
  const before = await page.evaluate(() => Object.keys(window.localStorage).filter((k) => k.startsWith('bn.conversations.')).sort());
  await page.getByRole('button', { name: 'Paste a new code' }).click();
  await page.waitForSelector('.connect-card', { timeout: 10_000 });
  const field = await page.locator('.connect textarea').inputValue();
  console.log(`  Paste a new code → fresh card, field ${JSON.stringify(field)}, notice: ${await page.locator('.notice').count()}, welcome back: ${await page.locator('.pitch:has-text("Welcome back")').count()}`);
  check(field === '', 'the new-code card still holds the revoked code');
  check((await page.locator('.notice').count()) === 0, '"Invite from your link is ready" above a revoked code');
  // 022 promise 6: the card destroyed nothing. The chats and the tunnel identity are exactly where
  // they were; only the dead code is gone, so a reload does not dial it again.
  const after = await page.evaluate(() => ({ chats: Object.keys(window.localStorage).filter((k) => k.startsWith('bn.conversations.')).sort(), identity: window.localStorage.getItem('bn.privateKey') !== null, lastHost: window.localStorage.getItem('bn.lastHost') !== null, invite: window.localStorage.getItem('bn.invite') !== null }));
  console.log(`  storage: chat keys before ${before.length} → after ${after.chats.length} · identity kept ${after.identity} · last host kept ${after.lastHost} · dead code kept ${after.invite}`);
  check(before.length > 0 && after.chats.join() === before.join(), 'Paste a new code removed chats (promise 6)');
  check(after.identity && after.lastHost, 'Paste a new code removed the identity or the last host (promise 6)');
  check(!after.invite, 'the revoked code is still remembered: a reload would dial it again');
  await shot20(page, 'revoked-new-code');
  await shot22(page, 'revoked-card-keeps-chats');
  // 024 promise 1: the card says the chats are still here, and a new code from the same host — a
  // re-issue after the revoke — opens the same drawer, because chats are keyed to the host.
  const pitch = await page.locator('.pitch').innerText();
  console.log(`  card: ${JSON.stringify(pitch)}`);
  check(/Your 1 chat with .* is still on this device\./.test(pitch), `the revoked card does not say the chats are still here: ${JSON.stringify(pitch)}`);
  const reissued = mint('bob-again');
  await page.locator('.connect textarea').fill(reissued.invite);
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.waitForSelector('.composer textarea', { timeout: 120_000 });
  await sleep(500);
  const titles = await page.locator('.conv-open').allInnerTexts();
  console.log(`  new code from the same host → drawer: ${JSON.stringify(titles)}`);
  check(titles.some((t) => t.startsWith('Write three short paragraphs')), 'the re-issued code did not open the same drawer (promise 1)');
  await shot(page, 'rekeyed-same-chats', '24-real');
  await page.context().close();
}

// --- 024 promise 4: the host dies while the model is still thinking — the note says so --------------
async function emptydeath(browser, key, host) {
  const page = await connected(browser, key.invite);
  await ask(page, 'Write an exhaustive 3000-word essay on TCP congestion control, section by section.');
  await page.waitForSelector('.thinking', { timeout: 120_000 }); // thinking has begun; no answer text yet
  await sleep(800);
  const answerStarted = (await page.locator('.row.assistant > .md p').count()) > 0;
  killHost(host);
  console.log(`\nHOST KILLED while thinking (answer text on screen: ${answerStarted}); waiting for the reply to end on its own…`);
  await page.waitForSelector('.row.assistant .ended', { timeout: 90_000 });
  const note = await page.locator('.row.assistant .ended').last().innerText();
  const inside = await page.locator('.thinking .ended').count();
  console.log(`  note: ${JSON.stringify(note)} · inside the Thinking block: ${inside}`);
  if (!answerStarted) {
    check(/nothing of the answer had arrived yet, only its thinking/.test(note), `the empty-answer death note is ${JSON.stringify(note)}`);
    check(!/What arrived is above/.test(note), 'the note claims an answer arrived');
    check(inside === 0, 'the note is inside the Thinking block (promise 4: outside)');
  } else console.log('  (the answer had started before the kill: the ordinary stall note applies; rerun to catch the thinking phase)');
  await shot(page, 'empty-death', '24-real');
  await page.context().close();
  return startHost();
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
  if (want('heal')) host = await heal(browser, alice, host);
  if (want('race')) host = await race(browser, alice, host);
  if (want('tabs')) await tabs(browser, alice);
  if (want('phone')) await phone(browser, alice);
  if (want('revoke') && bob) await revoke(browser, bob);
  if (want('meter')) await meter(browser, alice);
  if (want('emptydeath')) host = await emptydeath(browser, alice, host);

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
