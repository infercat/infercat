// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { act, createElement, StrictMode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import App, { restoredSession, shellTransport } from './App';
import { live, reduce, dropped, probing, type SessionState, type Live } from './session';
import { KEYS, hostScope, scopedKeys, save, saveChat } from './storage';
import { openTransport, type Transport } from './transport';
import fixture from './fixtures/me/current.json';
const device = vi.hoisted(() => ({ standalone: true, online: false, hidden: false, now: 100_000 }));
vi.mock('./install', async (original) => ({ ...await original<typeof import('./install')>(), platform: () => ({ standalone: device.standalone, coarse: false, ios: false, safariDesktop: false }) }));
vi.mock('./transport', async (original) => ({ ...await original<typeof import('./transport')>(), openTransport: vi.fn(), claimTunnelIdentity: async () => true }));
vi.mock('./storage', async (original) => ({ ...await original<typeof import('./storage')>(), electStore: (_scope: string, change: (leader: boolean) => void) => { change(true); return { release() {}, takeOver() {} }; } }));
vi.mock('./image-store', async (original) => ({ ...await original<typeof import('./image-store')>(), readImages: async () => ({}) }));
const addr = 'tcAgIBAqQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8', scope = hostScope(addr);
let root: Root | undefined, container: HTMLDivElement;
function seed() {
  save(KEYS.invite, `ic1.${addr}.YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY3`);
  save(KEYS.lastHost, { scope, name: 'fixture' }); save(scopedKeys(scope).me, fixture);
  saveChat(scope, { id: 'saved', title: 'Saved conversation', createdAt: 1, updatedAt: 1, messages: [{ id: 'question', role: 'user', content: 'History remains readable' }] });
}
function candidate(): Transport {
  return { kind: 'tunnel', close: vi.fn(), ping: async () => null, fetch: vi.fn(async () => new Response(JSON.stringify(fixture))) };
}
beforeEach(() => {
  localStorage.clear(); seed(); vi.clearAllMocks();
  device.online = false; device.standalone = true; device.hidden = false; device.now = 100_000;
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, matchMedia: () => ({ matches: false }) });
  Object.defineProperty(navigator, 'onLine', { configurable: true, get: () => device.online });
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => device.hidden });
  vi.spyOn(Date, 'now').mockImplementation(() => device.now);
  vi.mocked(openTransport).mockImplementation(async () => ({ transport: candidate(), path: null }));
  container = document.createElement('div'); document.body.append(container);
});
afterEach(async () => { if (root) await act(async () => root!.unmount()); root = undefined; container.remove(); vi.restoreAllMocks(); });
async function mount(skipDelay = false) { root = createRoot(container); await act(async () => root!.render(createElement(StrictMode, null, createElement(App)))); await act(async () => { await import('./ui/Chat'); if (!skipDelay) await new Promise((resolve) => setTimeout(resolve, 30)); }); }
async function network(online: boolean) { device.online = online; await act(async () => window.dispatchEvent(new Event(online ? 'online' : 'offline'))); }
async function visibility(hidden: boolean, elapsed = 0) { device.now += elapsed; device.hidden = hidden; await act(async () => document.dispatchEvent(new Event('visibilitychange'))); }
it('restores only remembered, matching, un-disconnected history with a usable snapshot; links win', () => {
  expect(restoredSession(false, false, false).name).toBe('degraded');
  expect(restoredSession(true, true, false).name).toBe('connecting');
  expect(restoredSession(true, false, false).name).toBe('idle');
  expect(restoredSession(false, true, true).name).toBe('idle');
  for (const patch of [{ scope, left: true }, { scope: 'another-host' }]) { save(KEYS.lastHost, patch); expect(restoredSession(false, true, false).name).toBe('idle'); }
  seed(); save(scopedKeys(scope).me, { host: {} }); expect(restoredSession(false, true, false).name).toBe('idle');
  seed(); localStorage.removeItem(scopedKeys(scope).me); expect(restoredSession(false, true, false).name).toBe('idle');
});
it('offline closes live and verifying transports, ignores late success and disables probes', () => {
  const held = live(restoredSession(false, true, false))!;
  const previous: SessionState = { name: 'verifying', transport: candidate(), redial: { ...held, transport: candidate(), offline: false } };
  const event = { t: 'offline' as const, transport: shellTransport() }, next = reduce(previous, event);
  expect(dropped(previous, event, next)).toHaveLength(2); expect(probing(next)).toBe(false);
  const late: Live = { ...held, transport: candidate(), offline: false, meOk: true };
  for (const event of [{ t: 'verified' as const, live: late }, { t: 'meOk' as const, me: late.me }, { t: 'pingOk' as const, path: { rttMs: 2, direct: true, via: '' }, at: 1 }]) expect(reduce(next, event)).toBe(next);
  expect(dropped(next, { t: 'verified', live: late }, next)).toEqual([late.transport]);
});
it('first snapshot verify failure returns to the card, while a warm redial stays degraded', () => {
  const held = live(restoredSession(false, true, false))!;
  const error = { title: 'Unavailable', detail: 'No reply' };
  expect(reduce({ name: 'connecting', redial: held }, { t: 'meError', error }).name).toBe('disconnected');
  expect(reduce({ name: 'connecting', redial: { ...held, snapshot: false, offline: false } }, { t: 'meError', error }).name).toBe('degraded');
});
it('offline renders saved chat without a dial, allows drafting and recovers once without replay', async () => {
  await mount(); expect(container.textContent).toContain('History remains readable'); expect(container.textContent).toContain('No network on this device');
  expect(openTransport).not.toHaveBeenCalled();
  const field = container.querySelector('textarea')!; expect(field.disabled).toBe(false);
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(field, 'Offline draft'); field.dispatchEvent(new Event('input', { bubbles: true })); });
  expect(container.querySelector<HTMLButtonElement>('.composer button.primary')!.disabled).toBe(true);
  await network(true); await network(true);
  expect(openTransport).toHaveBeenCalledTimes(1); expect(field.value).toBe('Offline draft'); expect(field.disabled).toBe(false);
  expect(container.querySelector<HTMLButtonElement>('.composer button.primary')!.disabled).toBe(false);
  const transport = (await vi.mocked(openTransport).mock.results[0]!.value).transport;
  expect(transport.fetch).toHaveBeenCalledTimes(1); // verify only, no replay of history/draft
});
it('long standalone resumes redial once; short resumes and browser tabs do not', async () => {
  device.online = true; await mount(); expect(openTransport).toHaveBeenCalledTimes(1);
  await visibility(true); await visibility(false, 30_000); expect(openTransport).toHaveBeenCalledTimes(1);
  await visibility(true); await visibility(false, 30_001); expect(openTransport).toHaveBeenCalledTimes(2);
  await visibility(false); expect(openTransport).toHaveBeenCalledTimes(2);
  device.standalone = false; await visibility(true); await visibility(false, 90_000); expect(openTransport).toHaveBeenCalledTimes(2);
});
it('offline during an unfinished dial does not let a stale candidate or coalesced old dial win', async () => {
  let finish!: (value: Awaited<ReturnType<typeof openTransport>>) => void;
  vi.mocked(openTransport).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
  device.online = true; await mount(); expect(openTransport).toHaveBeenCalledTimes(1);
  await network(false); await network(true); expect(openTransport).toHaveBeenCalledTimes(2);
  const stale = candidate(); await act(async () => finish({ transport: stale, path: null }));
  expect(stale.close).toHaveBeenCalled(); expect(container.textContent).not.toContain('No network on this device');
});

it('an offline empty card defers a typed invite until the network returns', async () => {
  localStorage.clear(); await mount();
  expect(container.textContent).toContain('No network on this device. The card will connect when it is back.');
  expect(openTransport).not.toHaveBeenCalled();
  const field = container.querySelector('textarea')!;
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(field, `ic1.${addr}.YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY3`); field.dispatchEvent(new Event('input', { bubbles: true })); });
  expect(container.querySelector<HTMLButtonElement>('.connect-actions button.primary')!.disabled).toBe(true);
  await network(true); expect(openTransport).toHaveBeenCalledTimes(1);
});
it('a probe response from before an offline/redial cycle cannot overwrite the new verified snapshot', async () => {
  const old = candidate(); let finish!: (response: Response) => void;
  vi.mocked(old.fetch).mockImplementationOnce(async () => new Response(JSON.stringify(fixture))).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
  vi.mocked(openTransport).mockResolvedValueOnce({ transport: old, path: null });
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  try {
    device.online = true; await mount(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(finish).toBeDefined(); await network(false); await network(true);
    await act(async () => finish(new Response(JSON.stringify({ ...fixture, host: { ...fixture.host, name: 'STALE PROBE' } }))));
    expect(container.textContent).not.toContain('STALE PROBE');
  } finally { vi.useRealTimers(); }
});

it('an edited draft survives a refused replacement and stays available after recovery', async () => {
  await mount();
  const edit = container.querySelector<HTMLButtonElement>('.row.user .actions button')!;
  await act(async () => edit.click());
  const field = container.querySelector<HTMLTextAreaElement>('.bubble.editing textarea')!;
  expect(field.disabled).toBe(false);
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(field, 'Keep my edited draft'); field.dispatchEvent(new Event('input', { bubbles: true })); });
  const replace = container.querySelector<HTMLButtonElement>('.edit-actions .primary')!;
  expect(replace.disabled).toBe(true); await act(async () => replace.click());
  expect(container.querySelector('.bubble.editing textarea')).toBe(field); expect(field.value).toBe('Keep my edited draft');
  expect(openTransport).not.toHaveBeenCalled();
  await network(true);
  expect(field.value).toBe('Keep my edited draft'); expect(replace.disabled).toBe(false);
  await act(async () => replace.click());
  expect(container.querySelector('.bubble.editing')).toBeNull();
  expect(container.querySelector('.row.user .bubble')?.textContent).toBe('Keep my edited draft');
});
