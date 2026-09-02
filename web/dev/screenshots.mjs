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

async function connectViaTunnel(page, extra = '') {
  await page.goto(`${BASE}/?${SLOW}&invite=${encodeURIComponent(INVITE)}${extra}`);
  await page.getByRole('button', { name: 'Connect' }).click();
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
  await offline.getByRole('button', { name: 'Connect' }).click();
  await offline.waitForSelector('.failure', { timeout: 20_000 });
  await shot(offline, 'host-offline');

  // --- format error, before anything is dialled ---
  const bad = await desktop.newPage();
  watch(bad, 'bad-invite');
  await bad.goto(BASE);
  await bad.locator('.connect textarea').fill('bn9.tcSomething.abc');
  await bad.getByRole('button', { name: 'Connect' }).click();
  await bad.waitForSelector('.inline-error');
  await shot(bad, 'bad-invite');

  // --- Direct mode: real fetch, real CORS, real SSE against the Node fake gateway ---
  const direct = await desktop.newPage();
  watch(direct, 'direct');
  await direct.goto(`${BASE}/?direct&invite=${encodeURIComponent(INVITE)}&autoconnect`);
  // Same browser context as the shots above, so this one opens onto the remembered history:
  // proof that conversations survive, and a reason to start a clean thread for the shot.
  await direct.waitForSelector('.composer textarea', { timeout: 20_000 });
  await direct.getByRole('button', { name: 'New chat' }).click();
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
  await m.getByRole('button', { name: 'Conversations' }).click();
  await sleep(400);
  await shot(m, 'mobile-drawer');

  // No horizontal scrolling at 360 px, ever.
  const overflow = await m.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  if (overflow > 0) problems.push(`mobile: page scrolls horizontally by ${overflow}px at 360px`);

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
    await p.waitForSelector('.composer textarea', { timeout: 20_000 });
    await p.getByRole('button', { name: 'New chat' }).click();
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
    () => document.querySelector('.path')?.textContent?.includes('path unknown'),
    { timeout: 45_000 },
  );
  await shot7(stale, 'degraded-path');

  // Promise 14: the invite arrives as a link fragment. A clean context, so the only thing that
  // could have filled the field is the fragment itself.
  const linked = await browser.newContext({ viewport: { width: 1280, height: 860 }, colorScheme: 'light' });
  const link = await linked.newPage();
  watch(link, '07-invite-link');
  await link.goto(`${BASE}/?${FAST}#${INVITE}`);
  await link.waitForSelector('.connect-card h1');
  const filled = await link.locator('.connect textarea').inputValue();
  if (filled !== INVITE) problems.push(`invite link: field holds ${JSON.stringify(filled)}`);
  const leftInBar = await link.evaluate(() => location.hash);
  if (leftInBar !== '') problems.push(`invite link: the secret is still in the address bar (${leftInBar})`);
  await shot7(link, 'invite-link');

  // Promise 5 + 15: a revoked invite mid-chat returns to Connect saying so, in one register.
  const revoked = await browser.newContext({ viewport: { width: 1280, height: 860 }, colorScheme: 'light' });
  const rev = await revoked.newPage();
  watch(rev, '07-revoked');
  await rev.goto(`${BASE}/?${FAST}&invite=${encodeURIComponent(INVITE)}&autoconnect`);
  await rev.waitForSelector('.composer textarea', { timeout: 20_000 });
  await rev.locator('.composer textarea').fill('/403 revoke me');
  await rev.getByRole('button', { name: 'Send' }).click();
  await rev.waitForSelector('.failure', { timeout: 20_000 });
  await shot7(rev, 'revoked-return');

  // --- the built bundle, served statically, with every request accounted for ---
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
  console.log('No console errors, no page errors, no horizontal overflow at 360 px.');
}

main().catch((err) => {
  console.error(err);
  stopAll();
  process.exit(1);
});
