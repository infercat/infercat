import { afterEach, expect, it, vi } from 'vitest';
import { GatewayError } from './api';
import { imageRefresh } from './imageRefresh';
import { imageLimit } from './imageJobs';
afterEach(() => vi.useRealTimers());
it('coalesces a burst and spaces subsequent reads, with one read in flight', async () => {
  vi.useFakeTimers(); const ac = new AbortController(), read = vi.fn(async () => {}), refresh = imageRefresh(read, ac.signal);
  const first = Array.from({ length: 30 }, () => refresh());
  await vi.advanceTimersByTimeAsync(249); expect(read).not.toHaveBeenCalled();
  await vi.advanceTimersByTimeAsync(1); await Promise.all(first); expect(read).toHaveBeenCalledTimes(1);
  const later = [refresh(),refresh()]; await vi.advanceTimersByTimeAsync(5999); expect(read).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(1); await Promise.all(later); expect(read).toHaveBeenCalledTimes(2); ac.abort();
});
it('keeps the last snapshot on 429 and backs off through event bursts until Retry-After', async () => {
  vi.useFakeTimers(); const ac = new AbortController(); let snapshot = 'running';
  const read = vi.fn().mockRejectedValueOnce(new GatewayError(429,'rate_limited','','slow down',12)).mockImplementationOnce(async () => { snapshot = 'done'; });
  const refresh = imageRefresh(read, ac.signal), pending = refresh();
  await vi.advanceTimersByTimeAsync(250); expect(snapshot).toBe('running'); expect(read).toHaveBeenCalledTimes(1);
  const burst = Array.from({ length: 20 }, () => refresh()); await vi.advanceTimersByTimeAsync(11999);expect(read).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(1); await Promise.all([pending,...burst]); expect(snapshot).toBe('done');expect(read).toHaveBeenCalledTimes(2);ac.abort();
});
it('waits a conservative minute without Retry-After and cancels scheduled reads on disconnect', async () => {
  vi.useFakeTimers();const ac = new AbortController(), read = vi.fn().mockRejectedValue(new GatewayError(429,'rate_limited','','slow down'));
  const refresh = imageRefresh(read,ac.signal), pending = refresh(); await vi.advanceTimersByTimeAsync(250);
  await vi.advanceTimersByTimeAsync(59000);expect(read).toHaveBeenCalledTimes(1); ac.abort();await pending;
  await vi.advanceTimersByTimeAsync(10000);expect(read).toHaveBeenCalledTimes(1);
});
it('events arriving during a read cause one trailing read, without overlap', async () => {
  vi.useFakeTimers();const ac = new AbortController();let finish!:()=>void;
  const read = vi.fn().mockImplementationOnce(()=>new Promise<void>((r)=>{finish=r;})).mockResolvedValue(undefined), refresh=imageRefresh(read,ac.signal);
  const first=refresh();await vi.advanceTimersByTimeAsync(250);const trailing=[refresh(),refresh()];await vi.advanceTimersByTimeAsync(10000);expect(read).toHaveBeenCalledTimes(1);
  finish();await first;await vi.advanceTimersByTimeAsync(250);await Promise.all(trailing);expect(read).toHaveBeenCalledTimes(2);ac.abort();
});
it('uses negative image limits as unlimited and zero/absent as defaults',()=>{
 expect([-1,-8,0,undefined,3].map((n)=>imageLimit(n,8))).toEqual([-1,-8,8,8,3]);
 expect(imageLimit(0,20)).toBe(20);
});
