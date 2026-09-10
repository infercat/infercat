import { Buffer } from 'node:buffer';
// 144b: only the engineer-owned host and engines. Key stays in the private proof file/browser.
import { chromium } from 'playwright';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import assert from 'node:assert/strict';
const out='/private/tmp/infercat-144b-live';mkdirSync(out,{recursive:true});
const invite=JSON.parse(readFileSync('/private/tmp/infercat-144b-key.json','utf8')).invite;
const browser=await chromium.launch(), errors=[], network=[];
const context=await browser.newContext({viewport:{width:1280,height:900},acceptDownloads:true});
const page=await context.newPage();page.on('pageerror',(e)=>errors.push(String(e)));
page.on('request',(r)=>{const u=new URL(r.url());if(u.port==='49147') network.push({method:r.method(),path:u.pathname});});
const api=async(path)=>{const response=await fetch('http://127.0.0.1:49147'+path,{headers:{authorization:`Bearer ${invite.split('.').at(-1)}`}});assert.equal(response.status,200);return response.json();};
const capture=async(name)=>{await page.evaluate(()=>document.fonts.ready);if(page.viewportSize().width<=760) await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().right<=0);await page.screenshot({path:`${out}/${name}.png`});};
try {
 await page.goto(`http://127.0.0.1:49148/?direct&invite=${encodeURIComponent(invite)}&autoconnect`);
 await page.locator('.make').waitFor({timeout:60000});await page.waitForFunction(()=>!document.querySelector('.composer textarea').disabled);
 const before=await api('/me');assert.equal(before.host.images.model,'FLUX.2-klein-4B-Q8_0');
 await page.locator('.make').click();await page.locator('.composer textarea').fill('A red paper cat on a white desk, minimalist poster.\n\nA blue paper cat beside a window, minimalist poster.');await page.locator('.composer .primary').click();
 await page.locator('.row.run[data-run-state="running"]').waitFor({timeout:60000});await page.locator('.row.run[data-run-state="queued"]').waitFor();
 await page.locator('.row.run[data-run-state="queued"] .spoken button').click();await page.locator('.row.run[data-run-state="cancelled"]').waitFor();
 await page.locator('.row.run[data-run-state="running"] .spoken button').click();await page.getByText(/cancelling · after this image/).waitFor();await capture('cancelling-1280-en');
 await page.waitForFunction(()=>document.querySelector('.make').getAttribute('aria-pressed')==='false');
 await page.locator('.composer textarea').fill('Reply with one short sentence about colors.');await page.locator('.composer .primary').click();
 await page.locator('.row.run[data-run-state="done"] .shot img').waitFor({timeout:180000});await capture('done-1280-en');
 await page.waitForFunction(()=>document.querySelectorAll('.row.assistant:not(.run) .meta').length>0,{},{timeout:120000});
 const listed=(await api('/v1/images/jobs')).jobs, done=listed.find((j)=>j.state==='done'), cancelled=listed.find((j)=>j.state==='cancelled');assert.ok(done&&cancelled);assert.ok(done.cancel_requested);assert.equal(done.batch.id,cancelled.batch.id);
 await page.locator('.row.run .shot').click();await page.locator('.sheet.image').waitFor();const download=page.waitForEvent('download');await page.locator('.sheet.image .sheet-actions button').click();const saved=await download;await saved.saveAs(`${out}/saved.png`);assert.ok(readFileSync(`${out}/saved.png`).subarray(0,8).equals(Buffer.from([137,80,78,71,13,10,26,10])));await page.keyboard.press('Escape');
 await page.locator('.conv.harvest button').click();await page.locator(`[data-image-job="${done.id}"] .shot img`).waitFor();await capture('images-1280-en');
 await page.setViewportSize({width:390,height:844});await capture('images-390-en');
 const discardedResponse = page.waitForResponse((r) => r.request().method() === 'DELETE' && r.url().endsWith('/v1/images/outputs/'+done.id)); await page.locator(`[data-image-job="${done.id}"] .actions button`).last().click(); await discardedResponse;await page.getByText('no longer on Image jobs app proof').first().waitFor();
 assert.ok((await api('/v1/images/jobs')).jobs.find((j)=>j.id===done.id).output.gone);
 const after=await api('/me');assert.equal(after.usage.today_images,(before.usage.today_images??0)+1);
 assert.equal(network.filter((r)=>r.method==='POST'&&r.path==='/v1/images/jobs').length,1);assert.equal(network.filter((r)=>r.method==='POST'&&r.path==='/v1/chat/completions').length,1);assert.deepEqual(errors,[]);
 const result={host:'127.0.0.1:49147',model:before.host.images.model,batch:2,queued_cancel:cancelled.state,deferred_cancel:done.state,output_bytes:done.output.bytes,saved_bytes:readFileSync(`${out}/saved.png`).length,discarded:true,images_charged:after.usage.today_images-(before.usage.today_images??0),chat_posted_while_image_live:true,errors,requests:network};writeFileSync(`${out}/result.json`,JSON.stringify(result,null,2));console.log(JSON.stringify(result));
} finally { await context.close();await browser.close(); }
