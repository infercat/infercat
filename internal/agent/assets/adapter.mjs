// Product-owned plugin; the pinned harness owns agent execution and sandbox policy.
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { createServer } from 'node:http';
import { randomUUID } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';

export const name = 'infercat-agent';
export const inject = ['agents', 'sessions', 'llm', 'systemPrompt'];
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

export function apply(ctx) {
  const require = createRequire(process.env.INFERCAT_AGENT_RUNTIME + '/package.json');
  const load = name => import(pathToFileURL(require.resolve(name)).href);
  void (async () => {
    const [{ installModelSelection }, { LlmAdapter, createUserMessage }] = await Promise.all([
      load('@deepseek-ai/dsh-agent'), load('@deepseek-ai/dsh-llm'),
    ]);
    await ctx.get('loader')?.await();
    const runs = new Map(), calls = new Map();
    const send = message => {
      const raw = JSON.stringify(message) + '\n';
      if (Buffer.byteLength(raw) > MAX_FRAME || process.stdout.writableLength > MAX_FRAME) process.exit(1);
      process.stdout.write(raw);
    };
    class GatewayAdapter extends LlmAdapter {
      async *stream(options) {
        const run = runs.get(options.sessionId);
        if (!run || run.cancelled) throw Error('No live Infercat run for model call');
        const id = randomUUID();
        let resolve, reject;
        const result = new Promise((yes, no) => { resolve = yes; reject = no; });
        const abort = () => { send({ type: 'model_cancel', run_id: run.id, id }); reject(Error('Run cancelled')); };
        calls.set(id, { run, resolve, reject });
        options.signal?.addEventListener('abort', abort, { once: true });
        try {
          if (options.signal?.aborted) throw Error('Run cancelled');
          const { signal, ...request } = options;
          send({ type: 'model', run_id: run.id, id, request });
          for (const chunk of await result) yield chunk;
        } finally {
          calls.delete(id);
          options.signal?.removeEventListener('abort', abort);
        }
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
      send({ type: 'event', run_id: session.id, event });
    });
    ctx.on('agent/assistant-stream', ({ agent, frame }) => {
      if (runs.has(agent.session.id)) send({ type: 'stream', run_id: agent.session.id, frame });
    });
    async function start(message) {
      const run = { id: message.id, cancelled: false };
      runs.set(run.id, run);
      try {
        const selection = { provider: 'infercat', model: message.model };
        run.handle = await ctx.agents.create({
          sessionId: run.id, meta: { cwd: message.cwd }, agentOptions: selection,
          setup: agentCtx => { installModelSelection(agentCtx, { current: selection, assembled: undefined }); },
        });
        if (!run.cancelled) {
          await run.handle.agent.whenIdle();
          if (!run.cancelled) run.handle.agent.followup(createUserMessage({ content: [{ type: 'text', text: message.text }], source: { kind: 'user' } }));
          await run.handle.agent.whenIdle();
        }
        await ctx.sessions.flush(run.handle.agent.session);
      } catch (error) {
        run.failed = true;
        send({ type: 'error', run_id: run.id, message: String(error.message) });
      } finally {
        await run.handle?.dispose();
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
          && typeof message.text === 'string' && typeof message.model === 'string' && typeof message.cwd === 'string'
          && !runs.has(message.id) && runs.size < 64) {
        void start(message).catch(() => process.exit(1));
      } else if (message.type === 'cancel') {
        const run = runs.get(message.id);
        if (run) {
          run.cancelled = true;
          for (const call of calls.values()) if (call.run === run) call.reject(Error('Run cancelled'));
          run.handle?.agent.cancel();
        }
      } else if (message.type === 'model_result' && typeof message.id === 'string' && Array.isArray(message.chunks)) {
        calls.get(message.id)?.resolve(message.chunks); // A reply may cross cancellation settlement.
      } else {
        throw Error('Invalid agent protocol message');
      }
    }
    process.exit(0); // Host EOF; the guardian joins the process group.
  })().catch(error => { process.stderr.write(String(error.message) + '\n'); process.exit(1); });
}
