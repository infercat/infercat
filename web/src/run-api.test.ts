import { expect, it } from 'vitest';
import { cancelRun, getRun, runEvents, submitRun } from './api';
import type { Transport } from './transport';
const signal = new AbortController().signal;
function transport(fetch: Transport['fetch']) { return { fetch } as Transport; }
it('submits exactly once with the existing bearer and keeps Cancel independent', async () => {
  const calls: { path: string; init?: RequestInit }[] = [];
  const t = transport(async (path, init) => { calls.push({ path, init }); return Response.json({ id: 'r-1' }); });
  await submitRun(t, 'test-bearer', { prompt: 'hello' }, signal);
  await cancelRun(t, 'test-bearer', 'r-1', signal);
  await getRun(t, 'test-bearer', 'r-1', signal);
  expect(calls.map((c) => [c.path, c.init?.method ?? 'GET'])).toEqual([['/v1/runs', 'POST'], ['/v1/runs/r-1', 'DELETE'], ['/v1/runs/r-1', 'GET']]);
  expect(JSON.parse(String(calls[0]!.init!.body))).toEqual({ kind: 'agent', input: { prompt: 'hello' } });
  expect(new Headers(calls[0]!.init!.headers).get('authorization')).toBe('Bearer test-bearer');
});
it('does not replay a submission whose response was lost', async () => {
  let n = 0; const t = transport(async () => { n++; throw new Error('connection lost'); });
  await expect(submitRun(t, 'key', {}, signal)).rejects.toThrow('connection lost'); expect(n).toBe(1);
});
it('reads split SSE frames, ignores heartbeat and sends the last cursor', async () => {
  let requested: RequestInit | undefined;
  const raw = ': keepalive\r\n\r\nid: epoch:4\nevent: run\ndata: {"cursor":"epoch:4","run_id":"r","state":"running","time":"2026-09-11T12:00:00Z"}\n\n';
  const t = transport(async (_path, init) => {
    requested = init;
    return new Response(new ReadableStream({ start(c) { for (const char of raw) c.enqueue(new TextEncoder().encode(char)); c.close(); } }), { headers: { 'content-type': 'text/event-stream' } });
  });
  const events = []; for await (const e of runEvents(t, 'key', 'epoch:3', signal)) events.push(e);
  expect(events).toHaveLength(1); expect(events[0]!.cursor).toBe('epoch:4');
  expect(new Headers(requested?.headers).get('last-event-id')).toBe('epoch:3');
});
it('exposes reset snapshots rather than treating them as completion', async () => {
  const event = { cursor: 'epoch:8', time: '2026-09-11T12:00:00Z', reset: true, runs: [{ id: 'r', kind: 'agent', state: 'done' }] };
  const t = transport(async () => new Response(`data: ${JSON.stringify(event)}\n\n`, { headers: { 'content-type': 'text/event-stream' } }));
  const events = []; for await (const e of runEvents(t, 'key', '', signal)) events.push(e);
  expect(events).toEqual([event]);
});

import { answerRun, runOutput } from './api';
it('answers a single approval id, and leaves stale/duplicate answers as conflicts', async () => {
  let calls = 0;
  const t = transport(async (path, init) => {
    calls++; expect(path).toBe('/v1/runs/r/approval'); expect(JSON.parse(String(init?.body))).toEqual({ id: 'a', allow: false });
    return Response.json({ error: { code: 'invalid_request', message: 'stale approval' } }, { status: 409 });
  });
  await expect(answerRun(t, 'key', 'r', 'a', false, signal)).rejects.toMatchObject({ status: 409 }); expect(calls).toBe(1);
});
it('fetches output only under the scoped run path and bounds streamed bytes', async () => {
  const t = transport(async (path) => { expect(path).toBe('/v1/runs/r/outputs/o'); return new Response(new Uint8Array(1024 * 1024 + 1)); });
  await expect(runOutput(t, 'key', 'r', 'o', signal)).rejects.toThrow('exceeds 1 MiB');
});
it('keeps validated correlation outside model input and never makes it idempotency', async () => {
  let calls = 0;
  const t = transport(async (_path, init) => { calls++; expect(JSON.parse(String(init?.body))).toEqual({ kind: 'agent', input: { prompt: 'hello' }, client_request_id: 'request_1' }); return Response.json({ id: `r-${calls}` }); });
  await submitRun(t, 'key', { prompt: 'hello' }, signal, 'request_1');
  await submitRun(t, 'key', { prompt: 'hello' }, signal, 'request_1');
  expect(calls).toBe(2);
  await expect(submitRun(t, 'key', {}, signal, '../bad')).rejects.toThrow('Invalid client request id');
  expect(calls).toBe(2);
});

import { timeoutSignal } from './api';
it('bounds a request and follows parent cancellation without AbortSignal.any', async () => {
  const parent = new AbortController(), combined = timeoutSignal(10000, parent.signal);
  parent.abort('tab closed'); expect(combined.aborted).toBe(true); expect(combined.reason).toBe('tab closed');
  const elapsed = timeoutSignal(1, new AbortController().signal);
  await new Promise((resolve) => elapsed.addEventListener('abort', resolve, { once: true })); expect(elapsed.aborted).toBe(true);
});
