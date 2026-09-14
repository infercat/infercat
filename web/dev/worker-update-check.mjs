import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium, webkit } from 'playwright';
import { workerFixture } from './worker-fixture.mjs';
const shots = process.env.UPDATE_SHOTS ?? join(tmpdir(), 'infercat-worker-update');
await mkdir(shots, { recursive: true });
let revision = 1, checks = 0;
const { server, origin } = await workerFixture(() => `\n// fixture revision ${revision}\n`, process.env.CHECK_PORT ? Number(process.env.CHECK_PORT) + 3 : 0);
const ok = (value, message) => { assert.ok(value, message); checks++; };
try {
  const response = await fetch(`${origin}/github`, { redirect: 'manual' });
  ok(response.status === 302 && response.headers.get('location') === 'https://github.com/infercat/infercat', '/github rule');
  for (const name of (process.env.BROWSERS ?? 'chromium').split(',')) for (const language of ['en','zh']) {
    const browser = await ({chromium,webkit}[name]).launch();
    try {
      const page = await browser.newPage({ viewport: {width:390,height:844} });
      await page.addInitScript(language => {
        window.localStorage.setItem('bn.language',JSON.stringify(language)); window.__clock=0; const now=Date.now; Date.now=()=>now()+window.__clock;
        window.__checks=0;const update=window.ServiceWorkerRegistration.prototype.update;window.ServiceWorkerRegistration.prototype.update=function(){window.__checks++;return update.call(this);};
      },language);
      await page.goto(origin);await page.waitForFunction(()=>window.navigator.serviceWorker.controller?.state==='activated');
      ok(await page.locator('.worker-update').count()===0,'no first-install prompt');
      let navigations=0;page.on('framenavigated',frame=>{if(frame===page.mainFrame())navigations++;});
      revision++;
      await page.evaluate(async()=>{const r=await window.navigator.serviceWorker.ready;await r.update();});
      await page.locator('.worker-update').waitFor();ok(navigations===0,'installation does not reload');
      ok((await page.locator('.worker-update').innerText()).includes(language==='zh'?'新版本':'A newer version'),'localized status');
      const other=language==='en'?'zh-Hans':'en';await page.locator(`.language-links a[lang="${other}"]:visible`).first().click();
      await page.waitForFunction(chinese=>document.querySelector('.worker-update').textContent.includes(chinese?'新版本':'A newer version'),language==='en');checks++;
      await page.locator(`.language-links a[lang="${language==='zh'?'zh-Hans':'en'}"]:visible`).first().click();
      await page.evaluate(()=>document.fonts.ready);await page.screenshot({path:join(shots, `${name}-${language}.png`)});
      await page.locator('.worker-update button').last().click();ok(await page.locator('.worker-update').count()===0,'dismiss');
      ok(await page.evaluate(async()=>!!(await window.navigator.serviceWorker.ready).waiting),'dismiss leaves waiting worker');
      const before=await page.evaluate(()=>window.__checks);
      await page.evaluate(()=>{Object.defineProperty(document,'visibilityState',{configurable:true,get:()=> 'visible'});document.dispatchEvent(new window.Event('visibilitychange'));});
      ok(await page.evaluate(()=>window.__checks)===before,'no early visibility check');
      revision++;
      await page.evaluate(()=>{window.__clock=3600001;document.dispatchEvent(new window.Event('visibilitychange'));document.dispatchEvent(new window.Event('visibilitychange'));});
      await page.locator('.worker-update').waitFor();ok(await page.evaluate(()=>window.__checks)===before+1,'one hourly check');
      const reload=page.waitForEvent('framenavigated',{predicate:frame=>frame===page.mainFrame()});
      await page.locator('.worker-update button').first().click();await reload;
      await page.waitForFunction(()=>window.navigator.serviceWorker.controller?.state==='activated' && !document.querySelector('.worker-update'));
      await page.waitForLoadState('load');ok(navigations===1,'one explicit reload');
      console.log(`UPDATE ${name} ${language} PASS`);
    } finally { await browser.close(); }
  }
  console.log(`${checks} passed / 0 failed`);
} finally { await new Promise(resolve=>server.close(resolve)); }
