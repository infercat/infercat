// Local diagnostic page plus the immutable 101 controller, without copying it into the tree.
import { readFileSync,writeFileSync,mkdirSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { resolve } from 'node:path';
import { createServer, transformWithEsbuild } from 'vite';
const root=resolve(import.meta.dirname,'..');
const cache='/tmp/infercat-102-baseline.js';
if(process.argv.includes('--prepare')) {
 const source=execFileSync('git',['show','243d92e:web/src/voice.ts'],{cwd:resolve(root,'..'),encoding:'utf8'});
 const before=await transformWithEsbuild(source.replace("from './api'", "from '/src/api.ts'"),'voice.before.ts',{loader:'ts'});
 writeFileSync(cache,before.code);process.exit(0);
}
const before=readFileSync(cache,'utf8');
const server=await createServer({root,server:{host:'127.0.0.1',port:49102,strictPort:true},plugins:[{
 name:'mic-baseline',configureServer(server){
 server.middlewares.use('/__save-blob',async(request,response)=>{
  if(request.method!=='POST'||request.headers.origin!=='http://127.0.0.1:49102'){response.statusCode=403;response.end();return;}
  const chunks=[];let size=0;
  for await(const chunk of request){size+=chunk.length;if(size>25*1024*1024){response.statusCode=413;response.end();return;}chunks.push(chunk);}
  const directory='/tmp/infercat-102-blobs';mkdirSync(directory,{recursive:true,mode:0o700});
  const path=resolve(directory,`safari-${randomUUID()}.${request.headers['content-type']?.includes('mp4')?'m4a':'webm'}`);
  writeFileSync(path,Buffer.concat(chunks),{mode:0o600});response.setHeader('Content-Type','application/json');response.end(JSON.stringify({savedPath:path}));
 });
 server.middlewares.use('/src/voice.before.ts',(_request,response)=>{response.setHeader('Content-Type','application/javascript');response.end(before);});}
}]});
await server.listen();server.printUrls();
