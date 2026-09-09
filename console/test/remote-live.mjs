// Opt-in 085 proof; every process and file belongs to a fresh isolated host.
import {chromium} from 'playwright';
import {createServer} from 'node:http';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {mkdtemp,mkdir,writeFile,readFile} from 'node:fs/promises';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import assert from 'node:assert/strict';
const binary=process.env.INFERCAT_PROOF_BINARY,adapter=process.env.INFERCAT_PROOF_TUNNEL;
if(!binary||!adapter)throw new Error('set INFERCAT_PROOF_BINARY and INFERCAT_PROOF_TUNNEL');
const dir=await mkdtemp(join(tmpdir(),'ic085-')),children=[],logs=new Map(),report=[];
const sleep=ms=>new Promise(r=>setTimeout(r,ms));const cli=async(...args)=>(await promisify(execFile)(binary,args)).stdout.trim();
function start(bin,args,name){const p=spawn(bin,args,{stdio:['ignore','pipe','pipe']});children.push(p);logs.set(name,'');for(const stream of [p.stdout,p.stderr])stream.on('data',b=>logs.set(name,logs.get(name)+b));return p;}
async function until(fn){for(let i=0;i<150;i++){try{const v=await fn();if(v)return v;}catch{}await sleep(200);}throw new Error('timed out');}
const engine=createServer(async(req,res)=>{for await(const _ of req){void _;}res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(req.url==='/props'?{total_slots:2,default_generation_settings:{n_ctx:4096}}:{data:[{id:'proof-model'}]}));});
await new Promise(r=>engine.listen(0,'127.0.0.1',r));let browser;
try{
 await mkdir(join(dir,'host'));start(binary,['serve','--data-dir',join(dir,'host'),'--upstream',`http://127.0.0.1:${engine.address().port}`,'--console','127.0.0.1:0','--name','085 isolated proof'],'host');
 const url=await until(()=>cli('console','--print','--data-dir',join(dir,'host')));
 const friend=JSON.parse(await cli('keys','add','proof-friend','--json','--data-dir',join(dir,'host')));
 browser=await chromium.launch();const page=await browser.newPage({viewport:{width:1280,height:1100}});const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto(url);await page.locator('#settings').waitFor();await page.evaluate(()=>document.fonts.ready);
 async function mutation(click){const answer=page.waitForResponse(r=>r.request().method()!=='GET'&&new URL(r.url()).pathname.startsWith('/api/'));await click();assert.equal((await answer).status(),200);await page.locator('[data-action="new"]:not(:disabled)').waitFor({state:'attached'});}
 await page.locator('#setting-name').fill('085 renamed live');await page.locator('#setting-slots').fill('4');await mutation(()=>page.locator('[data-form="settings"] button[type="submit"]').click());assert.ok((await page.locator('.head').innerText()).includes('085 renamed live'));assert.ok((await page.locator('#settings').innerText()).includes('running with 2'));
 await mutation(()=>page.locator('[data-action="remote-enable"]').click());const code=await page.locator('.once .code').innerText();const [,address,secret]=code.split('.');assert.equal(code.split('.')[0],'ia1');await mkdir('test/evidence',{recursive:true});await page.screenshot({path:'test/evidence/remote-live-code.png'});await page.locator('[data-action="done"]').click();
 start(adapter,[address],'tunnel');const local=await until(()=>logs.get('tunnel').match(/http:\/\/127\.0\.0\.1:\d+/)?.[0]);
 async function curl(label,path,method,bearer,body,want){
  const header=join(dir,'curl-header');await writeFile(header,'Authorization: Bearer '+bearer+'\n',{mode:0o600});const args=['-sS','--max-time','20','-X',method,'-H','@'+header,'-H','Content-Type: application/json','-w','\n%{http_code}',local+path];if(body)args.push('--data',JSON.stringify(body));
  const out=(await promisify(execFile)('curl',args)).stdout;const at=out.lastIndexOf('\n'),status=Number(out.slice(at+1)),payload=out.slice(0,at);assert.equal(status,want);report.push(`${label}: ${method} ${path} -> ${status}`);return payload;
 }
 await curl('admin code', '/console/', 'GET',secret,null,200);
 const remote=JSON.parse(await curl('remote limit edit','/console/api/keys/'+friend.key_id,'PATCH',secret,{rpm:7},200));assert.equal(remote.ok,true);
 await until(async()=>{const rows=await page.locator(`[data-key="${friend.key_id}"]`).innerText();return rows.includes('7 rpm');});
 await until(async()=>{return (await page.locator('.rstate').innerText()).includes('· in use');});await page.locator('#settings').screenshot({path:'test/evidence/remote-live-state.png'});
 await curl('admin is not a chat key','/v1/models','GET',secret,null,401);
 const me=JSON.parse(await curl('live name on friend poll','/me','GET',friend.invite.split('.')[2],null,200));assert.equal(me.host.name,'085 renamed live');
 await curl('remote console address refusal','/console/api/settings','PATCH',secret,{console:'off'},400);
 await mutation(()=>page.locator('[data-action="remote-rotate"]').click());const next=(await page.locator('.once .code').innerText()).split('.')[2];await curl('rotation rejects old code','/console/','GET',secret,null,401);await curl('rotation accepts new code','/console/','GET',next,null,200);await page.locator('[data-action="done"]').click();
 await page.locator('button[data-action="remote-confirm"]').click();await mutation(()=>page.locator('[data-action="remote-off"]').click());await curl('off hides route','/console/','GET',next,null,404);
 const token=(await readFile(join(dir,'host/admin.token'),'utf8')).trim();assert.ok(!logs.get('host').includes(token));assert.ok(!logs.get('host').includes(secret));assert.deepEqual(errors,[]);
 const usage=(await readFile(join(dir,'host/usage.jsonl'),'utf8')).trim().split('\n').map(JSON.parse).filter(e=>e.kind==='console');assert.ok(usage.some(e=>e.status===200)&&usage.some(e=>e.status===401));assert.ok(usage.every(e=>!e.prompt&&!e.completion));
 report.push('Local settings: live name, saved slots=4/running slots=2; API counts-only usage; no secrets in host log; browser errors=0.');await writeFile('test/evidence/remote-live.txt',report.join('\n')+'\n');console.log(report.join('\n'));
}finally{await browser?.close();for(const p of children.reverse()){if(p.exitCode===null){p.kill('SIGINT');await Promise.race([new Promise(r=>p.once('exit',r)),sleep(10000)]);if(p.exitCode===null)p.kill('SIGKILL');}}await new Promise(r=>engine.close(r));console.log('All isolated proof processes stopped.');}
