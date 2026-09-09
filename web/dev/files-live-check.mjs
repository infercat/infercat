// 076 proof: start our own host/key/data dir against an explicitly supplied local engine.
import { chromium } from 'playwright';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import assert from 'node:assert/strict';
const repo = fileURLToPath(new URL('../../', import.meta.url));
const engine = process.env.INFERCAT_FILE_ENGINE;
if (!engine || !/^http:\/\/127\.0\.0\.1:\d+$/.test(engine)) throw new Error('Set INFERCAT_FILE_ENGINE to your isolated loopback engine');
const base = process.env.FILE_LIVE_URL ?? 'http://127.0.0.1:49176';
const out = '/private/tmp/infercat-076-live'; await mkdir(out, { recursive: true });
const data = await mkdtemp(join(tmpdir(), 'infercat-076-host-'));
const binary = join(repo, 'bin/infercat');
const host = spawn(binary, ['serve', '--data-dir', data, '--upstream', engine, '--slots', '1', '--name', '076 isolated host'], { cwd: repo, stdio: ['ignore', 'pipe', 'pipe'] });
let log = ''; host.stdout.on('data', (chunk) => { log += chunk; }); host.stderr.on('data', (chunk) => { log += chunk; });
function cli(args) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, [...args, '--data-dir', data], { cwd: repo }); let output = '';
    child.stdout.on('data', (chunk) => { output += chunk; }); child.stderr.on('data', (chunk) => { output += chunk; });
    child.on('error', reject); child.on('exit', (code) => resolve({ code, output }));
  });
}
let browser;
try {
  let ready = false;
  for (let i = 0; i < 60; i++) {
    if ((await cli(['status'])).code === 0) { ready = true; break; }
    if (host.exitCode !== null) throw new Error(log);
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  assert.ok(ready, 'own host ready');
  const key = await cli(['keys', 'add', 'file-proof', '--json', '--max-output-tokens', '256']);
  assert.equal(key.code, 0, key.output);
  const { invite } = JSON.parse(key.output); // Never printed or written to evidence.
  browser = await chromium.launch(); const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage(); const errors = []; page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(`${base}/?invite=${encodeURIComponent(invite)}&autoconnect`);
  await page.locator('.composer textarea').waitFor({ timeout: 90_000 });
  await page.waitForFunction(() => !document.querySelector('.composer textarea')?.disabled);
  // This fresh browser contains only our test host. Persist its ordinary supported setting.
  await page.evaluate(() => {
    for (const key of Object.keys(window.localStorage).filter((key) => key.startsWith('bn.settings.'))) {
      window.localStorage.setItem(key, JSON.stringify({ ...JSON.parse(window.localStorage.getItem(key)), thinking: 'off' }));
    }
  });
  await page.reload(); await page.locator('.composer textarea').waitFor({ timeout: 90_000 });
  await page.waitForFunction(() => !document.querySelector('.composer textarea')?.disabled);
  await page.locator('input[type=file]').setInputFiles(['rate-limits.pdf', 'limiter.ts'].map((name) => fileURLToPath(new URL(`./fixtures/files/${name}`, import.meta.url))));
  await page.waitForFunction(() => document.querySelectorAll('.attached .chip').length === 2 && !document.querySelector('.attachment-reading'));
  await page.locator('.composer textarea').fill('Which multiplier does the PDF require, and which named constant does the source file use? Reply in one sentence.');
  await page.locator('.composer .primary').click();
  await page.waitForFunction(() => document.querySelector('.row.assistant .actions button') && document.querySelector('.composer .primary')?.textContent.trim() === 'Send', undefined, { timeout: 180_000 });
  const reply = await page.locator('.row.assistant > .md').innerText();
  assert.match(reply, /two|2/i); assert.match(reply, /BURST_MULTIPLIER/);
  await page.evaluate(() => document.fonts.ready); await page.screenshot({ path: `${out}/pdf-source-reply.png` });
  console.log(`REAL FILE REPLY: ${reply.replaceAll('\n', ' ')}`);
  console.log(`REAL PATH: ${await page.locator('.path').first().innerText()}`);
  await writeFile(`${out}/reply.txt`, reply);
  await page.locator('input[type=file]').setInputFiles(fileURLToPath(new URL('./fixtures/files/scan.pdf', import.meta.url)));
  await page.locator('.hint.image-notice').waitFor(); const hint = await page.locator('.hint.image-notice').innerText();
  assert.match(hint, /Couldn’t find any text in scan.pdf/); assert.equal(await page.locator('.attached .chip').count(), 0);
  await page.screenshot({ path: `${out}/scanned-pdf-hint.png` }); console.log(`REAL SCAN HINT: ${hint}`);
  assert.deepEqual(errors, []); console.log('REAL FILE CHECK: 2 flows passed / 0 failed / 0 skipped');
} finally {
  await browser?.close();
  if (host.exitCode === null) {
    host.kill('SIGTERM');
    await new Promise((resolve) => { const timer = setTimeout(() => host.kill('SIGKILL'), 12_000); host.once('exit', () => { globalThis.clearTimeout(timer); resolve(); }); });
  }
  await rm(data, { recursive: true, force: true });
  console.log('Own host stopped; test key and temporary data removed.');
}
