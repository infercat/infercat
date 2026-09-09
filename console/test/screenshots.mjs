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
 for (const width of [1280,390]) for (const lang of ['en','zh']) for (const state of ['main','drawer','revoked','empty','offline','engine-down','pinned']) {
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
   if(route.request().method()!=='GET') throw new Error('write from read-only page');
   const url=new URL(route.request().url()); const key=url.pathname.slice(5);
   const payload=key==='usage'?(url.searchParams.get('window')==='week'?data.week:data.today):data[key];
   await route.fulfill({json:payload});
  });
  await page.goto(`${base}/?lang=${lang}#token=fixture-token`);
  await page.locator('#friends').waitFor(); await page.evaluate(()=>document.fonts.ready);
  if(state==='drawer') {
   await page.locator('[data-key="k_7f3a2b"]').click();
   const h=await page.locator('.drawer').evaluate(el=>el.scrollHeight);
   await page.setViewportSize({width,height:Math.max(width===1280?900:844,h)});
  }
  if(state==='revoked') await page.locator('details summary').click();
  if(state==='offline') { offline=true; await page.locator('.stale').waitFor(); }
  const measurements=await page.evaluate(()=>({overflow:document.documentElement.scrollWidth-innerWidth,hash:location.hash,local:localStorage.length,session:sessionStorage.length}));
  if(measurements.overflow>0 || measurements.hash || measurements.local || measurements.session || errors.length) throw new Error(JSON.stringify({state,width,lang,measurements,errors}));
  const name=`${state}-${width}-${lang}`;
  await page.screenshot({path:`${evidence}/${name}.png`,fullPage:state!=='drawer'});
  results.push(`${name}: PASS (no overflow, no token storage, no unexpected browser errors)`);
  await context.close();
 }
 await writeFile(`${evidence}/results.txt`,results.join('\n')+'\n');
 console.log(results.join('\n'));
} finally { await browser.close(); await new Promise(resolve=>server.httpServer.close(resolve)); }
