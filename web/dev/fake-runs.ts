import type { FakeRequest, FakeResponse } from './fake-backend';
import type { RunEvent, RunRecord } from '../src/api';
export const runs = new Map<string, RunRecord>();
const events: RunEvent[] = [];
try { for (const record of JSON.parse(globalThis.localStorage?.getItem('fake.runs') ?? '[]') as RunRecord[]) runs.set(record.id, record); } catch { /* isolated test host without storage */ }
export const runRequests: { path: string; method: string; body: unknown }[] = [];
let sequence = 0;
const json = (value: unknown, status = 200): FakeResponse => ({ status, headers: { 'content-type': 'application/json' }, body: JSON.stringify(value) });
const failure = (message: string, status = 400) => json({ error: { code: 'invalid_request', type: 'invalid_request_error', message } }, status);
export function scriptedRun(id: string, state: RunRecord['state'] = 'running'): RunRecord {
  const created = new Date(Date.now() - (state === 'done' ? 134 : 31) * 1000).toISOString();
  return { id, kind: 'agent', state, created, updated: new Date().toISOString(), expires: new Date(Date.now() + 86400000).toISOString(), cancel_requested: false, queue_position: 2, input: null,
    steps: state === 'queued' ? [] : [
      { id: 't', type: 'step', at: created, kind: 'think', text: 'I will check the documentation, find the burst rule, and write a concise summary of what it permits.', status: 'done' },
      { id: 's', type: 'step', at: new Date(Date.parse(created) + 4000).toISOString(), kind: 'search', tool: 'exa', result: '4 results', output_id: 'search', status: 'done' },
      { id: 'r', type: 'step', at: new Date(Date.parse(created) + 12000).toISOString(), kind: 'run', name: 'stats.py', status: state === 'running' ? 'running' : 'done', ...(state === 'done' ? { result: 'exit 0 · 11 17 14', text: '11 17 14' } : {}) },
    ],
    ...(state === 'waiting' ? { approval: { id: 'approval-1', request: 'write notes.md in the workspace (2 lines)', status: 'pending' as const } } : {}),
    ...(state === 'failed' ? { reason: 'The engine stopped answering during this step.' } : {}),
    ...(state === 'done' ? { text: 'The summary is in notes.md.' } : {}),
    outputs: ['done','cancelled'].includes(state) ? [{ id: 'notes', name: 'notes.md', mime: 'text/markdown', size: 93 }] : [],
    attempts: ['done','failed','cancelled'].includes(state) ? [{ id: 'model-1', dispatched: true, settled: true, accounting_uncertain: false, usage: { prompt_tokens: 4120, completion_tokens: 910 } }] : [],
  };
}
export function updateRun(id: string, patch: Partial<RunRecord>) {
  const old = runs.get(id); if (!old) throw new Error('Unknown fake run');
  const record = { ...old, ...patch, updated: new Date().toISOString() }; runs.set(id, record);
  events.push({ cursor: `fake:${++sequence}`, run_id: id, time: record.updated, state: record.state, ...(patch.steps?.at(-1) ? { type: 'step', step: patch.steps.at(-1) } : {}) });
  try { globalThis.localStorage?.setItem('fake.runs', JSON.stringify([...runs.values()])); } catch { /* optional fake host storage */ }
  return record;
}
export function handleFakeRuns(req: FakeRequest, state = 'running'): FakeResponse | undefined {
  Object.assign(globalThis, { __fakeRuns: { runRequests, updateRun, scriptedRun, get: (id: string) => structuredClone(runs.get(id)) } });
  const path = req.path.split('?')[0]!;
  if (path !== '/v1/events' && !path.startsWith('/v1/runs')) return;
  runRequests.push({ path, method: req.method, body: req.body ? JSON.parse(req.body) : null });
  if (path === '/v1/events') {
    const cursor = req.headers['last-event-id'] ?? ''; let n = cursor ? Number(cursor.split(':')[1]) : 0;
    if (cursor && (!cursor.startsWith('fake:') || !Number.isInteger(n) || n > sequence)) return failure('invalid cursor');
    return { status: 200, headers: { 'content-type': 'text/event-stream' }, sse: (async function* () {
      const frame = (e: RunEvent) => `id: ${e.cursor}\nevent: run\ndata: ${JSON.stringify(e)}\n\n`;
      if (!cursor) { n = sequence; yield frame({ cursor: `fake:${n}`, time: new Date().toISOString(), reset: true, runs: [...runs.values()].map(({ id, kind, state, client_request_id }) => ({ id, kind, state, client_request_id })) }); }
      for (;;) { for (const e of events.filter((e) => Number(e.cursor.split(':')[1]) > n)) { n = Number(e.cursor.split(':')[1]); yield frame(e); } yield ': keepalive\n\n'; await new Promise((resolve) => setTimeout(resolve, 100)); }
    })() };
  }
  if (path === '/v1/runs') {
    if (req.method === 'GET') return json({ runs: [...runs.values()].map(({ id, kind, state, client_request_id }) => ({ id, kind, state, client_request_id })) });
    if (req.method !== 'POST') return failure('method', 405);
    const body = JSON.parse(req.body) as { kind: string; input: unknown; client_request_id?: string };
    if (body.kind !== 'agent' || new TextEncoder().encode(JSON.stringify(body.input)).length > 1024 * 1024) return failure('run input too large', 413);
    const id = `r-${runs.size + 1}`; const record = scriptedRun(id, (state === 'unranked' ? 'queued' : state) as RunRecord['state']); if (state === 'unranked') delete record.queue_position; record.input = body.input; record.client_request_id = body.client_request_id; runs.set(id, record); updateRun(id, {}); return json({ id }, 202);
  }
  const [id, route, output] = path.slice('/v1/runs/'.length).split('/'); const record = runs.get(id!);
  if (!record) return failure('not found', 404);
  if (route === 'outputs' && req.method === 'GET') return { status: 200, headers: { 'content-type': 'text/plain' }, body: output === 'search' ? 'Four search results from the host.\nThe burst rule is documented.' : 'The burst rule permits twice the per-minute rate.\nIt applies within a ten-second window.' };
  if (route === 'approval' && req.method === 'POST') {
    const answer = JSON.parse(req.body) as { id: string; allow: boolean };
    if (record.approval?.status !== 'pending' || record.approval.id !== answer.id || typeof answer.allow !== 'boolean') return failure('stale approval', 409);
    return json(updateRun(id!, { state: answer.allow ? 'running' : 'failed', approval: { ...record.approval, status: 'answered', allow: answer.allow }, ...(answer.allow ? {} : { reason: 'The requested step was denied.' }) }));
  }
  if (req.method === 'DELETE') return json(updateRun(id!, { state: 'cancelled', cancel_requested: true }));
  return json(record);
}
