// Native MP3 proof through a real click. Run against Vite with VOICE_CHECK_URL.
import assert from 'node:assert/strict';
import { chromium, firefox, webkit } from 'playwright';
const base = process.env.VOICE_CHECK_URL ?? 'http://127.0.0.1:49180';
let passed = 0;
for (const [name, type] of Object.entries({ chromium, firefox, webkit })) {
  const browser = await type.launch({ headless: true });
  try {
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', (e) => errors.push(String(e)));
    page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
    await page.route('**/voice-media-test', (route) => route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>Voice media proof</title><button id="listen">Listen</button>' }));
    await page.goto(`${base}/voice-media-test`);
    const support = await page.evaluate(async () => {
      const { VoicePlayer, recordingMime } = await import('/src/voice.ts');
      const { handleFake } = await import('/dev/fake-backend.ts');
      window.states = []; window.requests = 0;
      window.player = new VoicePlayer((state) => window.states.push({ ...state, error: state.error ? String(state.error) : undefined }));
      document.getElementById('listen').onclick = () => {
        void window.player.play('reply', location.origin, 'test-tone', 'Native MP3 test', async () => {
          window.requests++;
          const response = handleFake({ method: 'POST', path: '/v1/audio/speech', headers: { authorization: 'Bearer test-key' }, body: '{"input":"test"}' }, { speech: true });
          return new window.Response(response.bytes, { headers: response.headers });
        });
      };
      return { recording: recordingMime(), playback: window.ManagedMediaSource?.isTypeSupported('audio/mpeg') ? 'managed' : window.MediaSource?.isTypeSupported('audio/mpeg') ? 'mse' : 'blob' };
    });
    await page.locator('#listen').click();
    await page.waitForFunction(() => window.states.some((s) => s.kind === 'playing' || s.kind === 'error'), null, { timeout: 15_000 });
    let states = await page.evaluate(() => window.states);
    assert.ok(states.some((s) => s.kind === 'playing'), `${name}: ${JSON.stringify(states)}`);
    await page.waitForFunction(() => window.player.state.kind === 'idle', null, { timeout: 15_000 });
    await page.evaluate(() => { window.states = []; });
    await page.locator('#listen').click();
    await page.waitForFunction(() => window.player.state.kind === 'playing', null, { timeout: 10_000 });
    assert.equal(await page.evaluate(() => window.requests), 1, 'cached replay is free');
    states = await page.evaluate(() => { window.player.stop(); return window.states; });
    assert.equal(states.at(-1).kind, 'idle');
    assert.deepEqual(errors, []);
    console.log(`MEDIA ${name} ${browser.version()} PASS ${JSON.stringify(support)}; decoded, ended, cached replay, Stop`);
    passed++;
  } finally { await browser.close(); }
}
console.log(`Native media: ${passed} passed / 0 failed / 0 skipped`);
