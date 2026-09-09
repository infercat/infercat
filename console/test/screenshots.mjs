import { preview } from 'vite';
import { chromium } from 'playwright';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const fixture = JSON.parse(await readFile(new URL('./fixture.json', import.meta.url), 'utf8'));
const evidence = resolve('test/evidence');
await mkdir(evidence, { recursive: true });
const server = await preview({ preview: { host: '127.0.0.1', port: 0 } });
const address = server.httpServer.address();
const base = `http://127.0.0.1:${address.port}`;
console.log(`CONSOLE_PREVIEW=${base}`);
const browser = await chromium.launch();
const results = [];
try {
 for (const width of [1280,390]) for (const lang of ['en','zh']) for (const state of ['main','drawer','revoked','empty','offline','engine-down','pinned','mint-form','mint','confirm']) {
  const context = await browser.newContext({ viewport: { width, height: width===1280?900:844 }, deviceScaleFactor: 1 });
  const page = await context.newPage();
  const errors=[]; let offline=false;
  page.on('pageerror',e=>errors.push(e.message));
  page.on('console',m=>{if(m.type()==='error' && !offline) errors.push(m.text());});
  await page.addInitScript(() => { Date.now = () => Date.parse('2026-09-06T12:00:00Z'); });
  const data=structuredClone(fixture);
  if(state==='empty') {
   data.keys=[]; data.status.keys=[];
   const zero=Object.fromEntries(Object.keys(data.today.total).map(k=>[k,typeof data.today.total[k]==='number'?0:{}]));
   for(const report of [data.today,data.week]) { report.total=zero; report.keys=[]; for(const day of report.daily) { day.total=zero;day.keys=[]; } }
  }
  if(state==='pinned') data.status.models_pinned=[data.engine.models[0]];
  if(state==='engine-down') { data.engine.health.ok=false; data.engine.health.err='connection refused'; }
  await page.route('**/api/**',async route=>{
   if(offline) { await route.abort(); return; }
   if(route.request().method()!=='GET') {
    if(route.request().method()!=='POST' || !route.request().url().endsWith('/api/keys')) throw new Error('unexpected write');
    const body=route.request().postDataJSON();
    data.keys.push({...data.keys[0],id:'k_c31d08',name:body.name,limits:body.limits,status:'active',last_seen:'',today_tokens:0});
    await route.fulfill({json:{key_id:'k_c31d08',name:body.name,invite:'ic1.tco2Fw8yq3znQ7KdLm4PvXe9RbHs2Wy6Tn.rrMAuK3fZp8QdL1xN0vY7cWbT4gHs9JmE2kR5uC7nNU',link:'https://infercat.ai/#ic1.tco2Fw8yq3znQ7KdLm4PvXe9RbHs2Wy6Tn.rrMAuK3fZp8QdL1xN0vY7cWbT4gHs9JmE2kR5uC7nNU'}});return;
   }
   const url=new URL(route.request().url()); const key=url.pathname.slice(5);
   const payload=key==='usage'?(url.searchParams.get('window')==='week'?data.week:data.today):data[key];
   await route.fulfill({json:payload});
  });
  await page.goto(`${base}/?lang=${lang}#token=fixture-token`);
  await page.locator('#friends').waitFor(); await page.evaluate(()=>document.fonts.ready);
  if(state==='mint'||state==='mint-form') {
   await page.locator('[data-action="new"]').click();await page.locator('#invite-name').fill('erin');
   if(state==='mint') { await page.locator('form button[type="submit"]').click();await page.locator('.once').waitFor();await page.locator('[data-action="new"]:not(:disabled)').waitFor({state:'attached'}); }
  }
  if(state==='drawer'||state==='confirm') {
   await page.locator('[data-key="k_7f3a2b"]').click();
   if(state==='confirm') await page.locator('[data-action="confirm"]').click();
   const h=await page.locator('.drawer').evaluate(el=>el.scrollHeight);
   await page.setViewportSize({width,height:Math.max(width===1280?900:844,h)});
  }
  if(['mint','mint-form'].includes(state)) { const h=await page.locator('.drawer').evaluate(el=>el.scrollHeight);await page.setViewportSize({width,height:Math.max(width===1280?900:844,h)}); }
  if(state==='revoked') await page.locator('details summary').click();
  if(state==='offline') { offline=true; await page.locator('.stale').waitFor(); }
  const measurements=await page.evaluate(()=>({overflow:document.documentElement.scrollWidth-innerWidth,hash:location.hash,local:localStorage.length,session:sessionStorage.length}));
  if(measurements.overflow>0 || measurements.hash || measurements.local || measurements.session || errors.length) throw new Error(JSON.stringify({state,width,lang,measurements,errors}));
  const name=`${state}-${width}-${lang}`;
  await page.screenshot({path:`${evidence}/${name}.png`,fullPage:!['drawer','confirm','mint','mint-form'].includes(state)});
  results.push(`${name}: PASS (no overflow, no token storage, no unexpected browser errors)`);
  await context.close();
 }
 await writeFile(`${evidence}/results.txt`,results.join('\n')+'\n');
 console.log(results.join('\n'));
} finally { await browser.close(); await new Promise(resolve=>server.httpServer.close(resolve)); }
