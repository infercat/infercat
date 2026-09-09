// Uses only a local, isolated host. Supply two model-restricted invites from that host.
import { chromium } from 'playwright';
import { readFileSync, mkdirSync } from 'node:fs';
import assert from 'node:assert/strict';
const invites = JSON.parse(readFileSync(process.env.IMAGE_PROOF_INVITES ?? '/tmp/infercat-073-proof-invites.json', 'utf8'));
const base = process.env.IMAGE_LIVE_URL ?? 'http://127.0.0.1:49184';
const out = '/tmp/infercat-073-live'; mkdirSync(out, { recursive: true });
const browser = await chromium.launch();
try {
  for (const kind of ['vision', 'text']) {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    const page = await context.newPage();
    const errors = []; page.on('pageerror', (e) => errors.push(String(e)));
    await page.goto(`${base}/?direct&invite=${encodeURIComponent(invites[kind])}&autoconnect`);
    await page.locator('.composer textarea').waitFor();
    await page.waitForFunction(() => !document.querySelector('.composer textarea')?.disabled);
    await page.evaluate(async () => {
      const dt = new window.DataTransfer();
      for (const [color, shape] of [['red','square'],['blue','circle']]) {
        const c = document.createElement('canvas'); c.width=1600; c.height=1200; const x=c.getContext('2d');
        x.fillStyle='white'; x.fillRect(0,0,1600,1200); x.fillStyle=color;
        if (shape==='square') x.fillRect(400,200,800,800); else { x.beginPath();x.arc(800,600,400,0,2*Math.PI);x.fill(); }
        const blob=await new Promise((r)=>c.toBlob(r,'image/png'));
        dt.items.add(new window.File([blob],`${color}-${shape}.png`,{type:'image/png'}));
      }
      document.querySelector('.composer textarea').dispatchEvent(new window.ClipboardEvent('paste',{clipboardData:dt,bubbles:true,cancelable:true}));
    });
    if (kind === 'vision') {
      await page.waitForFunction(() => document.querySelectorAll('.thumb').length === 2);
      let sent;
      page.on('request', (r) => { if (r.url().endsWith('/v1/chat/completions')) sent = JSON.parse(r.postData()); });
      await page.locator('.composer textarea').fill('Describe the color and shape in the first image and the second image. One short sentence.');
      await page.locator('.composer .primary').click();
      await page.waitForFunction(() => document.querySelector('.row.assistant .meta-text')?.textContent.includes('tokens in'), undefined, { timeout: 180000 });
      const reply = await page.locator('.row.assistant > .md').innerText();
      assert.match(reply, /red/i); assert.match(reply, /blue/i); assert.match(reply, /square/i); assert.match(reply, /circle/i);
      const parts = sent.messages.at(-1).content; assert.equal(parts.filter((p) => p.type === 'image_url').length, 2);
      assert.equal(parts.at(-1).type, 'text');
      console.log('REAL VISION: two JPEG image_url parts, text last; ' + reply.replaceAll('\n',' ').slice(-1200));
    } else {
      assert.equal(await page.locator('.attach').count(), 0);
      await page.locator('.image-notice').waitFor();
      assert.match(await page.locator('.image-notice').innerText(), /can’t see images/);
      assert.equal(await page.locator('.thumb').count(), 0);
      console.log('REAL TEXT: attach absent; ' + await page.locator('.image-notice').innerText());
    }
    await page.screenshot({ path: `${out}/${kind}.png` });
    assert.deepEqual(errors, []);
    await context.close();
  }
  console.log('REAL IMAGE CHECK: 2 flows passed / 0 failed / 0 skipped');
} finally { await browser.close(); }
