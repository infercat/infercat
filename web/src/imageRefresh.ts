import { GatewayError } from './api';
/** Event bursts share one read; ongoing traffic spends at most half the key's RPM on lists.
 * Only reads retry. A 429 preserves the last snapshot and honours the server's cooldown. */
export function imageRefresh(read: (signal: AbortSignal) => Promise<void>, signal: AbortSignal, interval = 6000) {
  let timer: ReturnType<typeof setTimeout> | undefined, running = false, dirty = false, next = 0;
  let waiting: { resolve: () => void; reject: (e: unknown) => void }[] = [];
  const schedule = () => { if (!timer && !running && !signal.aborted) timer = setTimeout(() => { timer = undefined; void flush(); }, Math.max(250, next - Date.now())); };
  async function flush() {
    if (signal.aborted) return;
    running = true; dirty = false; const held = waiting; waiting = []; next = Date.now() + interval;
    try { await read(signal); held.forEach((w) => w.resolve()); }
    catch (e) {
      if (!signal.aborted && e instanceof GatewayError && e.status === 429) {
        next = Math.max(next, Date.now() + (e.retryAfterS ?? 60) * 1000); dirty = true; waiting.unshift(...held);
      } else held.forEach((w) => w.reject(e));
    } finally { running = false; if (dirty) schedule(); }
  }
  signal.addEventListener('abort', () => { clearTimeout(timer); waiting.forEach((w) => w.resolve()); waiting = []; }, { once: true });
  return () => {
    if (signal.aborted) return Promise.resolve();
    dirty = true;
    const result = new Promise<void>((resolve, reject) => waiting.push({ resolve, reject })); schedule(); return result;
  };
}
