// Opt-in proof: only an isolated host/data directory, never an existing installation.
import { chromium } from 'playwright';
import { createServer } from 'node:http';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import assert from 'node:assert/strict';
const binary=process.env.INFERCAT_PROOF_BINARY;
if(!binary) throw new Error('set INFERCAT_PROOF_BINARY to the built ticket binary');
const dir=await mkdtemp(join(tmpdir(),'ic075-')), processes=[], logs=new Map(), report=[];
const cli=async(...args)=>(await promisify(execFile)(binary,args)).stdout.trim();
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const engine=createServer(async(req,res)=>{
 for await(const _ of req) { void _; }
 const value=req.url==='/props'?{total_slots:2,default_generation_settings:{n_ctx:4096}}:req.url==='/tokenize'?{tokens:[1,2,3]}:req.method==='GET'?{data:[{id:'proof-model'}]}:{id:'proof',choices:[{message:{role:'assistant',content:'proof ok'},finish_reason:'stop'}],usage:{prompt_tokens:3,completion_tokens:2,total_tokens:5}};
 res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(value));
});
await new Promise(resolve=>engine.listen(0,'127.0.0.1',resolve));
function start(args,name) {
 const child=spawn(binary,args,{stdio:['ignore','pipe','pipe']});processes.push(child);logs.set(name,'');
 for(const stream of [child.stdout,child.stderr])stream.on('data',b=>logs.set(name,logs.get(name)+b));return child;
}
async function until(fn) { for(let i=0;i<150;i++){try{const v=await fn();if(v)return v;}catch{}await sleep(200);}throw new Error('timed out'); }
let browser;
try {
 await mkdir(join(dir,'host'));start(['serve','--data-dir',join(dir,'host'),'--upstream',`http://127.0.0.1:${engine.address().port}`,'--console','127.0.0.1:0','--name','075 isolated proof'],'host');
 const url=await until(()=>cli('console','--print','--data-dir',join(dir,'host')));
 browser=await chromium.launch();const page=await browser.newPage({viewport:{width:1280,height:1000}});
 const errors=[],writes=[];page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(new URL(r.url()).pathname.startsWith('/api/')&&r.method()!=='GET')writes.push(r.method()+' '+new URL(r.url()).pathname);});
 await page.goto(url);await page.locator('#friends').waitFor();await page.evaluate(()=>document.fonts.ready);
 await page.locator('[data-action="new"]').click();await page.locator('#invite-name').fill('proof-friend');
 async function mutation(click) {const answer=page.waitForResponse(r=>r.request().method()!=='GET'&&new URL(r.url()).pathname.startsWith('/api/'));await click();assert.equal((await answer).status(),200);await page.locator('[data-action="new"]:not(:disabled)').waitFor({state:'attached'});}
 await mutation(()=>page.locator('form button[type="submit"]').click());
 const invite=await page.locator('.once .code').innerText();assert.ok(invite.startsWith('ic1.'));
 const id=(await page.locator('.drawer .meta').innerText()).split(' ')[0];
 await mkdir('test/evidence',{recursive:true});await page.screenshot({path:'test/evidence/live-mint.png'});
 async function connect(code,name) {
  const listener=createServer();await new Promise(resolve=>listener.listen(0,'127.0.0.1',resolve));const port=listener.address().port;await new Promise(resolve=>listener.close(resolve));
  await mkdir(join(dir,name));const child=start(['connect',code,'--listen',`127.0.0.1:${port}`,'--data-dir',join(dir,name)],name);
  await until(async()=>{if(child.exitCode!==null)throw new Error('connect exited');const r=await fetch(`http://127.0.0.1:${port}/v1/models`);return r.ok;});return port;
 }
 const oldPort=await connect(invite,'friend-old');
 async function answer(label,port,status) {
  const response=await fetch(`http://127.0.0.1:${port}/v1/chat/completions`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({model:'proof-model',messages:[{role:'user',content:'proof'}],max_tokens:8})});
  const body=await response.json();assert.equal(response.status,status);if(status===200)assert.equal(body.choices[0].message.content,'proof ok');
  report.push(`${label}: POST /v1/chat/completions -> ${response.status} ${status===200?'proof ok':body.error?.code||JSON.stringify(body)}`);
 }
 await answer('UI mint → real connect',oldPort,200);await page.locator('[data-action="done"]').click();
 for(const [verb,status] of [['pause',403],['resume',200]]){await mutation(()=>page.locator(`[data-action="${verb}"]`).click());await answer('UI '+verb,oldPort,status);}
 await mutation(()=>page.locator('[data-action="rotate"]').click());const rotated=await page.locator('.once .code').innerText();assert.notEqual(rotated,invite);
 await answer('UI rotate → old code',oldPort,401);const newPort=await connect(rotated,'friend-new');await answer('UI rotate → new code',newPort,200);
 await page.locator('[data-action="done"]').click();await page.locator('[data-action="confirm"]').click();assert.ok((await page.locator('.confirm').innerText()).includes('Revoking is permanent'));
 await mutation(()=>page.locator('[data-action="revoke"]').click());await answer('UI revoke',newPort,403);
 assert.equal(await page.locator('.drawer [data-action="resume"]').count(),0);
 await page.locator('button[data-close]').click();await page.locator('details summary').click();await page.locator(`[data-key="${id}"]`).click();assert.equal(await page.locator('.once').count(),0);
 await page.screenshot({path:'test/evidence/live-revoked.png'});
 const state=await page.evaluate(()=>({hash:location.hash,local:localStorage.length,session:sessionStorage.length}));assert.deepEqual(state,{hash:'',local:0,session:0});assert.deepEqual(errors,[]);
 assert.equal(writes.length,5);report.push(`Browser: ${writes.join('; ')}; storage=0; fragment empty; errors=0`);
 const hostLog=logs.get('host');const token=(await readFile(join(dir,'host','admin.token'),'utf8')).trim();assert.ok(!hostLog.includes(token));
 report.push('Own isolated host only; no admin token in startup log.');
 await writeFile('test/evidence/actions-live.txt',report.join('\n')+'\n');console.log(report.join('\n'));
} finally {
 await browser?.close();for(const child of processes.reverse()){if(child.exitCode===null){child.kill('SIGINT');await Promise.race([new Promise(resolve=>child.once('exit',resolve)),sleep(10000)]);if(child.exitCode===null)child.kill('SIGKILL');}}
 await new Promise(resolve=>engine.close(resolve));console.log('Isolated host, friend connections and local engine stopped.');
}
