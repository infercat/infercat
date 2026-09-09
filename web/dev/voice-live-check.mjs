// Real browser MediaRecorder → own host/ASR → editable text → chat → own host/Kokoro → Audio.
// Supplies a reproducible spoken WAV to Chromium's microphone device; no media APIs are mocked.
// VOICE_INVITE_FILE and VOICE_INPUT_WAV are required. VOICE_DIRECT=1 opts into loopback HTTP.
import assert from 'node:assert/strict';
import { readFileSync, mkdirSync } from 'node:fs';
import { chromium } from 'playwright';
const invite = JSON.parse(readFileSync(process.env.VOICE_INVITE_FILE, 'utf8')).invite;
const input = process.env.VOICE_INPUT_WAV;
assert.ok(input, 'VOICE_INPUT_WAV required');
const base = process.env.VOICE_CHECK_URL ?? 'http://127.0.0.1:49181';
const out = process.env.VOICE_CHECK_OUT ?? '/tmp/infercat-080-real'; mkdirSync(out, { recursive: true });
const browser = await chromium.launch({ headless: true, ignoreDefaultArgs: ['--mute-audio'], args: ['--use-fake-device-for-media-stream', '--use-fake-ui-for-media-stream', `--use-file-for-fake-audio-capture=${input}`] });
let stage = 'connect';
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: 'en-US', permissions: ['microphone'] });
  const page = await context.newPage(); const errors = [];
  page.on('pageerror', (error) => errors.push(String(error)));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  await page.goto(`${base}/${process.env.VOICE_DIRECT === '1' ? '?direct' : ''}#${invite}`);
  const field = page.locator('.composer textarea'), mic = page.locator('.mic');
  await field.waitFor({ timeout: 120_000 });
  await page.waitForFunction(() => !document.querySelector('.composer textarea')?.disabled);
  assert.equal(await mic.count(), 1);
  stage = 'record'; await mic.click();
  await page.waitForFunction(() => document.querySelector('.mic')?.getAttribute('aria-pressed') === 'true');
  await page.waitForTimeout(3000); await page.screenshot({ path: `${out}/real-waveform.png` });
  await page.waitForTimeout(6500); await mic.click();
  stage = 'transcribe'; await page.waitForFunction(() => document.querySelector('.composer textarea')?.value.length > 0, null, { timeout: 120_000 });
  const transcript = await field.inputValue();
  assert.match(transcript.toLowerCase(), /bicycle/);
  await page.screenshot({ path: `${out}/real-transcript.png` });
  console.log('TRANSCRIPT', transcript.trim());
  console.log('RECORDING LINE', await page.locator('.composer .spoken').innerText());
  stage = 'send'; await field.fill(`${transcript}\nReply in one short sentence. /no_think`);
  await page.locator('.composer .primary').click();
  await page.locator('.row.assistant .listen').waitFor({ timeout: 180_000 });
  assert.ok((await page.locator('.row.assistant .md').last().innerText()).trim());
  await page.screenshot({ path: `${out}/real-reply.png` });
  console.log('REPLY', (await page.locator('.row.assistant .md').last().innerText()).trim());
  stage = 'listen'; await page.locator('.row.assistant .listen').last().click();
  await page.waitForFunction(() => document.querySelector('.row.assistant .spoken')?.textContent.includes(' / '), null, { timeout: 120_000 });
  await page.screenshot({ path: `${out}/real-listen.png` });
  console.log('PLAYING LINE', await page.locator('.row.assistant .spoken').innerText());
  await page.waitForFunction(() => !document.querySelector('.row.assistant .spoken'), null, { timeout: 120_000 });
  assert.equal(await page.locator('.row.assistant .ended.interrupted').count(), 0);
  assert.deepEqual(errors, []);
  console.log(`REAL VOICE PASS: MediaRecorder → transcription → edited text send → reply → native MP3 playback ended; ${process.env.VOICE_DIRECT === '1' ? 'direct' : 'real tunnel'}`);
} catch (error) { console.log(`REAL VOICE FAILED at ${stage}: ${error.name}`); throw new Error(`Real voice proof failed at ${stage}; inspect the isolated host logs.`); }
finally { await browser.close(); }
