// All voice surfaces through the real app/fake tunnel. Native codecs are checked separately.
import assert from 'node:assert/strict';
import { mkdirSync } from 'node:fs';
import { Buffer } from 'node:buffer';
import { chromium } from 'playwright';
const base = process.env.VOICE_CHECK_URL ?? 'http://127.0.0.1:49180';
const out = process.env.VOICE_CHECK_OUT ?? '/tmp/infercat-080-shots';
mkdirSync(out, { recursive: true });
const invite = `ic1.tcVOICEproofaddressVOICEproofaddress.${'D'.repeat(43)}`;
const browser = await chromium.launch({ headless: true });
let checks = 0;
const check = (ok, message) => { assert.ok(ok, message); checks++; };
const errors = [];
async function connected(width, lang, extra = '&transcriptions&speech') {
  const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 800 }, locale: lang === 'zh' ? 'zh-CN' : 'en-US', isMobile: width === 390, hasTouch: width === 390 });
  await context.addInitScript((language) => {
    window.localStorage.setItem('bn.language', JSON.stringify(language));
    window.__level = .13; window.__micError = ''; window.__tracksStopped = 0;
    Object.defineProperty(window.navigator, 'mediaDevices', { configurable: true, value: { getUserMedia: async () => {
      if (window.__holdMic) await new Promise((resolve) => { window.__permitMic = resolve; });
      if (window.__micError) throw new window.DOMException('microphone unavailable', window.__micError);
      return { getTracks: () => [{ stop: () => window.__tracksStopped++ }] };
    } } });
    window.MediaRecorder = class {
      static isTypeSupported(type) { return type.includes('webm'); }
      state = 'inactive'; mimeType = 'audio/webm;codecs=opus';
      start() { this.state = 'recording'; }
      stop() { this.state = 'inactive'; window.queueMicrotask(() => { this.ondataavailable?.({ data: new window.Blob(['test recording']) }); this.onstop?.(); }); }
    };
    window.AudioContext = class {
      resume() { return Promise.resolve(); } close() { return Promise.resolve(); }
      createMediaStreamSource() { return { connect() {} }; }
      createAnalyser() { return { fftSize: 1024, getFloatTimeDomainData(data) { data.fill(window.__level * (.2 + Math.abs(Math.sin(performance.now() / 150)))); } }; }
    };
  }, lang);
  const page = await context.newPage();
  page.on('pageerror', (e) => errors.push(String(e)));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  await page.goto(`${base}/?fake&connectMs=1&tokenDelay=1&audioDelay=650${extra}#${invite}`);
  await page.locator('.composer textarea').waitFor();
  await page.waitForFunction(() => !document.querySelector('.composer textarea').disabled);
  await page.evaluate(async () => {
    const { FakeConn } = await import('/dev/fake-infercat-tunnel.ts');
    const write = FakeConn.prototype.write; window.__posts = [];
    FakeConn.prototype.write = function (bytes) {
      const text = new window.TextDecoder().decode(bytes);
      if (text.startsWith('POST ')) window.__posts.push({ path: text.split(' ')[1], body: text.split('\r\n\r\n').slice(1).join('\r\n\r\n') });
      return write.call(this, bytes);
    };
  });
  return { page, context };
}
const posts = (page, path) => page.evaluate((path) => window.__posts.filter((p) => p.path === path), path);
async function shot(page, name) {
  await page.evaluate(() => document.fonts.ready);
  check(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${name}: horizontal overflow`);
  await page.screenshot({ path: `${out}/${name}.png` }); console.log('SHOT', name);
}
try {
  for (const width of [390, 1280]) for (const lang of ['en', 'zh']) {
    const { page, context } = await connected(width, lang);
    const tag = `${width}-${lang}`, field = page.locator('.composer textarea'), mic = page.locator('.composer .mic');
    try {
      check(await mic.count() === 1, 'capability exposes microphone');
      await shot(page, `idle-${tag}`);
      await page.evaluate(() => { window.__holdMic = true; }); await mic.click();
      check((await page.locator('.composer .spoken').innerText()).includes(lang === 'zh' ? '正在等待麦克风' : 'Waiting for the microphone'), 'permission pending is honestly labelled');
      check(await page.locator('.composer .waveform').count() === 0, 'no recording waveform before permission');
      await shot(page, `waiting-${tag}`);
      await page.locator('.composer .spoken button').click();
      await page.evaluate(() => { window.__holdMic = false; window.__permitMic(); });
      await page.waitForTimeout(30);
      check(await page.locator('.composer .spoken').count() === 0, 'late permission stays cancelled');

      await page.getByRole('button', { name: lang === 'zh' ? '设置' : 'Settings', exact: true }).click();
      check((await page.locator('.sheet').innerText()).includes('whisper-large-v3') && (await page.locator('.sheet').innerText()).includes('kokoro'), 'Settings names both audio models');
      await page.locator('.sheet .small-print').first().scrollIntoViewIfNeeded(); await shot(page, `settings-${tag}`);
      await page.locator('.sheet .primary').click();
      await page.locator('.meters').click();
      const voiceLimits = page.locator('.sheet p').filter({ hasText: lang === 'zh' ? '语音也计入限额' : 'Voice counts too' });
      await voiceLimits.scrollIntoViewIfNeeded(); check((await voiceLimits.innerText()).includes(lang === 'zh' ? '控制台' : 'console'), 'Limits states separate host-counted budgets');
      await shot(page, `limits-${tag}`); await page.locator('.sheet .primary').click();
      await field.fill('Typed'); await mic.click();
      await page.waitForFunction(() => document.querySelector('.mic')?.getAttribute('aria-pressed') === 'true');
      await page.waitForTimeout(3100);
      check((await posts(page, '/v1/audio/transcriptions')).length === 0, 'recording stays local');
      check(await page.locator('.waveform').count() === 1 && await page.locator('.level').count() === 0, 'waveform replaces rail');
      await shot(page, `recording-${tag}`);
      const wave = await page.locator('.waveform').boundingBox(), cancel = await page.locator('.composer .spoken button').boundingBox();
      check(wave && cancel && wave.x + wave.width < cancel.x, 'waveform fits beside Cancel');
      await page.locator('input[type=file]').setInputFiles({ name: 'note.txt', mimeType: 'text/plain', buffer: Buffer.from('An attached note with enough readable text to accompany this recorded question.') });
      await page.locator('.chip').waitFor();
      await shot(page, `recording-file-${tag}`);
      await page.locator('.composer .spoken button').click();
      check((await posts(page, '/v1/audio/transcriptions')).length === 0, 'Cancel discards without upload');
      await page.evaluate(() => { window.__level = 0; }); await mic.click(); await page.waitForTimeout(1200);
      await shot(page, `muted-${tag}`);
      await mic.click();
      await page.waitForFunction(() => document.querySelector('.composer .mic')?.disabled);
      await shot(page, `transcribing-${tag}`);
      await page.waitForFunction(() => document.querySelector('.composer textarea')?.value !== 'Typed');
      check((await field.inputValue()).startsWith('Typed '), 'transcript appends to current draft');
      check(await page.evaluate(() => document.activeElement === document.querySelector('.composer textarea')) === (width !== 390), 'only desktop focuses transcript');
      check((await posts(page, '/v1/audio/transcriptions')).every((p) => !p.body.includes('name="model"')), 'no transcription model selected by client');
      await shot(page, `transcribed-${tag}`);
      check((await posts(page, '/v1/chat/completions')).length === 0, 'transcription never auto-sends');
      await field.fill('A fresh editable question.');
      check(await page.locator('.composer .spoken').count() === 0, 'editing clears measurement');
      await page.locator('.composer .primary').click();
      await page.locator('.row.assistant .listen').waitFor();
      const chat = await posts(page, '/v1/chat/completions');
      check(typeof JSON.parse(chat.at(-1).body).messages.at(-1).content === 'string', 'approved turn is text only');
      const listen = page.locator('.row.assistant .listen').last(); await listen.click();
      await page.locator('.row.assistant .spoken').waitFor(); await shot(page, `making-${tag}`);
      await page.waitForFunction(() => document.querySelector('.row.assistant .spoken')?.textContent.includes(' / '));
      await shot(page, `playing-${tag}`); await listen.click();
      check(await page.locator('.row.assistant .spoken').count() === 0, 'Stop clears playback line');
      const speech = await posts(page, '/v1/audio/speech');
      check(speech.length === 1 && !('model' in JSON.parse(speech[0].body)), 'speech sends no model');
      check(!JSON.parse(speech[0].body).input.includes('```') && !JSON.parse(speech[0].body).input.includes('**'), 'speech strips markdown syntax');
      await listen.click(); await page.waitForFunction(() => document.querySelector('.row.assistant .spoken')?.textContent.includes(' / ')); await listen.click();
      check((await posts(page, '/v1/audio/speech')).length === 1, 'cached replay makes no request');
      check(await page.evaluate(() => !JSON.stringify(window.localStorage).includes('data:audio') && !JSON.stringify(window.localStorage).includes('recording.webm')), 'audio is not stored');
      await mic.click(); await page.waitForTimeout(100); await mic.click();
      await page.locator('.composer .spoken button').click(); await page.waitForTimeout(750);
      check(await field.inputValue() === '' && await page.locator('.composer .spoken').count() === 0, 'Cancel discards a late transcription');
      await page.evaluate(() => { window.__micError = 'NotAllowedError'; }); await mic.click();
      await page.locator('.composer .hint').filter({ hasText: lang === 'zh' ? '麦克风' : 'Microphone blocked' }).waitFor(); await shot(page, `denied-${tag}`);
      await page.evaluate(() => { window.__micError = 'NotFoundError'; }); await mic.click();
      await page.locator('.composer .hint').filter({ hasText: lang === 'zh' ? '没有麦克风' : 'No microphone' }).waitFor(); await shot(page, `missing-${tag}`);
      await page.evaluate(() => { window.__micError = ''; }); await mic.click();
      const before = (await posts(page, '/v1/audio/transcriptions')).length;
      await page.evaluate(() => window.dispatchEvent(new window.Event('offline')));
      await page.waitForFunction(() => document.querySelector('.composer .mic')?.disabled);
      check(await field.isEnabled(), 'offline draft stays editable'); check(await listen.isDisabled(), 'shared gate blocks Listen');
      check((await posts(page, '/v1/audio/transcriptions')).length === before, 'offline discards recording without upload');
      await shot(page, `offline-${tag}`);
      await page.evaluate(() => window.dispatchEvent(new window.Event('online')));
      await page.waitForFunction(() => !document.querySelector('.composer .mic')?.disabled);
      await mic.click(); const pagehideCalls = (await posts(page, '/v1/audio/transcriptions')).length;
      await page.evaluate(() => window.dispatchEvent(new window.Event('pagehide')));
      await page.waitForFunction(() => document.querySelector('.mic')?.getAttribute('aria-pressed') === 'false');
      check((await posts(page, '/v1/audio/transcriptions')).length === pagehideCalls, 'leaving the page discards recording');
    } catch (error) { console.log('FAILED', tag, await page.locator('.composer').innerText(), errors); await page.screenshot({ path: `${out}/failed-${tag}.png` }); throw error; } finally { await context.close(); }
    const no = await connected(width, lang, '');
    check(await no.page.locator('.mic').count() === 0, 'absent capability hides microphone'); await shot(no.page, `no-voice-${tag}`); await no.context.close();
    const bad = await connected(width, lang, '&transcriptions&speech&audioFailure=429');
    await bad.page.locator('.mic').click();
    await bad.page.waitForFunction(() => document.querySelector('.mic')?.getAttribute('aria-pressed') === 'true');
    await bad.page.locator('.mic').click();
    await bad.page.locator('.spoken.bad').waitFor(); await shot(bad.page, `transcription-refused-${tag}`);
    check(await bad.page.locator('.banner .banner-actions button').count() === 1, 'audio cooldown never offers chat regeneration');
    await bad.page.locator('.composer textarea').fill('A reply to listen to.'); await bad.page.locator('.composer .primary').click();
    await bad.page.locator('.listen').waitFor(); await bad.page.locator('.listen').click();
    await bad.page.locator('.row.assistant .ended.interrupted').waitFor(); await bad.page.locator('.host-said summary').last().click();
    await shot(bad.page, `speech-refused-${tag}`); check((await bad.page.locator('.host-said').last().textContent()).includes('speech_budget_exhausted'), 'raw refusal under Details');
    await bad.context.close();
  }
  check(errors.length === 0, errors.join('\n'));
  console.log(`VOICE UI: ${checks} passed / 0 failed / 0 skipped; screenshots ${out}`);
} finally { await browser.close(); }
