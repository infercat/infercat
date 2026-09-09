// Opt-in proof against only a new isolated host, engine and browser profile.
import {chromium} from 'playwright';
import {createServer} from 'node:http';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {mkdtemp,mkdir,writeFile,readFile} from 'node:fs/promises';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import assert from 'node:assert/strict';
const binary=process.env.INFERCAT_PROOF_BINARY;if(!binary)throw new Error('set INFERCAT_PROOF_BINARY');
const dir=await mkdtemp(join(tmpdir(),'ic090-')),children=[],logs=[],report=[];
const sleep=ms=>new Promise(r=>setTimeout(r,ms)),exec=promisify(execFile);
const cli=async(...args)=>(await exec(binary,['--data-dir',dir,...args])).stdout.trim();
const engine=createServer(async(req,res)=>{for await(const _ of req){void _;}res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(req.url==='/props'?{total_slots:2,default_generation_settings:{n_ctx:4096}}:{data:[{id:'proof-model'}]}));});await new Promise(r=>engine.listen(0,'127.0.0.1',r));let browser;
async function start(consoleAddress='127.0.0.1:0'){
 const p=spawn(binary,['serve','--data-dir',dir,'--upstream',`http://127.0.0.1:${engine.address().port}`,'--console',consoleAddress],{stdio:['ignore','pipe','pipe']});children.push(p);p.stdout.on('data',b=>logs.push(String(b)));p.stderr.on('data',b=>logs.push(String(b)));
 for(let i=0;i<150;i++){try{await cli('status');return p;}catch{await sleep(200);}}throw new Error('own host did not start');
}
async function stop(p){if(p.exitCode===null){p.kill('SIGINT');await Promise.race([new Promise(r=>p.once('exit',r)),sleep(10000)]);if(p.exitCode===null)p.kill('SIGKILL');}}
try{
 await writeFile(join(dir,'admin.json'),'damaged',{mode:0o600});let host=await start();
 assert.equal(await readFile(join(dir,'admin.json'),'utf8'),'damaged');const status=JSON.parse(await cli('remote','status','--json'));assert.equal(status.enabled,false);assert.equal(status.warning_file,join(dir,'admin.json'));
 const url=await cli('console','--print'),origin=new URL(url).origin;
 await exec('npx',['--yes','agent-browser','--session','ic090','open',origin]);await exec('npx',['--yes','agent-browser','--session','ic090','snapshot','-i']);assert.equal((await exec('npx',['--yes','agent-browser','--session','ic090','errors'])).stdout.trim(),'');await exec('npx',['--yes','agent-browser','--session','ic090','close']);
 browser=await chromium.launch();const context=await browser.newContext(),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(m.type()==='error')errors.push(m.text());});
 await page.goto(url);await page.locator('.remote [role="status"]').waitFor();const out=new URL('./evidence/recovery/',import.meta.url);await mkdir(out,{recursive:true});
 for(const lang of ['en','zh']){await page.locator(`[data-lang="${lang}"]`).click();assert.ok((await page.locator('.remote [role="status"]').innerText()).includes(join(dir,'admin.json')));await page.locator('#settings').scrollIntoViewIfNeeded();await page.screenshot({path:new URL(`warning-${lang}.png`,out).pathname,fullPage:true});}
 report.push('Damaged admin.json: real host starts, file unchanged, remote off; banner/EN/ZH Settings name the file and repair.');
 const first=JSON.parse(await cli('remote','on','--json'));assert.ok(first.invite.startsWith('ia1.'));await page.locator('.remote [role="status"]').waitFor({state:'detached'});assert.equal(JSON.parse(await cli('remote','status','--json')).enabled,true);
 assert.deepEqual(errors,[]);await page.close();const record=await readFile(join(dir,'admin.json'),'utf8');assert.ok(!record.includes(first.invite.split('.').at(-1)));await stop(host);host=await start();assert.equal(JSON.parse(await cli('remote','status','--json')).enabled,true);
 const next=JSON.parse(await cli('remote','rotate','--json'));assert.notEqual(first.invite,next.invite);assert.notEqual(record,await readFile(join(dir,'admin.json'),'utf8'));
 await cli('remote','off');assert.equal(JSON.parse(await cli('remote','status','--json')).enabled,false);
 await stop(host);host=await start('off');for(const action of ['on','off','rotate','status'])await assert.rejects(cli('remote',action),/loopback console listener/);
 assert.equal(logs.join('').split('Remote access is off:').length-1,1);for(const code of [first.invite,next.invite])assert.ok(!logs.join('').includes(code.split('.').at(-1)));assert.deepEqual(errors,[]);
 report.push('CLI on repairs, warning clears; enabled state survives restart; rotate replaces hash; off disables; every verb refuses with listener off.');report.push('No admin secret in host logs; page errors=0; console errors=0.');console.log(report.join('\n'));await writeFile(new URL('proof.txt',out),report.join('\n')+'\n');
}finally{await browser?.close();for(const child of children)await stop(child);await new Promise(r=>engine.close(r));console.log('Own host, engine and browser stopped.');}
