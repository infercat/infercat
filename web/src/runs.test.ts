import { expect, it } from 'vitest';
import { followRuns } from './runs';
import type { Transport } from './transport';
const frame = (data: unknown) => new Response(`data: ${JSON.stringify(data)}\n\n`, { headers: { 'content-type': 'text/event-stream' } });
it('reconnects with the cursor only after reading the changed run and returns the completed snapshot', async () => {
  const ac = new AbortController(); const cursors: (string | null)[] = [], states: string[] = [], paths: string[] = [];
  let n = 0;
  const transport = { fetch: async (path: string, init?: RequestInit) => {
    paths.push(path);
    if (path === '/v1/events') {
      cursors.push(new Headers(init?.headers).get('Last-Event-ID')); n++;
      return frame({ cursor: `epoch:${n}`, time: 'now', ...(n === 1 ? { reset: true, runs: [{ id: 'r', kind: 'agent', state: 'running' }] } : { run_id: 'r', state: 'done' }) });
    }
    return Response.json({ id: 'r', kind: 'agent', state: n === 1 ? 'running' : 'done' });
  } } as Transport;
  await followRuns({ transport, secret: 'key', signal: ac.signal, connected() {}, failed(error) { throw error; }, delay: async () => {}, changed(runs) { states.push(runs[0]!.state); if (states.length === 2) ac.abort(); } });
  expect(cursors).toEqual([null, 'epoch:1']); expect(states).toEqual(['running', 'done']); expect(paths.every((p) => p.startsWith('/v1/'))).toBe(true);
});
it('does not advance the cursor if a snapshot read was disconnected', async () => {
  const ac = new AbortController(); let reads = 0; const cursors: (string | null)[] = [];
  const transport = { fetch: async (path: string, init?: RequestInit) => {
    if (path === '/v1/events') { cursors.push(new Headers(init?.headers).get('Last-Event-ID')); return frame({ cursor: 'e:1', run_id: 'r', time: 'now' }); }
    if (++reads === 1) throw new Error('offline'); return Response.json({ id: 'r', state: 'done' });
  } } as Transport;
  await followRuns({ transport, secret: 'key', signal: ac.signal, connected() {}, failed() {}, delay: async () => {}, changed() { ac.abort(); } });
  expect(cursors).toEqual([null, null]);
});
it('clears a stale epoch through a read-only reset', async () => {
  const ac = new AbortController(); let n = 0; const cursors: (string | null)[] = [];
  const transport = { fetch: async (path: string, init?: RequestInit) => {
    expect(init?.method ?? 'GET').toBe('GET');
    if (path !== '/v1/events') return Response.json({ id: 'r', state: 'running' });
    cursors.push(new Headers(init?.headers).get('Last-Event-ID')); n++;
    if (n === 2) return Response.json({ error: { code: 'invalid_request', message: 'invalid cursor' } }, { status: 400 });
    return frame({ cursor: n === 1 ? 'old:1' : 'new:1', time: 'now', reset: true, runs: [] });
  } } as Transport;
  await followRuns({ transport, secret: 'key', signal: ac.signal, connected() {}, failed(error) { throw error; }, delay: async () => {}, changed() { if (n === 3) ac.abort(); } });
  expect(cursors).toEqual([null, 'old:1', null]);
});

import { fitsRunInput, mergeRunRecords, runItem } from './runs';
import { isAnswer, reopenChats, delivered, type Conversation } from './storage';
import { scriptedRun } from '../dev/fake-runs';
import { carried } from './stream';
it('keeps durable running items live across local history reopen, but carries only done words', () => {
  const running = runItem(scriptedRun('r', 'running'));
  const c: Conversation = { id: 'c', title: 'task', createdAt: 1, updatedAt: 1, messages: [{ id: 'u', role: 'user', content: 'task' }, running] };
  expect(reopenChats([c])[0]!.messages[1]).toEqual(running);
  expect(isAnswer(running)).toBe(false); expect(delivered(running)).toBe(true);
  const done = runItem(scriptedRun('r', 'done'));
  expect(carried([done], { systemPrompt: '' }, 0).messages).toEqual([{ role: 'assistant', content: 'The summary is in notes.md.' }]);
});
it('reconciles resets by run id, preserves unrelated history and does not duplicate steps', () => {
  const record = scriptedRun('r', 'running');
  const c: Conversation = { id: 'c', title: 'task', createdAt: 1, updatedAt: 1, messages: [runItem(record)] };
  const update = { ...record, steps: [{ ...record.steps![0]!, text: 'replacement' }] };
  const next = mergeRunRecords(mergeRunRecords([c], [update]), [update]);
  expect(next).toHaveLength(1); expect(next[0]!.messages).toHaveLength(1);
  const item = next[0]!.messages[0]!;
  expect(item.kind === 'run' && item.run?.steps).toEqual(update.steps);
  expect(item.kind === 'run' && item.run?.input).toBeNull();
});
it('discovers unassociated runs without guessing a prompt match', () => {
  const r = scriptedRun('r', 'done'); r.input = { messages: [{ role: 'user', content: 'same words' }] };
  const old: Conversation = { id: 'old', title: 'same', createdAt: 1, updatedAt: 1, messages: [{ id: 'u', role: 'user', content: 'same words' }] };
  const next = mergeRunRecords([old], [r]);
  expect(next).toHaveLength(2); expect(next[0]).toBe(old); expect(next[1]!.id).toBe('run:r');
});
it('bounds UTF-8 input bytes, not JavaScript characters', () => {
  expect(fitsRunInput({ text: '界'.repeat(400000) })).toBe(false);
  expect(fitsRunInput({ text: 'a'.repeat(400000) })).toBe(true);
});

import { applyRunStep } from './runs';
it('upserts full step events by identity, preserves order and does not regress a newer snapshot', () => {
  const record = scriptedRun('r', 'running'), step = { ...record.steps![1]!, result: '8 results' };
  const event = { cursor: 'e:5', time: new Date(Date.parse(record.updated) + 1000).toISOString(), run_id: 'r', state: 'running' as const, type: 'step' as const, step };
  const next = applyRunStep(record, event);
  expect(next.steps).toHaveLength(3); expect(next.steps![1]).toEqual(step);
  expect(applyRunStep(next, { ...event, time: record.created })).toBe(next);
  expect(applyRunStep({ ...next, state: 'done' }, event).state).toBe('done');
});

it('uses key-scoped correlation after a lost response, without choosing among multiple runs', () => {
  const pending = { kind: 'run' as const, id: 'local', role: 'assistant' as const, content: '', submission: 'uncertain' as const, clientRequestId: 'request_1', keyId: 'key-a' };
  const c: Conversation = { id: 'c', title: 'task', createdAt: 1, updatedAt: 1, messages: [pending] };
  const first = { ...scriptedRun('r1'), client_request_id: 'request_1' };
  const second = { ...scriptedRun('r2'), client_request_id: 'request_1' };
  const linked = mergeRunRecords([c], [first], 'key-a');
  expect(linked).toHaveLength(1); expect(linked[0]!.messages[0]!.id).toBe('local');
  const both = mergeRunRecords(linked, [second], 'key-a');
  expect(both).toHaveLength(1); expect(both[0]!.messages).toHaveLength(2);
  expect(mergeRunRecords([c], [first, second], 'key-a')[0]!.messages).toHaveLength(2);
  const foreign = mergeRunRecords([c], [first], 'key-b');
  expect(foreign).toHaveLength(2); expect(foreign[0]!.messages[0]).toBe(pending);
});
it('does not guess a conversation if a correlation value is reused in two chats', () => {
  const item = { kind: 'run' as const, id: 'local', role: 'assistant' as const, content: '', clientRequestId: 'same', keyId: 'key' };
  const c: Conversation = { id: 'a', title: 'task', createdAt: 1, updatedAt: 1, messages: [item] };
  expect(mergeRunRecords([c, { ...c, id: 'b' }], [{ ...scriptedRun('r'), client_request_id: 'same' }], 'key')).toHaveLength(3);
});
