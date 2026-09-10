import {preview} from 'vite';
import {chromium} from 'playwright';
import {readFile,mkdir,writeFile} from 'node:fs/promises';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import assert from 'node:assert/strict';
import {bridgeAudioFixture} from './bridge-audio-fixture.js';
const fixture=JSON.parse(await readFile(new URL('./fixture.json',import.meta.url),'utf8')),out=new URL('./evidence/bridge-audio/',import.meta.url);await mkdir(out,{recursive:true});
const server=await preview({preview:{host:'127.0.0.1',port:0}}),base=`http://127.0.0.1:${server.httpServer.address().port}`,exec=promisify(execFile);let browser;const results=[];
try{
 await exec('npx',['--yes','agent-browser','--session','ic110','open',base]);await exec('npx',['--yes','agent-browser','--session','ic110','snapshot','-i']);assert.equal((await exec('npx',['--yes','agent-browser','--session','ic110','errors'])).stdout.trim(),'');await exec('npx',['--yes','agent-browser','--session','ic110','close']);
 browser=await chromium.launch();
 for(const width of [1280,390])for(const lang of ['en','zh'])for(const state of ['on','error','off','reconnecting','absent','drawer']){
  const data=bridgeAudioFixture(fixture,state==='drawer'?'on':state),context=await browser.newContext({viewport:{width,height:900},timezoneId:'UTC'}),page=await context.newPage(),errors=[],writes=[];
  page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(m.type()==='error')errors.push(m.text());});
  await page.route('**/api/**',async route=>{const req=route.request();if(req.method()!=='GET')writes.push(req.method());const u=new URL(req.url()),key=u.pathname.slice(5);await route.fulfill({json:key==='usage'?(u.searchParams.get('window')==='week'?data.week:data.today):data[key]});});
  await page.addInitScript(()=>{Date.now=()=>Date.parse('2026-09-06T12:00:00Z');});
  await page.goto(base+`/?lang=${lang}#token=fixture`);await page.locator('#friends').waitFor();await page.evaluate(()=>document.fonts.ready);
  if(state==='drawer'){await page.locator(`[data-key="${data.keys[0].id}"]`).click();const height=await page.locator('.drawer').evaluate(e=>e.scrollHeight);await page.setViewportSize({width,height:Math.max(900,height)});assert.ok((await page.locator('.drawer .nowline').innerText()).includes(lang==='en'?'12 s · 340 chars':'12 秒 · 340 字符'));}
  if(state==='on')assert.ok((await page.locator('.fact.wide .s').innerText()).includes('12:04'));
  assert.equal(await page.locator('.fact.wide').count(),state==='absent'?0:1);assert.equal(await page.locator('.fact.wide .bad').count(),state==='error'?1:0);
  const overlaps=await page.locator('.via-row td').evaluateAll(cells=>cells.some(cell=>{const range=document.createRange();range.selectNodeContents(cell);const box=cell.getBoundingClientRect();return [...range.getClientRects()].some(r=>r.left<box.left-1||r.right>box.right+1);}));assert.equal(overlaps,false,'via cell text crosses its column');
  const bounds=await page.evaluate(()=>({overflow:document.documentElement.scrollWidth-window.innerWidth,hash:location.hash,stored:window.localStorage.length+window.sessionStorage.length}));assert.deepEqual(bounds,{overflow:0,hash:'',stored:0});assert.deepEqual(errors,[]);assert.deepEqual(writes,[]);
  const name=`${state}-${width}-${lang}`;await page.screenshot({path:new URL(name+'.png',out).pathname,fullPage:state!=='drawer'});results.push(name+' PASS');await context.close();
 }
 console.log(results.join('\n'));console.log(`${results.length} passed / 0 failed / 0 skipped`);await writeFile(new URL('results.txt',out),results.join('\n')+'\n');
}finally{await browser?.close();await new Promise(r=>server.httpServer.close(r));}
