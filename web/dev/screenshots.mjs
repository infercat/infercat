// Reusable browser check: boots the fake gateway and the vite dev server, drives the app with
// headless chromium, and writes every screenshot the ticket asks for into dev/screenshots/.
//
//   pnpm screenshots            # all shots
//   WEB_PORT=49180 FAKE_GATEWAY_PORT=49091 pnpm screenshots
//
// Any console error or page error in the browser fails the run, so the shots are evidence that the
// app was clean when they were taken, not just that it rendered.
import { spawn, spawnSync } from 'node:child_process';
import { mkdirSync, readdirSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, '..');
const shots = join(here, 'screenshots');

const WEB_PORT = Number(process.env.WEB_PORT ?? 49173);
const GATEWAY_PORT = Number(process.env.FAKE_GATEWAY_PORT ?? 49090);
const BASE = `http://127.0.0.1:${WEB_PORT}`;

// A well-formed invite for the fake host: bn1.<addr>.<43-char base64url secret>.
const INVITE = `bn1.tcDEMOaddressDEMOaddressDEMOaddressDEMO.${'D'.repeat(43)}`;
const OFFLINE_INVITE = `bn1.tcOFFLINEaddressOFFLINEaddress.${'D'.repeat(43)}`;
const SLOW = 'fake&connectMs=2200&tokenDelay=70';

const children = [];
function start(name, cmd, args, env) {
  const child = spawn(cmd, args, { cwd: web, env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
  child.stdout.on('data', (d) => process.stdout.write(`[${name}] ${d}`));
  child.stderr.on('data', (d) => process.stderr.write(`[${name}] ${d}`));
  children.push(child);
  return child;
}
function stopAll() {
  for (const c of children) c.kill('SIGTERM');
}
process.on('exit', stopAll);
process.on('SIGINT', () => { stopAll(); process.exit(130); });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function waitFor(url, what) {
  for (let i = 0; i < 150; i++) {
    try {
      const r = await fetch(url);
      if (r.ok) return;
    } catch {
      /* not up yet */
    }
    await sleep(200);
  }
  throw new Error(`${what} never came up at ${url}`);
}

const problems = [];
function watch(page, label) {
  page.on('console', (m) => m.type() === 'error' && problems.push(`${label}: console ${m.text()}`));
  page.on('pageerror', (e) => problems.push(`${label}: pageerror ${e.message}`));
}

let n = 0;
async function shot(page, name) {
  return write(page, `${String(++n).padStart(2, '0')}-${name}`);
}

/** Ticket 007's states get their own prefix so the 004 set stays numbered as it was. */
async function shot7(page, name) {
  return write(page, `07-${name}`);
}

async function write(page, base) {
  const file = join(shots, `${base}.png`);
  await page.screenshot({ path: file });
  const kb = Math.round(statSync(file).size / 1024);
  console.log(`  ${file.replace(`${web}/`, '')}  ${kb} KB`);
  if (kb > 500) throw new Error(`${base} is ${kb} KB, over the 500 KB budget`);
}

/** A code in the URL connects by itself (020 promise 8): nothing is clicked. */
async function connectViaTunnel(page, extra = '') {
  await page.goto(`${BASE}/?${SLOW}&invite=${encodeURIComponent(INVITE)}${extra}`);
}
async function shot20(page, name) {
  return write(page, `20-${name}`);
}
async function shot22(page, name) {
  return write(page, `22-${name}`);
}
/** No horizontal overflow, and nothing in the header past the right edge, at this width (022 promise 4). */
async function fits(page, label) {
  const r = await page.evaluate(() => ({
    over: document.documentElement.scrollWidth - window.innerWidth,
    edge: Math.max(0, ...[...document.querySelectorAll('.topbar, .truth, .meters, .meter, .path')].map((e) => Math.ceil(e.getBoundingClientRect().right - window.innerWidth))),
    width: window.innerWidth,
  }));
  if (r.over > 0) problems.push(`${label}: page scrolls horizontally by ${r.over}px at ${r.width}px`);
  if (r.edge > 0) problems.push(`${label}: the header runs ${r.edge}px past the edge at ${r.width}px`);
  console.log(`  ${label}: no horizontal overflow at ${r.width} px (header edge ${r.edge <= 0 ? 'inside' : `${r.edge}px out`})`);
}
/** Every page here shares one host and one browser, so only the newest tab writes (020 promise 6):
 *  a page that is about to start a thread takes the store over the way a reader would. */
async function lead(page) {
  await page.waitForSelector('.composer textarea', { timeout: 20_000 });
  if (await page.locator('.degraded.follower').count()) {
    await page.getByRole('button', { name: 'Use this tab instead' }).click();
    await page.waitForSelector('.degraded.follower', { state: 'detached', timeout: 10_000 });
  }
}

async function main() {
  mkdirSync(shots, { recursive: true });

  start('gateway', process.execPath, ['--experimental-strip-types', 'dev/fake-gateway.ts'], {
    FAKE_GATEWAY_PORT: String(GATEWAY_PORT),
  });
  start('vite', 'node', ['node_modules/vite/bin/vite.js', '--port', String(WEB_PORT)], {
    WEB_PORT: String(WEB_PORT),
    FAKE_GATEWAY_PORT: String(GATEWAY_PORT),
  });
  await waitFor(`http://127.0.0.1:${GATEWAY_PORT}/healthz`, 'fake gateway');
  await waitFor(BASE, 'vite dev server');

  const browser = await chromium.launch();

  // --- desktop, light ---
  const desktop = await browser.newContext({ viewport: { width: 1280, height: 860 }, colorScheme: 'light' });
  const page = await desktop.newPage();
  watch(page, 'desktop');

  await page.goto(BASE);
  await page.waitForSelector('.connect-card h1');
  await shot(page, 'connect');

  await connectViaTunnel(page);
  await page.waitForSelector('.steps li.now');
  await sleep(700);
  await shot(page, 'connecting');

  await page.waitForSelector('.empty', { timeout: 20_000 });
  await shot(page, 'chat-empty');

  await page.locator('.composer textarea').fill('Explain what just happened when I pasted that code.');
  await page.getByRole('button', { name: 'Send' }).click();
  await page.waitForSelector('.thinking.open .thinking-body');
  await sleep(900);
  await shot(page, 'streaming-thinking');

  await page.waitForSelector('.row.assistant .md p', { timeout: 30_000 });
  await sleep(1500);
  await shot(page, 'streaming-answer');

  await page.waitForSelector('.meta-text:has-text("out")', { timeout: 60_000 });
  await shot(page, 'chat-complete');

  await page.locator('.composer textarea').fill('/429 one too many');
  await page.getByRole('button', { name: 'Send' }).click();
  await page.waitForSelector('.banner');
  await shot(page, 'rate-limited');

  await page.getByRole('button', { name: 'Settings' }).click();
  await page.waitForSelector('.sheet');
  await shot(page, 'settings');
  await page.getByRole('button', { name: 'Done' }).click();

  // --- failure state: a host that never answers ---
  const offline = await desktop.newPage();
  watch(offline, 'offline');
  await offline.goto(`${BASE}/?${SLOW}&invite=${encodeURIComponent(OFFLINE_INVITE)}`);
  await offline.waitForSelector('.failure', { timeout: 20_000 });
  await shot(offline, 'host-offline');

  // --- format error, before anything is dialled ---
  // A clean context: this browser has a remembered invite by now, which (014 promise 9) opens onto
  // the welcome-back face with the code masked, and a stranger's first paste is what is being shot.
  const fresh = await browser.newContext({ viewport: { width: 1280, height: 860 }, colorScheme: 'light' });
  const bad = await fresh.newPage();
  watch(bad, 'bad-invite');
  await bad.goto(BASE);
  await bad.locator('.connect textarea').fill('bn9.tcSomething.abc');
  // 014 promise 10: the reason arrives as they paste, and it is what disables Connect.
  await bad.waitForSelector('.inline-error');
  if (await bad.getByRole('button', { name: 'Connect' }).isEnabled()) {
    problems.push('bad invite: Connect is still enabled with a malformed code');
  }
  await shot(bad, 'bad-invite');

  // --- Direct mode: real fetch, real CORS, real SSE against the Node fake gateway ---
  const direct = await desktop.newPage();
  watch(direct, 'direct');
  await direct.goto(`${BASE}/?direct&invite=${encodeURIComponent(INVITE)}&autoconnect`);
  // Same browser context as the shots above, so this one opens onto the remembered history:
  // proof that conversations survive, and a reason to start a clean thread for the shot.
  await lead(direct);
  await direct.locator('.sidebar-head button').click();
  await direct.locator('.composer textarea').fill('Direct mode against the fake gateway.');
  await direct.getByRole('button', { name: 'Send' }).click();
  await direct.waitForSelector('.meta-text:has-text("out")', { timeout: 60_000 });
  await shot(direct, 'direct-mode');

  // --- dark ---
  const dark = await browser.newContext({ viewport: { width: 1280, height: 860 }, colorScheme: 'dark' });
  const darkPage = await dark.newPage();
  watch(darkPage, 'dark');
  await darkPage.goto(BASE);
  await darkPage.waitForSelector('.connect-card h1');
  await shot(darkPage, 'connect-dark');
  await connectViaTunnel(darkPage);
  await darkPage.waitForSelector('.empty', { timeout: 20_000 });
  await darkPage.locator('.composer textarea').fill('Show me a table and a code block.');
  await darkPage.getByRole('button', { name: 'Send' }).click();
  await darkPage.waitForSelector('.meta-text:has-text("out")', { timeout: 60_000 });
  await shot(darkPage, 'chat-dark');

  // --- mobile, 360 px ---
  const mobile = await browser.newContext({
    viewport: { width: 360, height: 780 },
    deviceScaleFactor: 2,
    isMobile: true,
    hasTouch: true,
  });
  const m = await mobile.newPage();
  watch(m, 'mobile');
  await m.goto(BASE);
  await m.waitForSelector('.connect-card h1');
  await shot(m, 'mobile-connect');
  await connectViaTunnel(m);
  await m.waitForSelector('.empty', { timeout: 20_000 });
  await m.locator('.composer textarea').fill('Does this fit on a phone?');
  await m.getByRole('button', { name: 'Send' }).click();
  await m.waitForSelector('.meta-text:has-text("out")', { timeout: 60_000 });
  await shot(m, 'mobile-chat');
  await sleep(1200); // the post-request /me fills the meters: the longest labels there are
  await fits(m, 'mobile: 360 px with three meters'); // 022 promise 4
  await shot22(m, 'phone-360-header');
  await m.getByRole('button', { name: 'Conversations' }).click();
  await sleep(400);
  await shot(m, 'mobile-drawer');

  // No horizontal scrolling at 360 px, ever.
  await fits(m, 'mobile: 360 px with the drawer open');

  // --- ticket 007: the states that used to lie ---------------------------------------------
  const FAST = 'fake&connectMs=200&tokenDelay=25';

  async function connected(query, label) {
    const p = await desktop.newPage();
    watch(p, label);
    await p.goto(`${BASE}/?${FAST}&${query}&invite=${encodeURIComponent(INVITE)}&autoconnect`);
    return p;
  }

  // These pages share the browser context above, so they open onto that host's remembered
  // history — which is itself proof of promise 7. Start a clean thread for the shot.
  async function chatting(query, label) {
    const p = await connected(query, label);
    await lead(p);
    await p.locator('.sidebar-head button').click(); // the sidebar's New chat; a context wall offers one in the thread too
    return p;
  }

  async function ask(p, text) {
    await p.locator('.composer textarea').fill(text);
    await p.getByRole('button', { name: 'Send' }).click();
  }

  // Promise 1: a stream that dies mid-answer keeps its text and says it was cut off.
  const cut = await chatting('', '07-interrupted');
  await ask(cut, '/cut Tell me how the tunnel works.');
  await cut.waitForSelector('.row.assistant .ended', { timeout: 30_000 });
  await shot7(cut, 'interrupted');

  // Promise 2: a reply that was all thinking is not an empty bubble.
  const think = await chatting('', '07-no-answer');
  await ask(think, '/think What is on the other side of this tunnel?');
  await think.waitForSelector('.thinking .ended', { timeout: 30_000 });
  await shot7(think, 'no-answer');

  // Promise 11 + 1: the host's own diagnostic shown *next to* our copy, not instead of it.
  const mid = await chatting('', '07-mid-stream-error');
  await ask(mid, '/mid Start answering and then fall over.');
  await mid.waitForSelector('.row.assistant .ended', { timeout: 30_000 });
  await shot7(mid, 'mid-stream-error');

  // Promise 4: an engine that is not answering says so in the header, not in a settings sheet.
  const sick = await chatting('upstreamDown', '07-engine-offline');
  await shot7(sick, 'engine-offline');

  // Promise 12: the disclosure a host running --log-prompts forces, before the first message.
  const loud = await connected('logPrompts', '07-log-prompts');
  await loud.waitForSelector('.failure', { timeout: 20_000 });
  await shot7(loud, 'log-prompts-disclosure');
  await loud.getByRole('button', { name: 'I understand — start chatting' }).click();
  await loud.waitForSelector('.logging', { timeout: 20_000 });
  await shot7(loud, 'log-prompts-chat');

  // Promise 4: the path pill after a ping fails — the last good number, dated, never presented as
  // current. This one waits out a real 30 s measurement tick; that is the thing being shown.
  const stale = await chatting('pingFailsAfter=1', '07-degraded-path');
  await stale.waitForSelector('.path', { timeout: 20_000 });
  await stale.waitForFunction(
    () => document.querySelector('.path')?.textContent?.includes('not answering'),
    { timeout: 45_000 },
  );
  await shot7(stale, 'degraded-path');

  // Promise 14: the invite arrives as a link fragment. A clean context, so the only thing that
  // could have filled the field is the fragment itself.
  const linked = await browser.newContext({ viewport: { width: 1280, height: 860 }, colorScheme: 'light' });
  const link = await linked.newPage();
  watch(link, '07-invite-link');
  await link.goto(`${BASE}/?${SLOW}#${INVITE}`);
  // 020 promise 8: the link is the consent — it connects by itself, with a Cancel.
  await link.waitForSelector('.pitch:has-text("Connecting")', { timeout: 10_000 });
  const leftInBar = await link.evaluate(() => location.hash);
  if (leftInBar !== '') problems.push(`invite link: the secret is still in the address bar (${leftInBar})`);
  await shot20(link, 'link-connecting');
  await link.getByRole('button', { name: 'Cancel' }).click();
  await link.waitForSelector('.notice', { timeout: 10_000 });
  // Cancelled: the card, with the code masked — the secret is never shown unless asked for.
  const shownLink = await link.locator('.masked code').innerText();
  if (shownLink.includes(INVITE.split('.')[2])) problems.push(`invite link: the secret is on screen (${shownLink})`);
  if (await link.locator('.connect textarea').count()) problems.push('invite link: the code is in a text box unmasked');
  await shot7(link, 'invite-link');

  // Promise 5 + 15: a revoked invite mid-chat returns to Connect saying so, in one register.
  const revoked = await browser.newContext({ viewport: { width: 1280, height: 860 }, colorScheme: 'light' });
  const rev = await revoked.newPage();
  watch(rev, '07-revoked');
  await rev.goto(`${BASE}/?${FAST}&invite=${encodeURIComponent(INVITE)}&autoconnect`);
  await rev.waitForSelector('.composer textarea', { timeout: 20_000 });
  await ask(rev, 'A chat worth keeping.');
  await rev.waitForSelector('.meta-text:has-text("out")', { timeout: 60_000 });
  const kept = await rev.evaluate(() => Object.keys(window.localStorage).filter((k) => k.startsWith('bn.conversations.')).sort());
  await rev.locator('.composer textarea').fill('/403 revoke me');
  await rev.getByRole('button', { name: 'Send' }).click();
  // 020 promise 5: like pause — the thread stays, the header says, the composer is off.
  await rev.waitForSelector('.degraded.key', { timeout: 20_000 });
  if (await rev.locator('.connect-card').count()) problems.push('revoked: ejected to the connect screen');
  if (!(await rev.locator('.composer textarea').isDisabled())) problems.push('revoked: the composer is still enabled');
  if (!(await rev.getByRole('button', { name: 'Paste a new code' }).count())) problems.push('revoked: no "Paste a new code"');
  await shot20(rev, 'revoked-in-thread');
  await rev.getByRole('button', { name: 'Paste a new code' }).click();
  await rev.waitForSelector('.connect-card', { timeout: 10_000 });
  if ((await rev.locator('.connect textarea').inputValue()) !== '') problems.push('revoked: the new-code card holds the old code');
  if (await rev.locator('.notice').count()) problems.push('revoked: "Invite from your link is ready" above a revoked code');
  // 022 promise 6: the card destroyed nothing — the chats and the identity are where they were.
  const still = await rev.evaluate(() => ({ chats: Object.keys(window.localStorage).filter((k) => k.startsWith('bn.conversations.')).sort(), identity: window.localStorage.getItem('bn.privateKey') !== null, invite: window.localStorage.getItem('bn.invite') !== null }));
  if (kept.length === 0 || still.chats.join() !== kept.join()) problems.push(`revoked: Paste a new code changed the chats on disk (${kept.length} → ${still.chats.length})`);
  if (!still.identity) problems.push('revoked: Paste a new code removed the tunnel identity');
  if (still.invite) problems.push('revoked: the dead code is still remembered');
  await shot20(rev, 'revoked-new-code');
  await shot22(rev, 'revoked-card-keeps-chats');


  // --- ticket 014: the two failures a friend will hit, and the honest meters ------------------
  async function shot14(page, name) {
    return write(page, `14-${name}`);
  }

  // Promise 1 (blocker). The host went to sleep after we connected. This shot waits out the real
  // 5 s and the real ~15 s: the timings are the thing being shown, so they are not shortened.
  const gone = await chatting('hostAsleep', '14-host-asleep');
  await ask(gone, 'Are you still there?');
  await gone.waitForSelector('.waiting:has-text("Still waiting")', { timeout: 20_000 });
  await shot14(gone, 'host-asleep-waiting');
  await gone.waitForSelector('.row.assistant .ended', { timeout: 40_000 });
  const asleepCopy = await gone.locator('.row.assistant .ended').innerText();
  for (const raw of ['context deadline', 'dial port', 'closed inside the response']) {
    if (asleepCopy.includes(raw)) problems.push(`host-asleep: raw transport string in primary copy (${raw})`);
  }
  if (!asleepCopy.includes('asleep or offline')) problems.push(`host-asleep: copy is ${asleepCopy}`);
  // The reader's words are still in the thread, marked, with something to press. For this failure
  // the useful thing is a new connection, not another 30 s over the dead one (014 promise 13
  // supersedes promise 1's "Try again" label for exactly this case).
  await gone.waitForSelector('.bubble.pending', { timeout: 10_000 });
  await gone.waitForSelector('button:has-text("Reconnect")', { timeout: 10_000 });
  await shot14(gone, 'host-asleep-failed');

  // Promise 2 (blocker). A paused invite keeps the chat, keeps the words, and says who to ask.
  const paused = await chatting('keyPaused', '14-paused');
  if (await paused.locator('.degraded.key').count()) problems.push('paused: degraded before the pause');
  await ask(paused, 'Is my invite still good?');
  await paused.waitForSelector('.degraded.key', { timeout: 20_000 });
  await sleep(300);
  // 020 promise 1: the words stay in the thread as the one pending turn, and only that one.
  const pausedMarks = await paused.locator('.bubble.pending').count();
  if (pausedMarks !== 1) problems.push(`paused: ${pausedMarks} bubbles marked, not exactly 1`);
  if (!(await paused.locator('.composer textarea').isDisabled())) problems.push('paused: the composer is still enabled');
  if (await paused.locator('.connect-card').count()) problems.push('paused: ejected to the connect screen');
  await shot14(paused, 'paused-banner');

  // Promise 3. A /me that stopped answering renders "—", never a zero nobody measured.
  const unknown = await chatting('meFailsAfter=1', '14-unknown');
  await ask(unknown, 'Count something for me.');
  await unknown.waitForSelector('.meter-label:has-text("—")', { timeout: 60_000 });
  const meterText = await unknown.locator('.meters').innerText();
  if (/\b0\b/.test(meterText.split('\n')[0])) problems.push(`unknown meters: ${meterText}`);
  await shot14(unknown, 'unknown-meters');

  // Promise 10. Said once: four words under the message, the explanation and countdown in one toast.
  const fast429 = await chatting('', '14-rate-limit');
  await ask(fast429, '/429 one too many');
  await fast429.waitForSelector('.banner', { timeout: 20_000 });
  const inline = await fast429.locator('.row.assistant .ended').innerText();
  if (inline !== 'Too fast — not sent.') problems.push(`rate limit: inline copy is ${JSON.stringify(inline)}`);
  const toast = await fast429.locator('.banner').innerText();
  if (!/Try again in \d+s/.test(toast)) problems.push(`rate limit: no countdown in the toast (${toast})`);
  await shot14(fast429, 'rate-limit-once');

  // Promise 8. Deleting is six seconds of Undo, not a dialog.
  const del = await chatting('', '14-delete');
  await ask(del, 'A chat worth deleting.');
  await del.waitForSelector('.meta-text:has-text("out")', { timeout: 60_000 });
  await del.locator('.conv.current .conv-del').click();
  await del.waitForSelector('.toast:has-text("Undo")', { timeout: 10_000 });
  await shot14(del, 'delete-undo');

  // Promise 9. A returning reader, in the same browser, with their chats still here and their code
  // masked. No invite in the URL: the only thing that can fill the field is what was remembered.
  const back = await desktop.newPage();
  watch(back, '14-welcome-back');
  await back.goto(`${BASE}/?${SLOW}`);
  // 020 promise 8: the return visit is the consent; the face below is what Cancel shows.
  await back.waitForSelector('.pitch:has-text("Connecting to")', { timeout: 10_000 });
  await back.getByRole('button', { name: 'Cancel' }).click();
  await back.waitForSelector('.masked code', { timeout: 10_000 });
  const shown = await back.locator('.masked code').innerText();
  if (shown.includes(INVITE.split('.')[2])) problems.push(`welcome back: the secret is on screen (${shown})`);
  if (!(await back.locator('.pitch:has-text("Welcome back")').count())) problems.push('welcome back: no welcome');
  if (!(await back.getByRole('button', { name: 'Reconnect' }).count())) problems.push('welcome back: no Reconnect');
  await shot14(back, 'welcome-back');

  // Promise 6. A touch keyboard: Return makes a line, Send sends, and the hint does not lie.
  const phone = await browser.newContext({
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 2,
    isMobile: true,
    hasTouch: true,
  });
  const ph = await phone.newPage();
  watch(ph, '14-touch');
  await ph.goto(`${BASE}/?${FAST}&invite=${encodeURIComponent(INVITE)}&autoconnect`);
  await ph.waitForSelector('.composer textarea', { timeout: 20_000 });
  await ph.locator('.composer textarea').fill('One line');
  await ph.locator('.composer textarea').press('Enter');
  const afterEnter = await ph.locator('.composer textarea').inputValue();
  if (afterEnter !== 'One line\n') problems.push(`touch: Enter did not make a newline (${JSON.stringify(afterEnter)})`);
  if (await ph.locator('.hint:has-text("Enter sends")').count()) problems.push('touch: the "Enter sends" hint is shown');
  const del44 = await ph.evaluate(() => {
    const el = document.querySelector('.conv-del');
    const r = el?.getBoundingClientRect();
    return r ? Math.min(r.width, r.height) : 0;
  });
  if (del44 > 0 && del44 < 44) problems.push(`touch: the delete target is ${del44}px, under 44`);
  await shot14(ph, 'touch-composer');
  const phoneOverflow = await ph.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  if (phoneOverflow > 0) problems.push(`touch: page scrolls horizontally by ${phoneOverflow}px at 390px`);


  // 022 promise 1. The dead session is replaced by itself: the self-probe dials afresh 5 s after
  // the failure (the fake's next session starts awake, as a restarted host does), the header flips
  // to live values and the failed exchange offers Try again — nothing is clicked. Promise 13's
  // Reconnect is the same move by hand, for a reader who does not want to wait the step.
  await lead(gone); // other tabs have taken the store since; the action is the leader's
  await gone.waitForFunction(() => document.querySelector('.path')?.textContent?.includes('relayed via'), null, { timeout: 30_000 });
  await sleep(300);
  if (await gone.getByRole('button', { name: 'Reconnect' }).count()) problems.push('healed: Reconnect still offered after the session healed');
  if (!(await gone.getByRole('button', { name: 'Try again' }).count())) problems.push('healed: no Try again on the failed exchange');
  // The two limit meters carry numbers again; the context meter reads "—" until a reply reports usage, which none has.
  const healedMeters = await gone.locator('.meter-label').allInnerTexts();
  if (healedMeters.slice(0, 2).some((m) => m.startsWith('—'))) problems.push(`healed: the meters still read ${JSON.stringify(healedMeters)}`);
  if (await gone.locator('.connect-card .failure').count()) problems.push('reconnect: ended on the connect screen');
  await shot22(gone, 'healed-by-itself');
  await shot14(gone, 'reconnected');

  // Promise 14 (blocker). A reply that stopped at the invite's cap says so, and offers the only
  // thing that helps.
  const capped = await chatting('', '14-reply-cap');
  await ask(capped, '/cap Write me something long.');
  await capped.waitForSelector('.ended.capped', { timeout: 30_000 });
  const capCopy = await capped.locator('.ended.capped').innerText();
  if (!/token reply limit/.test(capCopy)) problems.push(`reply cap: copy is ${capCopy}`);
  if (!(await capped.getByRole('button', { name: 'Continue' }).count())) problems.push('reply cap: no Continue');
  await shot14(capped, 'reply-cap');

  // 020 promise 3. The other wall: the sum filled the model, the output did not reach the cap.
  const walled = await chatting('', '20-context-wall');
  await ask(walled, '/wall Write me something very long.');
  await walled.waitForSelector('.ended.wall', { timeout: 30_000 });
  const wallCopy = await walled.locator('.ended.wall').innerText();
  if (!/filled the .* memory on/.test(wallCopy)) problems.push(`context wall: copy is ${wallCopy}`);
  // 022 promise 3: the copy says what happens next, and nothing is disabled.
  if (!/no longer fits/.test(wallCopy)) problems.push(`context wall: the copy does not say what happens next (${wallCopy})`);
  if (await walled.locator('.ended button:has-text("Continue")').count()) problems.push('context wall: Continue offered');
  if (!(await walled.locator('.ended.wall button:has-text("New chat")').count())) problems.push('context wall: no New chat');
  if (await walled.locator('.composer textarea').isDisabled()) problems.push('context wall: the composer is disabled');
  if (!/8\.2k\/8\.2k context/.test(await walled.locator('.meters').innerText())) problems.push(`context wall: meter reads ${await walled.locator('.meters').innerText()}`);
  await shot20(walled, 'context-wall');
  await shot22(walled, 'context-wall');

  // 020 promise 6. A second tab of the same browser follows the first, and can take over.
  const tabA = await chatting('', '20-tab-a');
  await ask(tabA, 'First tab speaking.');
  await tabA.waitForSelector('.meta-text:has-text("out")', { timeout: 60_000 });
  const tabB = await connected('', '20-tab-b');
  await tabB.waitForSelector('.degraded.follower', { timeout: 20_000 });
  if (!(await tabB.locator('.composer textarea').isDisabled())) problems.push('follower: the composer is enabled');
  if (await tabA.locator('.degraded.follower').count()) problems.push('leader: shows the follower banner');
  await shot20(tabB, 'follower-tab');
  await tabB.getByRole('button', { name: 'Use this tab instead' }).click();
  await tabA.waitForSelector('.degraded.follower', { timeout: 10_000 });
  await tabB.waitForSelector('.degraded.follower', { state: 'detached', timeout: 10_000 });
  await shot20(tabA, 'tab-taken-over');

  // 020 promise 7. Deleting from the phone drawer is the same six seconds of Undo as the desktop.
  await ask(ph, 'A chat worth deleting, on a phone.');
  await ph.waitForSelector('.meta-text:has-text("out")', { timeout: 60_000 });
  await ph.getByRole('button', { name: 'Conversations' }).click();
  await sleep(400);
  await ph.locator('.conv.current .conv-del').click();
  await ph.waitForSelector('.toast:has-text("Undo")', { timeout: 5_000 });
  const toastBox = await ph.locator('.toast').boundingBox();
  if (!toastBox || toastBox.y + toastBox.height > 844) problems.push('phone drawer: the undo toast is off screen');
  await shot20(ph, 'phone-drawer-undo');
  await ph.getByRole('button', { name: 'Undo' }).click();
  await sleep(300);

  // 022 promise 4. The header fits at 390 px with three counters showing numbers, and the drawer's
  // Disconnect is inside the visible viewport, not under the browser bar. The drawer is still open
  // from the delete above (its backdrop would swallow a tap on the hamburger): close it first.
  await ph.locator('.backdrop').click({ position: { x: 370, y: 500 } });
  await sleep(400);
  await fits(ph, 'phone: 390 px with three meters');
  await shot22(ph, 'phone-390-header');
  await ph.getByRole('button', { name: 'Conversations' }).click();
  await sleep(400);
  const foot = await ph.evaluate(() => { const b = document.querySelector('.sidebar-foot')?.getBoundingClientRect(); return b ? { bottom: b.bottom, vv: window.visualViewport?.height ?? window.innerHeight } : null; });
  if (!foot || foot.bottom > foot.vv) problems.push(`phone drawer: Disconnect at ${foot?.bottom}px, below the ${foot?.vv}px viewport`);
  await shot22(ph, 'phone-drawer-disconnect');
  // 022 promise 5. Disconnect outlives a reload: the card, with the returning sentence, and no dial.
  await ph.getByRole('button', { name: 'Disconnect' }).click();
  await ph.waitForSelector('.connect-card', { timeout: 10_000 });
  await ph.reload();
  await sleep(2500);
  if (await ph.locator('.pitch:has-text("Connecting")').count()) problems.push('disconnect: a reload dialled the host again');
  if (await ph.locator('.composer').count()) problems.push('disconnect: a reload landed in the chat');
  const returning = await ph.locator('.pitch').innerText().catch(() => '');
  if (!/Welcome back\. Your \d+ chats? with .* (is|are) still on this device\./.test(returning)) problems.push(`disconnect: the returning sentence is ${JSON.stringify(returning)}`);
  await shot22(ph, 'phone-after-disconnect-reload');
  await ph.getByRole('button', { name: 'Reconnect' }).click();
  await ph.waitForSelector('.composer textarea', { timeout: 20_000 });

  // Promise 16. Editing replaces the answer — and says so — but does not throw the old one away.
  const edited = await chatting('', '14-previous-answer');
  await ask(edited, 'First question, which becomes the title.');
  await edited.waitForSelector('.meta-text:has-text("out")', { timeout: 60_000 });
  await edited.getByRole('button', { name: 'Edit' }).click();
  await edited.locator('.bubble.editing textarea').fill('Second question, which retitles the chat.');
  if (!(await edited.getByRole('button', { name: 'Replace answer' }).count())) {
    problems.push('edit: the button does not say what it does');
  }
  await edited.getByRole('button', { name: 'Replace answer' }).click();
  await edited.waitForSelector('.meta-text:has-text("out")', { timeout: 60_000 });
  await edited.waitForSelector('.previous summary', { timeout: 10_000 });
  const title = await edited.locator('.conv.current .conv-open').innerText();
  if (!title.startsWith('Second question')) problems.push(`edit: the sidebar title is stale (${title})`);
  await edited.locator('.previous summary').click();
  await sleep(200);
  await shot14(edited, 'previous-answer');

  // Promise 17. Reasoning is model output: it renders as markdown, not as raw asterisks.
  const marked = await chatting('', '14-reasoning-markdown');
  await ask(marked, '/think Show me your working.');
  await marked.waitForSelector('.thinking .ended', { timeout: 30_000 });
  // The block collapses when the reply ends; open it, which is what a curious reader does.
  await marked.locator('.thinking-toggle').click();
  await marked.waitForSelector('.thinking.open .thinking-body .md strong', { timeout: 10_000 });
  const rawStars = await marked.locator('.thinking-body').innerText();
  if (rawStars.includes('**')) problems.push('reasoning: raw asterisks on screen');
  await shot14(marked, 'reasoning-markdown');

  // --- the built bundle, served statically, with every request accounted for ---
  // PROD=0 skips it: dev/real-check.mjs serves dist/ while it runs, and a rebuild under it is a race.
  if (process.env.PROD === '0') {
    await browser.close();
    stopAll();
    console.log(`\n${readdirSync(shots).filter((f) => f.endsWith('.png')).length} screenshots in dev/screenshots/ (production section skipped)`);
    if (problems.length > 0) {
      console.error('\nProblems:');
      for (const p of problems) console.error(`  ${p}`);
      process.exit(1);
    }
    return;
  }
  const previewPort = WEB_PORT + 1;
  spawnSync('node', ['node_modules/vite/bin/vite.js', 'build'], { cwd: web, stdio: 'inherit' });
  start('preview', 'node', ['node_modules/vite/bin/vite.js', 'preview', '--port', String(previewPort)], {
    WEB_PORT: String(WEB_PORT),
    FAKE_GATEWAY_PORT: String(GATEWAY_PORT),
  });
  const previewBase = `http://127.0.0.1:${previewPort}`;
  await waitFor(previewBase, 'vite preview');
  const prod = await browser.newContext({ viewport: { width: 1280, height: 860 } });
  const prodPage = await prod.newPage();
  watch(prodPage, 'production');
  const offOrigin = [];
  prodPage.on('request', (r) => {
    if (!r.url().startsWith(previewBase) && !r.url().startsWith('data:')) offOrigin.push(r.url());
  });
  await prodPage.goto(previewBase);
  await prodPage.waitForSelector('.connect-card h1');
  await shot(prodPage, 'production-landing');
  // Promise 7: the landing page fetches nothing off its own origin, and no wasm until Connect.
  if (offOrigin.length > 0) problems.push(`production: off-origin requests ${offOrigin.join(', ')}`);
  const wasmRequests = await prodPage.evaluate(() =>
    performance.getEntriesByType('resource').filter((e) => e.name.includes('wasm')).map((e) => e.name),
  );
  if (wasmRequests.length > 0) problems.push(`production: wasm fetched before Connect: ${wasmRequests}`);
  if (!(await prodPage.title())) problems.push('production: the page has no <title>');

  await browser.close();
  stopAll();

  console.log(`\n${readdirSync(shots).filter((f) => f.endsWith('.png')).length} screenshots in dev/screenshots/`);
  if (problems.length > 0) {
    console.error('\nProblems:');
    for (const p of problems) console.error(`  ${p}`);
    process.exit(1);
  }
  console.log('No console errors, no page errors, no horizontal overflow at 360 px or 390 px.');
}

main().catch((err) => {
  console.error(err);
  stopAll();
  process.exit(1);
});
