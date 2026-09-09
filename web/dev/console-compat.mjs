import {readFileSync,mkdirSync,writeFileSync} from 'node:fs';
import assert from 'node:assert/strict';
import {resolve} from 'node:path';
export async function consoleCompat(base,browser){
 const capture=JSON.parse(readFileSync(new URL('../src/fixtures/console/current-085-dev.json',import.meta.url),'utf8'));
 const me=JSON.parse(readFileSync(new URL('../src/fixtures/me/current.json',import.meta.url),'utf8'));
 const code='ia1.tcCOMPATproofaddressCOMPATproofaddress.'+'A'.repeat(43),chat=code.replace('ia1.','ic1.');let passed=0;
 const evidence=resolve('../console/test/evidence/remote-web');mkdirSync(evidence,{recursive:true});
 for(const variant of ['current-085-dev','legacy-optionals','refused','closed','budget']){
  const context=await browser.newContext({locale:'en-US'}),page=await context.newPage(),data=globalThis.structuredClone(capture),errors=[],calls=[];let outcome=200;
  if(variant==='legacy-optionals'){delete data.status.models_pinned;delete data.status.audio;data.engine.models=null;delete data.week.daily;data.today.keys=null;delete data.settings.saved_at;}
  page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(m.type()==='error'&&!m.text().includes('status of 4'))errors.push(m.text());});
  await context.route('http://127.0.0.1:49090/**',async route=>{
   const r=route.request(),url=new URL(r.url()),path=url.pathname;calls.push({path,method:r.method(),auth:r.headers().authorization});
   if(path==='/healthz'){await route.fulfill({json:{ok:true},headers:{'Access-Control-Allow-Origin':'*'}});return;}
   if(path==='/me'){await route.fulfill({json:me});return;}if(path==='/v1/models'){await route.fulfill({json:{data:me.host.models.map(id=>({id}))}});return;}
   assert.ok(path.startsWith('/console/api/'));assert.equal(r.headers().authorization,'Bearer '+'A'.repeat(43));
   if(outcome!==200){await route.fulfill({status:outcome,body:'refused',headers:{'Retry-After':'10','Access-Control-Allow-Origin':'*'}});return;}
   const key=path.slice('/console/api/'.length);await route.fulfill({json:key==='usage'?(url.searchParams.get('window')==='week'?data.week:data.today):data[key],headers:{'Access-Control-Allow-Origin':'*'}});
  });
  await page.goto(base+'console?direct');await page.getByRole('heading',{name:'Remote console'}).waitFor();
  const before=await page.evaluate(()=>{const b=document.createElement('button');b.className='primary';b.id='outside-probe';document.body.append(b);const s=window.getComputedStyle(b);return {color:s.color,background:s.backgroundColor,font:s.fontFamily};});
  await page.evaluate(chat=>{window.localStorage.setItem('bn.invite',JSON.stringify(chat));window.localStorage.setItem('bn.privateKey',JSON.stringify('remembered-private-key'));},chat);
  calls.length=0;await page.goto(base+'console?direct#'+code);await page.locator('#friends').waitFor({timeout:10000}).catch(async e=>{console.log(JSON.stringify({errors,calls,body:await page.locator('body').innerText()}));throw e;});await page.evaluate(()=>document.fonts.ready);
  assert.equal(calls.filter(c=>c.path==='/me').length,0,'admin route must not bootstrap chat');
  await page.locator('a[href="#settings"]').click();assert.equal(await page.evaluate(()=>location.hash),'');await page.evaluate(()=>window.scrollTo(0,0));
  assert.equal(await page.locator('#setting-console').count(),0);assert.ok((await page.locator('.head').innerText()).includes('through the tunnel'));
  const isolation=await page.evaluate(()=>{const b=document.createElement('button');b.className='primary';document.body.append(b);const s=window.getComputedStyle(b);return {style:{color:s.color,background:s.backgroundColor,font:s.fontFamily},fonts:[...document.fonts].filter(f=>f.family.includes('Infercat Console')).map(f=>({family:f.family,status:f.status})),hash:location.hash,invite:window.localStorage.getItem('bn.invite'),key:window.localStorage.getItem('bn.privateKey'),session:window.sessionStorage.length};});
  assert.deepEqual(isolation.style,before);assert.equal(isolation.fonts.length,4);assert.ok(isolation.fonts.every(f=>f.status==='loaded'));assert.equal(isolation.hash,'');assert.equal(isolation.invite,JSON.stringify(chat));assert.equal(isolation.key,JSON.stringify('remembered-private-key'));assert.equal(isolation.session,0);
  if(variant==='current-085-dev')for(const width of [1280,390])for(const lang of ['en','zh']){await page.setViewportSize({width,height:900});await page.locator(`[data-lang="${lang}"]`).click();await page.screenshot({path:`${evidence}/header-${width}-${lang}.png`,fullPage:true});await page.locator('.head').screenshot({path:`${evidence}/header-only-${width}-${lang}.png`});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth),false,JSON.stringify({width,lang,overflow:await page.evaluate(()=>[...document.querySelector('[data-remote-console]').shadowRoot.querySelectorAll('*')].filter(e=>e.getBoundingClientRect().right>window.innerWidth+1).map(e=>({tag:e.tagName,cls:e.className,text:e.textContent.slice(0,100)})).slice(-12))}));}
  if(['refused','closed','budget'].includes(variant)){
   outcome=variant==='refused'?401:variant==='closed'?404:429;
   await page.getByText(variant==='refused'?'This admin code was refused. Open a fresh code from the host.':variant==='closed'?/Closed · observed at/:/Too many console requests/).first().waitFor();
   const count=calls.length;await page.waitForTimeout(4200);assert.equal(calls.length,count,'terminal states and backoff must not keep reading');await page.screenshot({path:`${evidence}/${variant}.png`,fullPage:true});
  }
  assert.deepEqual(errors,[]);assert.ok(calls.every(c=>c.method==='GET'));
  await page.locator('[data-action="leave"]').click();await page.waitForURL(url=>url.pathname==='/');await page.waitForFunction(()=>[...document.fonts].filter(f=>f.family.includes('Infercat Console')).length===0);assert.equal(await page.evaluate(()=>[...document.fonts].filter(f=>f.family.includes('Infercat Console')).length),0);
  const after=await page.evaluate(()=>{const b=document.createElement('button');b.className='primary';document.body.append(b);const s=window.getComputedStyle(b);return {color:s.color,background:s.backgroundColor,font:s.fontFamily};});assert.deepEqual(after,before);
  await context.close();console.log('CONSOLE COMPAT '+variant+' PASS (storage, fonts, styles, route)');passed++;
 }
 const pasteContext=await browser.newContext(),paste=await pasteContext.newPage();
 await pasteContext.route('http://127.0.0.1:49090/**',async route=>{const u=new URL(route.request().url()),key=u.pathname.slice('/console/api/'.length);assert.notEqual(u.pathname,'/me','admin paste must not verify chat');await route.fulfill({json:u.pathname==='/healthz'?{ok:true}:key==='usage'?(u.searchParams.get('window')==='week'?capture.week:capture.today):capture[key],headers:{'Access-Control-Allow-Origin':'*'}});});
 await paste.goto(base+'?direct');await paste.locator('textarea').fill(code);await paste.evaluate(()=>window.dispatchEvent(new window.Event('offline')));assert.equal(await paste.getByRole('button',{name:'Open console',exact:true}).isDisabled(),true);assert.equal(await paste.locator('[data-remote-console]').count(),0);await paste.evaluate(()=>window.dispatchEvent(new window.Event('online')));await paste.locator('#friends').waitFor();assert.equal(await paste.evaluate(()=>window.localStorage.length+window.sessionStorage.length),0);await pasteContext.close();passed++;console.log('CONSOLE COMPAT pasted ia1 PASS');
 writeFileSync(`${evidence}/results.txt`,`${passed} passed / 0 failed / 0 skipped\n`);return passed;
}
