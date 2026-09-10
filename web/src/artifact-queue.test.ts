import { afterEach, expect, it, vi } from 'vitest';
import { imageArtifact } from './imageJobs';
import type { Transport } from './transport';

const cooldownMessage = 'too many images loading at once; retry in a second';
const limited = (seconds = 1) => Response.json({ error: { code: 'concurrency_limited', message: cooldownMessage } }, { status: 429, headers: { 'Retry-After': String(seconds) } });
afterEach(() => { vi.clearAllTimers(); vi.useRealTimers(); });

it('honors Retry-After and exposes the gallery message only after two retries', async () => {
  vi.useFakeTimers(); const fetch = vi.fn(async () => limited(2));
  let settled = false;
  const read = imageArtifact({ fetch } as unknown as Transport, 'key', 'image', new AbortController().signal);
  const failed = expect(read.finally(() => { settled = true; })).rejects.toThrow(cooldownMessage);
  await vi.advanceTimersByTimeAsync(1999); expect(fetch).toHaveBeenCalledTimes(1); expect(settled).toBe(false);
  await vi.advanceTimersByTimeAsync(1); expect(fetch).toHaveBeenCalledTimes(2); expect(settled).toBe(false);
  await vi.advanceTimersByTimeAsync(2000); await failed; expect(fetch).toHaveBeenCalledTimes(3);
  await vi.advanceTimersByTimeAsync(10000); expect(fetch).toHaveBeenCalledTimes(3);
});

it('removes an aborted queued tile without starting it or blocking later work', async () => {
  vi.useFakeTimers(); let block = true;
  const releases: (() => void)[] = [], started: string[] = [];
  const transport = { fetch: async (path: string) => {
    started.push(path); if (block) await new Promise<void>((resolve) => releases.push(resolve));
    return new Response(new Uint8Array([1]), { headers: { 'Content-Type': 'image/png' } });
  } } as unknown as Transport;
  const active = Array.from({ length: 4 }, (_, i) => imageArtifact(transport, 'key', `held-${i}`, new AbortController().signal));
  const ac = new AbortController(), queued = imageArtifact(transport, 'key', 'cancelled', ac.signal);
  const cancelled = expect(queued).rejects.toMatchObject({ name: 'AbortError' });
  await vi.advanceTimersByTimeAsync(0); expect(started).toHaveLength(4);
  ac.abort(); await cancelled; block = false; releases.forEach((release) => release()); await Promise.all(active);
  await imageArtifact(transport, 'key', 'next', new AbortController().signal);
  expect(started).toHaveLength(5); expect(started.some((path) => path.endsWith('/cancelled'))).toBe(false);
});

it('cancels a retry cooldown without another host request', async () => {
  vi.useFakeTimers(); const fetch = vi.fn(async () => limited()), ac = new AbortController();
  const read = imageArtifact({ fetch } as unknown as Transport, 'key', 'image', ac.signal);
  const cancelled = expect(read).rejects.toMatchObject({ name: 'AbortError' });
  await vi.advanceTimersByTimeAsync(0); expect(fetch).toHaveBeenCalledTimes(1);
  ac.abort(); await cancelled; await vi.advanceTimersByTimeAsync(5000); expect(fetch).toHaveBeenCalledTimes(1);
});

it('starts the thirty-second read deadline after dequeue, so later tiles can finish', async () => {
  vi.useFakeTimers(); let calls = 0;
  const transport = { fetch: (_path: string, init: RequestInit) => new Promise<Response>((resolve, reject) => {
    calls++; const signal = init.signal!;
    const abort = () => { clearTimeout(timer); reject(signal.reason); };
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve(new Response(new Uint8Array([1]), { headers: { 'Content-Type': 'image/png' } })); }, 20000);
    signal.addEventListener('abort', abort, { once: true });
  }) } as unknown as Transport;
  const reads = Array.from({ length: 8 }, (_, i) => imageArtifact(transport, 'key', String(i), new AbortController().signal));
  await vi.advanceTimersByTimeAsync(0); expect(calls).toBe(4);
  await vi.advanceTimersByTimeAsync(20000); expect(calls).toBe(8);
  await vi.advanceTimersByTimeAsync(20000); expect(await Promise.all(reads)).toHaveLength(8);
});
