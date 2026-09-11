import { chromium } from 'playwright';
import { createServer } from 'vite';
import { mkdirSync } from 'node:fs';
import assert from 'node:assert/strict';
const out='/private/tmp/infercat-167-screens';mkdirSync(out,{recursive:true});
const server=await createServer({server:{host:'127.0.0.1',port:0}});await server.listen();const browser=await chromium.launch();let checks=0;
const ok=(v,m)=>{assert.ok(v,m);checks++;};
try{for(const tools of [['make_image'],['web_search'],['make_image','web_search']])for(const width of [390,1280])for(const lang of ['en','zh']){
 const ctx=await browser.newContext({viewport:{width,height:width===390?844:900}}),page=await ctx.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(lang=>window.localStorage.setItem('bn.language',JSON.stringify(lang)),lang);
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/?fake&hostTools=${tools.join(',')}${tools.includes('make_image')?'&imageJobs':''}&connectMs=20&invite=ic1.tcRUNproofaddressRUNproofaddress.${'D'.repeat(43)}&autoconnect`);
 await page.locator('.composer textarea').waitFor();await page.waitForFunction(()=>!document.querySelector('.composer textarea').disabled);
 await page.evaluate(async()=>{const {TunnelTransport}=await import('/src/transport/index.ts'),old=TunnelTransport.prototype.fetch;window.__requests=[];TunnelTransport.prototype.fetch=function(path,init){window.__requests.push({path,body:init?.body,auth:new window.Headers(init?.headers).has('authorization')});return old.call(this,path,init);};});
 await page.locator('.composer textarea').fill('Find the fox facts, then draw a fox if you can.');await page.locator('.composer .primary').click();await page.locator('.steps summary').waitFor();await page.locator('.steps summary').click();
 await page.waitForFunction(n=>document.querySelectorAll('.steps .step').length===n,tools.length);
 const asked=await page.evaluate(()=>window.__requests.filter(r=>r.path==='/v1/chat/completions'));ok(asked.length===1,'one request');assert.deepEqual(JSON.parse(asked[0].body).host_tools,tools);checks++;
 ok(await page.locator('.steps .step').count()===tools.length,'all offered steps');
 if(tools.includes('web_search')){
  await page.locator('.steps button.r').filter({hasText:'4'}).click();await page.locator('.sheet.file pre').waitFor();ok((await page.locator('.sheet.file pre').innerText()).includes('Four search results'),'captured result opened');ok(await page.evaluate(()=>window.__requests.some(r=>r.path.endsWith('/outputs/search')&&r.auth)),'authenticated scoped output read');await page.keyboard.press('Escape');
 }
 if(tools.includes('make_image')){await page.waitForFunction(()=>document.querySelectorAll('.row.run').length===3);ok(true,'image rows retained alongside tool steps');}
 await page.evaluate(()=>document.fonts.ready);ok(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),'no overflow');await page.screenshot({path:`${out}/${tools.join('-')}-${width}-${lang}.png`});
 if(tools.length===1&&tools[0]==='web_search'){await page.reload();await page.locator('.steps summary').waitFor();ok(await page.locator('.row.run').count()===1,'search-only reconnect retains one trace');}
 ok(errors.length===0,errors.join('\n'));await ctx.close();
}console.log(`${checks} passed / 0 failed; 12 captures; ${out}`);}finally{await browser.close();await server.close();}
