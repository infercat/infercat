// Opt-in: two fresh browser contexts, one fresh local host, the real WASM/relay transport.
import {chromium} from 'playwright';
import {createServer} from 'node:http';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {mkdtemp,mkdir,writeFile,readFile} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {tmpdir} from 'node:os';
import assert from 'node:assert/strict';
const binary=process.env.INFERCAT_PROOF_BINARY,web=process.env.INFERCAT_PROOF_WEB;
if(!binary||!web)throw new Error('set INFERCAT_PROOF_BINARY and INFERCAT_PROOF_WEB');
const dir=await mkdtemp(join(tmpdir(),'ic086-')),children=[],logs=[],report=[];const sleep=ms=>new Promise(r=>setTimeout(r,ms));
const cli=async(...args)=>(await promisify(execFile)(binary,args)).stdout.trim();
const engine=createServer(async(req,res)=>{for await(const _ of req){void _;}res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(req.url==='/props'?{total_slots:2,default_generation_settings:{n_ctx:4096}}:{data:[{id:'proof-model'}]}));});await new Promise(r=>engine.listen(0,'127.0.0.1',r));let browser;
try{
 await mkdir(join(dir,'host'));const host=spawn(binary,['serve','--data-dir',join(dir,'host'),'--upstream',`http://127.0.0.1:${engine.address().port}`,'--console','127.0.0.1:0','--web-url',web,'--name','086 own host'],{stdio:['ignore','pipe','pipe']});children.push(host);host.stdout.on('data',b=>logs.push(String(b)));host.stderr.on('data',b=>logs.push(String(b)));
 let url='';for(let i=0;i<150;i++){try{url=await cli('console','--print','--data-dir',join(dir,'host'));break;}catch{await sleep(200);}}assert.ok(url);
 const friend=JSON.parse(await cli('keys','add','proof-friend','--json','--data-dir',join(dir,'host')));
 browser=await chromium.launch();const localContext=await browser.newContext(),remoteContext=await browser.newContext(),local=await localContext.newPage(),remote=await remoteContext.newPage();const errors=[];remote.on('pageerror',e=>errors.push(e.message));remote.on('console',m=>{if(m.type()==='error')errors.push(m.text());});
 await local.goto(url);await local.locator('#friends').waitFor();
 async function localAction(action){const response=local.waitForResponse(r=>r.request().method()==='POST');await local.locator(`[data-action="${action}"]`).click();assert.equal((await response).status(),200);await local.locator('[data-action="new"]:not(:disabled)').waitFor({state:'attached'});}
 await localAction('remote-enable');const code=await local.locator('.once .code').innerText();await local.locator('[data-action="done"]').click();
 await remote.goto(web+'console#'+code);await remote.locator('#friends').waitFor({timeout:90000});await remote.evaluate(()=>document.fonts.ready);
 assert.ok((await remote.locator('.path').innerText()).includes('through the tunnel'));assert.equal(await remote.locator('#setting-console').count(),0);
 const requestsBefore=await remote.evaluate(()=>performance.getEntriesByType('resource').map(x=>x.name));assert.ok(requestsBefore.some(url=>/\/runtime\/[^/]+\/infercat\.wasm/.test(url)),'must use real WASM');
 const evidence=resolve(new URL('../../console/test/evidence/remote-web',import.meta.url).pathname);await mkdir(evidence,{recursive:true});await remote.screenshot({path:join(evidence,'live-connected.png'),fullPage:true});
 await remote.locator(`[data-key="${friend.key_id}"]`).click();await remote.locator('#limit-rpm').fill('9');await remote.locator('form[data-form="limits"] button[type="submit"]').click();await local.locator(`[data-key="${friend.key_id}"]`).filter({hasText:'9 rpm'}).waitFor();report.push('Browser via real WASM/relay: PATCH /console/api/keys/'+friend.key_id+' -> local row 9 rpm');await remote.locator('button[data-close]').click();
 await local.locator('.rstate').filter({hasText:'· in use'}).waitFor();
 await remote.locator('#setting-name').fill('086 renamed remotely');await remote.locator('[data-form="settings"] button[type="submit"]').click();await local.locator('.head').filter({hasText:'086 renamed remotely'}).waitFor();report.push('Remote settings PATCH -> live name on local console');
 // Capture public response shapes from the real 085-compatible host. This is a dev capture, not a release.
 const localToken=(await readFile(join(dir,'host/admin.token'),'utf8')).trim(),origin=new URL(url).origin,capture={};
 for(const [key,path]of [['status','status'],['keys','keys'],['engine','engine'],['settings','settings'],['today','usage?window=today'],['week','usage?window=week']]){const r=await fetch(origin+'/api/'+path,{headers:{Authorization:'Bearer '+localToken}});assert.equal(r.status,200);capture[key]=await r.json();}
 if(process.env.CAPTURE_CONSOLE_FIXTURE==='1')await writeFile(new URL('../src/fixtures/console/current-085-dev.json',import.meta.url),JSON.stringify(capture,null,2)+'\n');
 await localAction('remote-rotate');const next=await local.locator('.once .code').innerText();await remote.getByText('This admin code was refused. Open a fresh code from the host.').first().waitFor();report.push('Local rotation -> remote 401 refusal, old session stops reading');await remote.screenshot({path:join(evidence,'live-refused.png'),fullPage:true});await local.locator('[data-action="done"]').click();
 await remote.goto(web+'console#'+next);await remote.locator('#friends').waitFor({timeout:90000});
 await local.locator('button[data-action="remote-confirm"]').click();await localAction('remote-off');await remote.getByText(/Closed · observed at/).first().waitFor();report.push('Local off -> remote 404 CLOSED with observed time');await remote.screenshot({path:join(evidence,'live-closed.png'),fullPage:true});
 const state=await remote.evaluate(()=>({hash:location.hash,local:window.localStorage.length,session:window.sessionStorage.length}));assert.deepEqual(state,{hash:'',local:0,session:0});assert.deepEqual(errors,[]);assert.ok(!logs.join('').includes(localToken));
 report.push('Second browser context: empty fragment, localStorage=0, sessionStorage=0, page errors=0.');await writeFile(join(evidence,'live.txt'),report.join('\n')+'\n');console.log(report.join('\n'));
}finally{await browser?.close();for(const child of children){if(child.exitCode===null){child.kill('SIGINT');await Promise.race([new Promise(r=>child.once('exit',r)),sleep(10000)]);if(child.exitCode===null)child.kill('SIGKILL');}}await new Promise(r=>engine.close(r));console.log('Own isolated host, engine and browser contexts stopped.');}
