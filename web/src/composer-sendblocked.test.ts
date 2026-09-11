// @vitest-environment jsdom
import { fakeTransport, mountHost, typeInto } from './test/fakes';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { act, createElement } from 'react';
import Chat from './ui/Chat';
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
