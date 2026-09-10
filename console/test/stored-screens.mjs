import { preview } from 'vite';
import { chromium } from 'playwright';
import { readFile, mkdir } from 'node:fs/promises';
import assert from 'node:assert/strict';
const fixture=JSON.parse(await readFile(new URL('./fixture.json',import.meta.url),'utf8')), stored=JSON.parse(await readFile(new URL('./stored.json',import.meta.url),'utf8'));
const out='/private/tmp/infercat-158-screens';await mkdir(out,{recursive:true});
const server=await preview({preview:{host:'127.0.0.1',port:0}}), base=`http://127.0.0.1:${server.httpServer.address().port}`, browser=await chromium.launch();let passed=0;
try {for(const width of [390,1280])for(const lang of ['en','zh']){
 const context=await browser.newContext({viewport:{width,height:900}}),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(String(e)));
 await page.addInitScript(()=>{Date.now=()=>Date.parse('2026-09-06T12:00:00Z');});
 await page.route('**/api/**',async route=>{assert.equal(route.request().method(),'GET');const url=new URL(route.request().url()),path=url.pathname.slice(5);const value=path==='stored'?{...stored,key_id:fixture.keys[0].id}:path==='usage'?url.searchParams.get('window')==='week'?fixture.week:fixture.today:fixture[path];await route.fulfill({json:value});});
 await page.goto(`${base}/?lang=${lang}#token=fixture-only`);await page.locator(`[data-key="${fixture.keys[0].id}"]`).click();await page.locator('#stored-details').waitFor();
 await page.locator('#stored-details summary').click();await page.locator('[data-action="stored-confirm"]').click();await page.locator('#stored-details').scrollIntoViewIfNeeded();await page.evaluate(()=>document.fonts.ready);
 assert.ok((await page.locator('#stored-details').innerText()).includes('17 MiB'));assert.deepEqual(errors,[]);
 assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth));
 await page.screenshot({path:`${out}/stored-${width}-${lang}.png`});passed++;await context.close();
}console.log(`${passed} stored screenshot cases passed / 0 failed; ${out}`);}finally{await browser.close();await server.httpServer.close();}
