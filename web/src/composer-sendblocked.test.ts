// @vitest-environment jsdom
import { fakeTransport, mountHost, typeInto } from './test/fakes';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { act, createElement } from 'react';
import Chat from './ui/Chat';
import Connect from './ui/Connect';
import { saveImage } from './ui/ImageJobs';
import type { ImageJob } from './imageJobs';
import { chatEvents, type Me } from './api';
import { hostScope, loadChats } from './storage';
import type { Live } from './session';
import me from './fixtures/me/current.json';

const election = vi.hoisted(() => ({ leader: true }));
vi.mock('./storage', async (original) => ({ ...await original<typeof import('./storage')>(),
  electStore: (_scope: string, change: (leader: boolean) => void) => {
    change(election.leader); return { release() {}, takeOver() {} };
  },
}));
vi.mock('./image-store', async (original) => ({ ...await original<typeof import('./image-store')>(), readImages: async () => ({}) }));
vi.mock('./api', async (original) => ({ ...await original<typeof import('./api')>(),
  chatEvents: vi.fn(async function* () { yield* []; }), getMe: async () => me,
}));
let host: ReturnType<typeof mountHost>, container: HTMLDivElement;
const live: Live = {
  addr: 'composer-test', secret: 'test', me: me as Me, mode: 'direct',
  transport: fakeTransport(async () => { throw new Error('Unexpected transport I/O'); }),
  path: null, pathAt: 0, pathOk: true, meOk: true, key: 'active', ephemeral: true, probed: 0,
};
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  localStorage.clear(); election.leader = true; vi.clearAllMocks();
  host = mountHost(); container = host.container;
});
afterEach(async () => { await host.unmount(); });
async function render(reconnecting: boolean, key: Live['key'] = 'active') {
  const current = { ...live, key };
  await host.render(createElement(Chat, { live: current, state: { name: 'connected', live: current }, reconnecting, dispatch() {}, onRedial() {} }));
}
const field = () => container.querySelector('textarea')!;
const sendButton = () => container.querySelector<HTMLButtonElement>('.composer button.primary')!;
async function type(text: string) { await typeInto(field(), text); }
async function enter(options: KeyboardEventInit = {}) {
  await act(async () => { field().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true, ...options })); });
}
it('reconnecting allows typing but blocks Send, Enter and direct suggestion callbacks without persistence', async () => {
  await render(true);
  expect(field().disabled).toBe(false);
  await type('Keep this draft');
  expect(field().value).toBe('Keep this draft'); expect(sendButton().disabled).toBe(true);
  await act(async () => sendButton().click()); await enter();
  const suggestion = container.querySelector<HTMLButtonElement>('.suggestion')!;
  expect(suggestion).not.toBeNull(); await act(async () => suggestion.click());
  expect(chatEvents).not.toHaveBeenCalled(); expect(loadChats(hostScope(live.addr))).toEqual([]);
  expect(field().value).toBe('Keep this draft');
});
it('verification enables the retained draft without replaying it, then Enter sends once', async () => {
  await render(true); await type('Retained through redial'); await enter();
  await render(false);
  expect(field().disabled).toBe(false); expect(field().value).toBe('Retained through redial');
  expect(sendButton().disabled).toBe(false); expect(chatEvents).not.toHaveBeenCalled();
  await enter(); expect(chatEvents).toHaveBeenCalledTimes(1);
});
it.each(['paused', 'revoked', 'invalid'] as const)('keeps %s invite input and sending disabled', async (key) => {
  await render(false, key);
  expect(field().disabled).toBe(true); expect(sendButton().disabled).toBe(true);
  await act(async () => container.querySelector<HTMLButtonElement>('.suggestion')?.click());
  expect(chatEvents).not.toHaveBeenCalled();
});
it('keeps follower input and sending disabled', async () => {
  election.leader = false; await render(false);
  expect(field().disabled).toBe(true); expect(sendButton().disabled).toBe(true);
  await act(async () => container.querySelector<HTMLButtonElement>('.suggestion')?.click());
  expect(chatEvents).not.toHaveBeenCalled();
});
it('retains ordinary Send and Shift+Enter behavior when no send wait applies', async () => {
  await render(false); await type('Normal draft'); await enter({ shiftKey: true });
  expect(chatEvents).not.toHaveBeenCalled(); expect(sendButton().disabled).toBe(false);
  await act(async () => sendButton().click()); expect(chatEvents).toHaveBeenCalledTimes(1);
});

it('Escape dismisses the limits sheet without changing the chat', async () => {
  await render(false); await type('Keep this draft');
  await act(async () => container.querySelector<HTMLButtonElement>('.meters')!.click());
  expect(container.querySelector('.sheet')).not.toBeNull();
  await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })));
  expect(container.querySelector('.sheet')).toBeNull(); expect(field().value).toBe('Keep this draft');
});
it('a pending reply uses the same not-answering line as the degraded session', async () => {
  let finish!: () => void;
  vi.mocked(chatEvents).mockImplementationOnce(async function* (...args) {
    args[7]?.(true);
    await new Promise<void>((resolve) => { finish = resolve; });
    yield* [];
  });
  await render(false); await type('Hello'); await enter();
  expect(container.textContent).toContain('Waiting for');
  const current = { ...live, pathOk: false, meOk: false };
  await host.render(createElement(Chat, { live: current, state: { name: 'degraded', live: current, reason: 'path' }, dispatch() {}, onRedial() {} }));
  expect(container.querySelector('.composer')!.textContent).toContain('not answering');
  expect(container.querySelector('.composer')!.textContent).not.toContain('to load');
  await act(async () => finish());
});
it('a revoked invite offers only a new code, and an empty card teaches ic2', async () => {
  await host.render(createElement(Connect, { state: { name: 'disconnected', reason: { title: 'This invite was revoked', detail: 'Ask for a new code.', fatal: true } }, dispatch() {} }));
  expect([...container.querySelectorAll('.connect-actions button')].map(button => button.textContent)).toEqual(['Paste a new code']);
  expect(container.querySelector('.failure')!.textContent).toContain('Paste a new code');
  expect(container.querySelector('textarea')!.placeholder).toBe('ic2.…');
});

it('Save stops after 30 seconds even while waiting through a gallery cooldown', async () => {
  vi.useFakeTimers();
  // Exercise the timeout fallback so this deadline uses the test clock too.
  const descriptor = Object.getOwnPropertyDescriptor(AbortSignal, 'timeout');
  Object.defineProperty(AbortSignal, 'timeout', { configurable: true, value: undefined });
  try {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ error: { code: 'concurrency_limited' } }), { status: 429, headers: { 'Retry-After': '60' } }));
    const saving = saveImage({ id: 'saved-image' } as ImageJob, { ...live, transport: fakeTransport(fetch) });
    const stopped = expect(saving).rejects.toBeDefined();
    await vi.advanceTimersByTimeAsync(29_999); expect(fetch).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1); await stopped;
    expect(fetch).toHaveBeenCalledTimes(1);
  } finally { if (descriptor) Object.defineProperty(AbortSignal, 'timeout', descriptor); vi.useRealTimers(); }
});
