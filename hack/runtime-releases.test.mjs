import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, mkdir, readFile, writeFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { stageRuntimes, verifyRuntimes } from './runtime-releases.mjs';
const names=['infercat.wasm.gz','wasm_exec.js'];
const bytes=(v,f)=>Buffer.from(`${v}/${f}`);
const manifest=Object.fromEntries(['0.1.3','0.1.4'].map(v=>[v,Object.fromEntries(names.map(f=>[f,createHash('sha256').update(bytes(v,f)).digest('hex')]))]));
const fetcher=async url=>{const parts=url.split('/');return new Response(bytes(parts.at(-2).replace(/^v/,''),parts.at(-1)),{headers:{'access-control-allow-origin':'*'}});};
async function directory(t){const dir=await mkdtemp(join(tmpdir(),'runtime-pairs-'));t.after(()=>rm(dir,{recursive:true,force:true}));for(const f of names)await writeFile(join(dir,f),'local build');return dir;}
test('stages both archived pairs on a fresh deploy and keeps them when a new version is added',async t=>{
 const dir=await directory(t);await stageRuntimes(manifest,dir,'0.1.5-dev',fetcher);
 for(const v of Object.keys(manifest))for(const f of names)assert.deepEqual(await readFile(join(dir,'v',v,f)),bytes(v,f));
 assert.equal(await readFile(join(dir,'v','0.1.5-dev',names[0]),'utf8'),'local build');
 const next={...manifest,'0.1.5':manifest['0.1.4']};await stageRuntimes(next,dir,'0.1.5',async url=>fetcher(url.replace('v0.1.5/','v0.1.4/')));
 assert.equal(await readFile(join(dir,'v','0.1.3',names[0]),'utf8'),'0.1.3/infercat.wasm.gz');
});
test('a stable deployed version uses release bytes rather than the local rebuild',async t=>{
 const dir=await directory(t);await stageRuntimes(manifest,dir,'0.1.4',fetcher);assert.deepEqual(await readFile(join(dir,'v','0.1.4',names[0])),bytes('0.1.4',names[0]));
});
for(const [reason,download] of [['missing',async()=>new Response('',{status:404})],['mismatch',async()=>new Response('wrong')]])test(`${reason} asset stops staging without changing an existing pair`,async t=>{
 const dir=await directory(t);await mkdir(join(dir,'v','0.1.3'),{recursive:true});await writeFile(join(dir,'v','0.1.3',names[0]),'kept');
 await assert.rejects(stageRuntimes(manifest,dir,'0.1.5-dev',download));assert.equal(await readFile(join(dir,'v','0.1.3',names[0]),'utf8'),'kept');assert.ok(!(await readdir(dir)).some(n=>n.startsWith('.runtimes-')));
});
test('an unlisted stable version refuses before fetching',async t=>{const dir=await directory(t);await assert.rejects(stageRuntimes(manifest,dir,'0.1.5',()=>assert.fail('fetched')),/missing from runtime manifest/);});
test('invalid manifest paths and incomplete pairs refuse',async t=>{const dir=await directory(t);for(const m of [{}, {'../escape':manifest['0.1.3']},{'0.1.3':{'wasm_exec.js':'x'}}])await assert.rejects(stageRuntimes(m,dir,'0.1.5-dev',fetcher));});
test('live verification requires actual bytes and CORS, not a 200 HTML fallback',async()=>{
 await verifyRuntimes(manifest,'https://site.example',fetcher);
 await assert.rejects(verifyRuntimes(manifest,'https://site.example',async()=>new Response('fallback',{headers:{'access-control-allow-origin':'*'}})),/SHA-256/);
 await assert.rejects(verifyRuntimes(manifest,'https://site.example',async()=>new Response('x')),/CORS/);
});
