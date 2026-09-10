import test from 'node:test';
import assert from 'node:assert/strict';
import { Readable } from 'node:stream';
import { frames, MAX_FRAME, rewrite, wording } from './adapter.mjs';
import {modelRequest,modelChunks} from './wire.mjs';

const read = async chunks => Array.fromAsync(frames(Readable.from(chunks)));
test('private frames handle split input and multiple messages in one chunk', async () => {
  assert.deepEqual(await read(['{"type":', '"cancel"}\n{"id":1}\n']), [{type:'cancel'}, {id:1}]);
});

test('native mapping preserves original conversation, images and chat settings',()=>{
  const messages=[{role:'system',content:'Be brief.'},{role:'assistant',content:'Prior reply'},{role:'user',content:[{type:'text',text:'Inspect'},{type:'image_url',image_url:{url:'data:image/png;base64,AAAA'}}]}];
  const run={promptID:'p',input:{model:'m',messages,temperature:0.4,chat_template_kwargs:{enable_thinking:false}}};
  const result=modelRequest(run,{model:'m',messages:[{id:'s',role:'system',content:[{type:'text',text:'Harness policy'}]},{id:'p',role:'user',content:[{type:'text',text:'Inspect'}]}],tools:[]});
  assert.deepEqual(result.messages.slice(1),messages);assert.equal(result.temperature,0.4);assert.deepEqual(result.chat_template_kwargs,{enable_thinking:false});
});
test('stream translation joins split tool arguments and keeps usage before final finish',async()=>{
  const events=[{choices:[{delta:{reasoning_content:'think'}}]},{choices:[{delta:{tool_calls:[{index:0,id:'c',function:{name:'write',arguments:'{"a":'}}]}}]},{choices:[{delta:{tool_calls:[{index:0,function:{arguments:'1}'}}]},finish_reason:'tool_calls'}]},{choices:[],usage:{prompt_tokens:3,completion_tokens:5,total_tokens:8}}];
  const wire=events.map(e=>'data: '+JSON.stringify(e)+'\n\n').join('');
  const chunks=await Array.fromAsync(modelChunks(Readable.from([wire.slice(0,11),wire.slice(11)])));
  assert.equal(chunks.at(-1).type,'finish');assert.equal(chunks.at(-1).reason.kind,'tool-calls');
  assert.deepEqual(chunks.find(c=>c.type==='block-end'&&c.block.type==='tool-call').block,{type:'tool-call',id:'c',name:'write',arguments:'{"a":1}'});
  assert.equal(chunks.find(c=>c.type==='usage').usage.totalTokens,8);
  await assert.rejects(Array.fromAsync(modelChunks(Readable.from(['data: {"choices":[]}\n\n']))),/without finish/);
});
test('unterminated frames are bounded before JSON parsing', async () => {
  await assert.rejects(read([' '.repeat(MAX_FRAME / 2), ' '.repeat(MAX_FRAME / 2)]), /too large/);
});
test('incomplete and malformed frames end the generation', async () => {
  await assert.rejects(read(['{"id":1}']), /Incomplete/);
  await assert.rejects(read(['not-json\n']), SyntaxError);
});
test('129 wording changes descriptions only and leaves the native schema intact', () => {
  const input = {tools:[{name:'write',description:'Original',parameters:{type:'object',required:['file_path','content'],properties:{
    sandbox_permissions:{type:'string',enum:['workspace-write'],description:'old'},justification:{type:'string',description:'old'},
  }}}]};
  const saved = structuredClone(input), result = rewrite(input), write = result.tools[0];
  assert.deepEqual(input, saved);
  assert.equal(write.description, 'Original\n\n' + wording.write);
  for (const key of ['sandbox_permissions','justification']) assert.equal(write.parameters.properties[key].description, wording[key]);
  assert.deepEqual(write.parameters.required, ['file_path','content']);
  assert.deepEqual(write.parameters.properties.sandbox_permissions.enum, ['workspace-write']);
  assert.throws(() => rewrite({tools:[]}), /Pinned/);
});


test('approval shapes preserve verbatim text and fail closed',async()=>{
 const {approvalText}=await import('./adapter.mjs');
 const req={agent:{session:{id:'r_one'}},toolName:'write',reason:'Allow this exact path?',signal:new AbortController().signal};
 assert.equal(approvalText(req),req.reason);
 assert.equal(approvalText({...req,reason:undefined}),'write');
 for(const bad of [{...req,reason:{}},{...req,reason:''},{...req,signal:{}},{...req,agent:{}},null])assert.throws(()=>approvalText(bad));
});

test('helper purposes use native options without friend reasoning or temperature',()=>{
 const run={promptID:'p',input:{model:'m',messages:[{role:'user',content:'friend'}],temperature:1.7,chat_template_kwargs:{enable_thinking:true}}};
 for(const purpose of ['compaction','session-title']){
 const r=modelRequest(run,{purpose,model:'m',maxTokens:64,messages:[{id:'p',role:'user',content:[{type:'text',text:'helper task'}]}]});
 assert.equal(r.temperature,undefined);assert.equal(r.max_tokens,64);assert.equal(r.chat_template_kwargs.enable_thinking,false);assert.equal(r.messages[0].content,'helper task');
 }
});
test('stream identity uses first nonempty value, only arguments concatenate',async()=>{
 const calls=[{index:0,function:{arguments:'{' }},{index:0,id:'a',type:'function',function:{name:'write',arguments:'"x":'}},{index:0,id:'a',type:'function',function:{name:'write',arguments:'1}'}}];
 const wire=calls.map(c=>'data: '+JSON.stringify({choices:[{delta:{tool_calls:[c]}}]})+'\n\n').join('')+'data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}\n\n';
 const chunks=await Array.fromAsync(modelChunks(Readable.from([wire])));const b=chunks.find(c=>c.type==='block-end').block;
 assert.equal(b.id,'a');assert.equal(b.name,'write');assert.equal(b.arguments,'{"x":1}');assert.deepEqual(chunks.filter(c=>c.type==='tool-call-delta').map(c=>c.id),['','a','a']);
});
