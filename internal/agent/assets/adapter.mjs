// Product-owned plugin; the pinned harness owns agent execution and sandbox policy.
import { createRequire } from 'node:module';
import {readFileSync,closeSync} from 'node:fs';
import { pathToFileURL } from 'node:url';
import { createServer } from 'node:http';
import { randomUUID } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';
import { modelRequest, modelChunks } from './wire.mjs';

export const name = 'infercat-agent';
export const inject = ['agents', 'sessions', 'llm', 'systemPrompt', 'web'];
export const MAX_FRAME = 2 * 1024 * 1024;
export const wording = {
  write: 'Normal workspace writes need only file_path and content. If the workspace is already writable, omit both sandbox_permissions and justification.',
  sandbox_permissions: 'Optional request for a strictly wider mode after a denied operation. Omit this field for ordinary writes in an already-writable workspace. This is not the current-mode selector. Never request the mode already in effect.',
  justification: 'Only include with a valid request for a strictly wider sandbox mode; otherwise omit. Ordinary workspace writes do not need a justification.',
};
const structure = value => Array.isArray(value) ? value.map(structure) : value && typeof value === 'object'
  ? Object.fromEntries(Object.entries(value).filter(([key]) => key !== 'description').map(([key, v]) => [key, structure(v)])) : value;
export function rewrite(assembly) {
  const updated = structuredClone(assembly), write = updated.tools.find(tool => tool.name === 'write');
  if (!write?.parameters?.properties?.sandbox_permissions || !write.parameters.properties.justification) throw Error('Pinned write schema not found');
  write.description += '\n\n' + wording.write;
  for (const key of ['sandbox_permissions', 'justification']) write.parameters.properties[key].description = wording[key];
  if (!isDeepStrictEqual(structure(assembly), structure(updated))) throw Error('Tool schema semantics changed');
  return updated;
}

// Byte-count before parsing, including a peer that never supplies a newline.
export async function* frames(input) {
  let pending = Buffer.alloc(0);
  for await (const chunk of input) {
    let remaining = Buffer.from(chunk);
    while (remaining.length) {
      const end = remaining.indexOf(10), part = end < 0 ? remaining : remaining.subarray(0, end);
      if (pending.length + part.length >= MAX_FRAME) throw Error('Agent frame too large');
      pending = Buffer.concat([pending, part]);
      if (end < 0) break;
      yield JSON.parse(pending.toString('utf8'));
      pending = Buffer.alloc(0);
      remaining = remaining.subarray(end + 1);
    }
  }
  if (pending.length) throw Error('Incomplete agent frame');
}

export function approvalText(req) {
  if(typeof req?.agent?.session?.id!=='string'||typeof req.toolName!=='string'||!req.toolName.trim())throw Error('Unsupported native approval request');
  const text=req.reason===undefined?req.toolName:req.reason;
  if(typeof text!=='string'||!text.trim()||Buffer.byteLength(text)>1024*1024||(req.signal!==undefined&&!(req.signal instanceof AbortSignal)))throw Error('Unsupported native approval request');
  return text;
}

export function apply(ctx) {
  const require = createRequire(process.env.INFERCAT_AGENT_RUNTIME + '/package.json');
  const load = name => import(pathToFileURL(require.resolve(name)).href);
  void (async () => {
    const [{ installModelSelection }, { LlmAdapter, createUserMessage }, {apply: exaApply}] = await Promise.all([
      load('@deepseek-ai/dsh-agent'), load('@deepseek-ai/dsh-llm'), load('@deepseek-ai/dsh-web-search-exa'),
    ]);
    await ctx.get('loader')?.await();
    const searchKey=process.env.INFERCAT_EXA_FD==='4'?readFileSync(4,'utf8'):'';
    if(process.env.INFERCAT_EXA_FD==='4')closeSync(4);
    exaApply(ctx,{apiKey:searchKey,numResults:3});
    const runs = new Map(), calls = new Map();
    const send = message => {
      const raw = JSON.stringify(message) + '\n';
      if (Buffer.byteLength(raw) > MAX_FRAME || process.stdout.writableLength > MAX_FRAME) process.exit(1);
      process.stdout.write(raw);
    };
    function ask(run,type,payload={},signal) {
      const id=randomUUID();let resolve,reject;
      const promise=new Promise((yes,no)=>{resolve=yes;reject=no});
      const abort=()=>reject(Error('Run cancelled'));
      calls.set(id,{run,resolve,reject});
      signal?.addEventListener('abort',abort,{once:true});
      send({type,id,run_id:run.id,...payload});
      return promise.finally(()=>{calls.delete(id);signal?.removeEventListener('abort',abort);});
    }
    async function checkpoint(run,stage,signal) {
      if(signal?.aborted)throw Error('Run cancelled');
      const events=run.pending;run.pending=[];run.pendingBytes=0;
      await ask(run,'checkpoint',{stage,events},signal);
    }
    class GatewayAdapter extends LlmAdapter {
      async *stream(options) {
        const run=runs.get(options.sessionId);
        if(!run||run.cancelled)throw Error('No live Infercat run');
        const previous=run.modelTail??Promise.resolve();let unlock;run.modelTail=new Promise(resolve=>{unlock=resolve});
        await previous;
        try {
        if(run.cancelled)throw Error('Run cancelled');
        await checkpoint(run,'model',options.signal);
        const id=randomUUID();let wake;
        const state={run,queue:[],bytes:0,done:false,error:null,resolve(){state.done=true;wake?.();},reject(error){state.error=error;state.done=true;wake?.();}};
        calls.set(id,state);
        const abort=()=>{send({type:'model_cancel',run_id:run.id,id});state.reject(Error('Run cancelled'));};
        options.signal?.addEventListener('abort',abort,{once:true});
        send({type:'model',run_id:run.id,id,purpose:options.purpose,request:modelRequest(run,options)});
        async function* source(){
          while(!state.done||state.queue.length){
            if(state.error)throw state.error;
            if(state.queue.length){const raw=state.queue.shift();state.bytes-=Buffer.byteLength(raw);yield raw;}
            else await new Promise(resolve=>{wake=resolve;state.wake=resolve;});
          }
          if(state.error)throw state.error;
        }
        try {yield* modelChunks(source());}
        finally {calls.delete(id);options.signal?.removeEventListener('abort',abort);}
        } finally {unlock();}
      }
    }
    ctx.llm.registerAdapter(['infercat'], new GatewayAdapter());
    // Catch every helper call too. No provider is allowed to bypass the IPC seam.
    ctx.on('llm/stream', options => new GatewayAdapter().stream(options));
    ctx.on('system-prompt/assemble', async (_assembly, _context, next) => rewrite(await next()));
    ctx.on('session/event', (session, event) => {
      const run = runs.get(session.id);
      if (!run) return;
      if (event.type === 'turn/end' && event.data.reason?.kind === 'error') run.failed = true;
      run.pendingBytes+=Buffer.byteLength(JSON.stringify(event));
      if(run.pendingBytes>900*1024){run.handle?.agent.cancel({kind:"hook",reason:"retained buffer limit"});run.failed=true;return;}
      run.pending.push(event);
    });
    ctx.on('agent/assistant-stream', ({ agent, frame }) => {
      if (runs.has(agent.session.id)) send({ type: 'stream', run_id: agent.session.id, frame });
    });
    ctx.on('tools/execute',async(exec,next)=>{
      const run=runs.get(exec.agent?.session.id);if(!run)throw Error('Unowned tool request');
      if(exec.parent)return next();
      await checkpoint(run,'tool',exec.signal);
      const result=await next();
      return result;
    });
    ctx.on('approval/request',async(req)=>{
      const run=runs.get(req?.agent?.session?.id);
      let text;try{text=approvalText(req);}catch(error){
        if(run)send({type:'approval_error',id:randomUUID(),run_id:run.id,request:'Unsupported native approval request'});
        throw error;
      }
      if(!run)throw Error('Unowned native approval request');
      await checkpoint(run,'approval',req.signal);
      const answer=await ask(run,'approval',{request:text},req.signal);
      return answer.allow===true?'allowed-once':'rejected';
    });
    async function start(message) {
      const run = { id: message.id, input:message.input, cancelled: false,pending:[],pendingBytes:0 };
      runs.set(run.id, run);
      try {
        const selection = { provider: 'infercat', model: message.input.model };
        run.handle = await ctx.agents.create({
          sessionId: run.id, meta: { cwd: message.cwd }, agentOptions: selection,
          setup: agentCtx => { installModelSelection(agentCtx, { current: selection, assembled: undefined }); },
        });
        if (!run.cancelled) {
          await run.handle.agent.whenIdle();
          if (!run.cancelled) {
            const last=run.input.messages.at(-1);
            const text=typeof last.content==='string'?last.content:last.content.filter(p=>p.type==='text').map(p=>p.text).join('\n');
            const prompt=createUserMessage({content:[{type:'text',text:text||'Inspect the attached image.'}],source:{kind:'user'}});
            run.promptID=prompt.id;run.handle.agent.followup(prompt);
          }
          await run.handle.agent.whenIdle();
        }
        await ctx.sessions.flush(run.handle.agent.session);
      } catch (error) {
        run.failed = true;
        send({ type: 'error', run_id: run.id, message: String(error.message) });
      } finally {
        await run.handle?.dispose();
        try { await checkpoint(run,'terminal'); } catch { run.failed = true; }
        runs.delete(run.id);
        send({ type: 'settled', run_id: run.id, cancelled: run.cancelled, failed: !!run.failed });
      }
    }
    const server = createServer((request, response) => {
      response.writeHead(request.url === '/health' ? 200 : 404);
      response.end(request.url === '/health' ? 'ready' : '');
    });
    server.listen({ fd: 3 });
    send({ type: 'ready' });
    for await (const message of frames(process.stdin)) {
      if (message.type === 'start' && typeof message.id === 'string' && /^[a-zA-Z0-9_-]{1,100}$/.test(message.id)
          && message.input && typeof message.input.model === 'string' && Array.isArray(message.input.messages) && typeof message.cwd === 'string'
          && !runs.has(message.id) && runs.size < 64) {
        void start(message).catch(() => process.exit(1));
      } else if (message.type === 'cancel') {
        const run = runs.get(message.id);
        if (run) {
          run.cancelled = true;
          for (const call of calls.values()) if (call.run === run) call.reject(Error('Run cancelled'));
          run.handle?.agent.cancel({kind:"user"});
        }
      } else if (message.type === 'reply' || message.type === 'model_result') {
        const call=calls.get(message.id);if(call){if(message.error)call.reject(Error(message.error));else call.resolve(message);}
      } else if(message.type==='model_data'){
        const call=calls.get(message.id);if(call?.queue){call.bytes+=Buffer.byteLength(message.data);if(call.bytes>MAX_FRAME)throw Error('Model buffer limit');call.queue.push(message.data);call.wake?.();}
      } else {
        throw Error('Invalid agent protocol message');
      }
    }
    process.exit(0); // Host EOF; the guardian joins the process group.
  })().catch(error => { process.stderr.write(String(error.message) + '\n'); process.exit(1); });
}
