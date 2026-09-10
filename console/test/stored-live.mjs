// Owned host only; credentials are read from private proof files and never printed.
import { chromium } from 'playwright';
import { readFile, stat, mkdir, writeFile } from 'node:fs/promises';
import assert from 'node:assert/strict';
const prefix=process.env.PROOF_PREFIX||'/private/tmp/infercat-158',dir=prefix+'-proof-host', out=prefix+'-live';await mkdir(out,{recursive:true});
const a=JSON.parse(await readFile(prefix+'-key-a.json','utf8')), b=JSON.parse(await readFile(prefix+'-key-b.json','utf8')), token=(await readFile(dir+'/admin.token','utf8')).trim();
async function request(path,key,data){const r=await fetch('http://127.0.0.1:49159'+path,{method:data?'POST':'GET',headers:{Authorization:'Bearer '+key.invite.split('.').at(-1),'Content-Type':'application/json'},body:data?JSON.stringify(data):undefined});assert.ok(r.ok,`friend ${r.status}`);return r.json();}
async function stored(key){const r=await fetch('http://127.0.0.1:49158/api/stored?key_id='+key.key_id,{headers:{Authorization:'Bearer '+token}});assert.equal(r.status,200);return r.json();}
const made=(await request('/v1/images/jobs',a,{prompts:['A red paper kite on a white table.','A blue paper kite on a white table.','A yellow paper kite on a white table.']})).jobs;
assert.equal(made.length,3);
const other=(await request('/v1/images/jobs',b,{prompts:['A green paper kite on a white table.']})).jobs[0];
let before;
for(let n=0;n<180;n++){before=await stored(a);if(before.terminal>0)break;await new Promise(r=>setTimeout(r,500));}
assert.ok(before.terminal>0&&before.terminal<before.total);assert.equal(before.bytes,(await stat(`${dir}/runs/${a.key_id}/state.json`)).size);assert.ok(!JSON.stringify(before).includes('paper kite'));
const browser=await chromium.launch(),context=await browser.newContext({viewport:{width:1280,height:900}}),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(String(e)));
try{
 await page.goto(`http://127.0.0.1:49158/?lang=en#token=${encodeURIComponent(token)}`);await page.locator(`[data-key="${a.key_id}"]`).click();await page.locator('#stored-details').waitFor();await page.locator('#stored-details summary').click();await page.locator('[data-action="stored-confirm"]').click();await page.locator('#stored-details').scrollIntoViewIfNeeded();await page.screenshot({path:out+'/before-clear-1280-en.png'});
 const response=page.waitForResponse(r=>r.request().method()==='DELETE'&&r.url().includes('/api/stored?'));await page.locator('[data-action="stored-clear"]').click();assert.equal((await response).status(),200);
 await page.waitForFunction(()=>document.querySelector('[data-action="stored-confirm"]')?.disabled);
 const after=await stored(a);assert.ok(after.total>0&&after.total<before.total);assert.equal(after.terminal,0);assert.equal(after.images,0);assert.equal((await stored(b)).total,1);await page.waitForFunction(n=>document.querySelector('#stored-details summary')?.textContent.includes(n+' runs'),after.total);
 for(const row of before.runs.filter(r=>['done','failed','cancelled'].includes(r.state))){await assert.rejects(stat(`${dir}/runs/${a.key_id}/images/${row.id}`),e=>e.code==='ENOENT');}
 assert.ok((await request('/v1/runs/'+other.id,b)).id===other.id);assert.deepEqual(errors,[]);
 await page.setViewportSize({width:390,height:900});await page.locator('#stored-details').scrollIntoViewIfNeeded();await page.screenshot({path:out+'/after-clear-390-en.png'});
 const result={before,after,other_key_preserved:true,live_runs_preserved:after.total,terminal_image_files_removed:true,metadata_only:true,errors};await writeFile(out+'/result.json',JSON.stringify(result,null,2));console.log(JSON.stringify({before_runs:before.total,cleared_runs:before.total-after.total,live_runs_preserved:after.total,other_key_preserved:true,images_removed:before.images,state_bytes_before:before.bytes,state_bytes_after:after.bytes,errors}));
}finally{await context.close();await browser.close();}
