import {preview} from 'vite';
import {chromium} from 'playwright';
import {readFile,mkdir,writeFile} from 'node:fs/promises';
const fixture=JSON.parse(await readFile(new URL('./fixture.json',import.meta.url),'utf8'));
await mkdir('test/evidence',{recursive:true});const server=await preview({preview:{host:'127.0.0.1',port:0}}),base=`http://127.0.0.1:${server.httpServer.address().port}`;const browser=await chromium.launch(),results=[];
try{
 for(const width of [1280,390])for(const lang of ['en','zh'])for(const state of ['settings','off','on','in-use','off-confirm','admin','admin-rotated']){
  const context=await browser.newContext({viewport:{width,height:900},timezoneId:'UTC'}),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(m.type()==='error')errors.push(m.text());});
  const data=structuredClone(fixture);Object.assign(data.settings,{writes_supported:true,default_web_url:'https://infercat.ai',configured_console:'127.0.0.1:9101',console_address:'127.0.0.1:9101',slots:4,running_slots:2,saved_at:{name:'2026-09-09T09:41:00Z',slots:'2026-09-09T09:41:00Z'},remote:{enabled:!['off','admin','settings'].includes(state),since:'2026-09-09T12:04:00Z',in_use:state==='in-use'}});
  await page.route('**/api/**',async route=>{
   const request=route.request(),url=new URL(request.url()),path=url.pathname.slice(5);
   if(request.method()!=='GET'){
    if(path!=='remote/enable'&&path!=='remote/rotate')throw new Error('unexpected mutation');data.settings.remote.enabled=true;
    await route.fulfill({json:{key_id:'admin',name:data.settings.name,invite:'ia1.tco2Fw8yq3znQ7KdLm4PvXe9RbHs2Wy6Tn.Qx7mKp2vTn9wRb4cHs8dLf3gJz6yNe1uAo5iWt0kMr',link:'https://infercat.ai/#ia1.tco2Fw8yq3znQ7KdLm4PvXe9RbHs2Wy6Tn.Qx7mKp2vTn9wRb4cHs8dLf3gJz6yNe1uAo5iWt0kMr'}});return;
   }
   await route.fulfill({json:path==='usage'?(url.searchParams.get('window')==='week'?data.week:data.today):data[path]});
  });
  await page.goto(`${base}/?lang=${lang}#token=fixture`);await page.locator('#settings').waitFor();await page.evaluate(()=>document.fonts.ready);
  if(state==='settings')await page.locator('#setting-web_url').fill('http://infercat.ai');
  if(state==='off-confirm')await page.locator('button[data-action="remote-confirm"]').click();
  const card=state.startsWith('admin');
  if(card){await page.locator(`[data-action="${state==='admin'?'remote-enable':'remote-rotate'}"]`).click();await page.locator('.once').waitFor();await page.locator('[data-action="new"]:not(:disabled)').waitFor({state:'attached'});const h=await page.locator('.drawer').evaluate(el=>el.scrollHeight);await page.setViewportSize({width,height:Math.max(900,h)});}
  const info=await page.evaluate(()=>({overflow:document.documentElement.scrollWidth-innerWidth,hash:location.hash,storage:localStorage.length+sessionStorage.length}));if(info.overflow||info.hash||info.storage||errors.length)throw new Error(JSON.stringify({state,width,lang,info,errors}));
  const name=`remote-${state}-${width}-${lang}`;if(card)await page.screenshot({path:`test/evidence/${name}.png`});else await page.locator('#settings').screenshot({path:`test/evidence/${name}.png`});results.push(name+': PASS');await context.close();
 }
 await writeFile('test/evidence/remote-screenshots.txt',results.join('\n')+'\n');console.log(`${results.length} passed / 0 failed / 0 skipped`);
}finally{await browser.close();await new Promise(r=>server.httpServer.close(r));}
