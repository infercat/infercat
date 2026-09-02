import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  KEYS,
  chatsChanged,
  countChats,
  deleteChat,
  dropLegacyHistory,
  forget,
  hostScope,
  isAnswer,
  load,
  loadChats,
  mergeChats,
  modelFor,
  electStore,
  reopenChats,
  settlePending,
  newConversation,
  prune,
  save,
  saveChat,
  scopedKeys,
  titleFrom,
  type Conversation,
  type Message,
} from './storage';

function stubStorage(impl: Partial<Storage> = {}): Map<string, string> {
  const map = new Map<string, string>();
  const base: Storage = {
    get length() {
      return map.size;
    },
    clear: () => map.clear(),
    getItem: (k) => map.get(k) ?? null,
    key: (i) => [...map.keys()][i] ?? null,
    removeItem: (k) => void map.delete(k),
    setItem: (k, v) => void map.set(k, v),
  };
  Object.defineProperty(globalThis, 'localStorage', {
    value: { ...base, ...impl },
    configurable: true,
  });
  return map;
}

beforeEach(() => stubStorage());

describe('storage', () => {
  it('round-trips a value', () => {
    save('k', { a: 1 });
    expect(load('k', null)).toEqual({ a: 1 });
  });

  it('falls back when nothing is stored or the stored value is corrupt', () => {
    expect(load('missing', 'fallback')).toBe('fallback');
    globalThis.localStorage.setItem('bad', '{not json');
    expect(load('bad', 'fallback')).toBe('fallback');
  });

  it('survives a browser that refuses to store (private mode, quota)', () => {
    stubStorage({
      setItem: () => {
        throw new DOMException('QuotaExceededError');
      },
      getItem: () => {
        throw new DOMException('SecurityError');
      },
    });
    expect(() => save('k', 1)).not.toThrow();
    expect(load('k', 'fallback')).toBe('fallback');
    expect(() => forget('k')).not.toThrow();
  });

  it('forgets what the user asked it to forget', () => {
    save('bn.invite', 'x');
    save('bn.privateKey', 'y');
    forget('bn.invite', 'bn.privateKey');
    expect(load('bn.invite', null)).toBeNull();
    expect(load('bn.privateKey', null)).toBeNull();
  });
});

describe('conversation helpers', () => {
  it('titles a chat from its first line, shortened', () => {
    expect(titleFrom('  How do tunnels work?\nmore  ')).toBe('How do tunnels work?');
    expect(titleFrom('x'.repeat(80))).toHaveLength(41);
    expect(titleFrom('   ')).toBe('Untitled chat');
  });

  it('drops empty conversations and caps history', () => {
    const conv = (id: string, n: number): Conversation => ({
      id,
      title: id,
      createdAt: 0,
      updatedAt: 0,
      messages: Array.from({ length: n }, (_, i) => ({ id: `${id}${i}`, role: 'user', content: 'x' })),
    });
    const list = [conv('a', 1), conv('b', 0), ...Array.from({ length: 60 }, (_, i) => conv(`c${i}`, 1))];
    const kept = prune(list, 50);
    expect(kept).toHaveLength(50);
    expect(kept.map((c) => c.id)).not.toContain('b');
    expect(kept[0]?.id).toBe('a');
  });
});

// Promise 7: two hosts, or two invites on one host, never see each other's history or settings.
describe('host-scoped storage', () => {
  const A = hostScope('tcHOSTA', 'k_1');
  const B = hostScope('tcHOSTB', 'k_1');
  const A2 = hostScope('tcHOSTA', 'k_2');

  it('gives every host+invite pair its own namespace, stably', () => {
    expect(A).not.toBe(B);
    expect(A).not.toBe(A2);
    expect(hostScope('tcHOSTA', 'k_1')).toBe(A);
    expect(A).toMatch(/^[0-9a-z]+$/); // short and safe in a storage key
  });

  it('keeps the address out of the key while still keying on it', () => {
    expect(scopedKeys(A).index).toBe(`${KEYS.conversations}.${A}`);
    expect(scopedKeys(A).index).not.toContain('tcHOSTA');
    expect(scopedKeys(A).conv('c1')).not.toContain('tcHOSTA');
  });

  it('connecting to a different host starts with that host\u2019s own empty list', () => {
    saveChat(A, { id: 'c1', title: 'mine', createdAt: 0, updatedAt: 0, messages: [{ id: 'm', role: 'user', content: 'hi' }] });
    expect(loadChats(A)).toHaveLength(1);
    expect(loadChats(B)).toHaveLength(0);
    expect(loadChats(A2)).toHaveLength(0);
  });

  it('drops the pre-scope history rather than handing it to whichever host connects first', () => {
    save(KEYS.conversations, [{ id: 'old' }]);
    save(KEYS.settings, { model: 'gone' });
    dropLegacyHistory();
    expect(load(KEYS.conversations, null)).toBeNull();
    expect(load(KEYS.settings, null)).toBeNull();
  });

  it('resets a persisted model the new host does not share', () => {
    expect(modelFor('gemma', ['gemma', 'qwen'])).toBe('gemma');
    expect(modelFor('gemma', ['deepseek'])).toBeNull();
    expect(modelFor(null, ['deepseek'])).toBeNull();
    expect(modelFor('gemma', [])).toBeNull();
  });
});

describe('what counts as context for the next message', () => {
  const m = (over: Partial<Message>): Message => ({ id: 'x', role: 'assistant', content: 'hi', ...over });

  it('keeps answers and stopped answers, drops the turns that never answered', () => {
    expect(isAnswer(m({ status: 'complete' }))).toBe(true);
    expect(isAnswer(m({ status: 'stopped' }))).toBe(true);
    expect(isAnswer(m({ status: 'interrupted' }))).toBe(false);
    expect(isAnswer(m({ status: 'no_answer' }))).toBe(false);
    expect(isAnswer(m({ role: 'user', status: undefined }))).toBe(true);
  });
});

// 014 promise 4: one conversation is one key, so two tabs cannot overwrite each other's history,
// and the reload rule folded in from 013.
describe('multi-tab conversation storage', () => {
  const S = hostScope('tcHOST', 'k_1');
  const msg = (id: string, content: string): Message => ({ id, role: 'user', content });
  const chat = (id: string, at: number, ...ms: Message[]): Conversation => ({
    id,
    title: id,
    createdAt: at,
    updatedAt: at,
    messages: ms,
  });

  it('writes each conversation under its own key, and an index of ids', () => {
    saveChat(S, chat('c1', 10, msg('m1', 'one')));
    saveChat(S, chat('c2', 20, msg('m2', 'two')));
    expect(load<string[]>(scopedKeys(S).index, [])).toEqual(['c2', 'c1']);
    expect(load<Conversation | null>(scopedKeys(S).conv('c1'), null)?.messages).toHaveLength(1);
    expect(countChats(S)).toBe(2);
  });

  it('never loses the other tab’s last exchange', () => {
    // Two tabs load the same two chats.
    saveChat(S, chat('c1', 10, msg('m1', 'one')));
    saveChat(S, chat('c2', 10, msg('m2', 'two')));
    const tabA = loadChats(S);
    const tabB = loadChats(S);
    // Tab B has an exchange in c2 that tab A has never seen.
    saveChat(S, { ...chat('c2', 30, msg('m2', 'two'), msg('m3', 'three')) });
    // Tab A hears about it and re-reads before writing its own, older, copy.
    const merged = mergeChats(S, tabA);
    expect(merged.find((c) => c.id === 'c2')?.messages).toHaveLength(2);
    expect(tabB).toHaveLength(2); // the other tab's own view is untouched
    // Tab A writing its unrelated conversation leaves tab B's turn where it is.
    saveChat(S, { ...chat('c1', 40, msg('m1', 'one'), msg('m4', 'four')) });
    const after = loadChats(S);
    expect(after.find((c) => c.id === 'c2')?.messages).toHaveLength(2);
    expect(after.find((c) => c.id === 'c1')?.messages).toHaveLength(2);
  });

  it('keeps a conversation this tab is writing right now, even if disk has not seen it', () => {
    saveChat(S, chat('c1', 10, msg('m1', 'one')));
    const mine = [chat('draft', 99, msg('m9', 'live')), ...loadChats(S)];
    const merged = mergeChats(S, mine);
    expect(merged.map((c) => c.id)).toEqual(['draft', 'c1']);
  });

  it('recognises a storage event about this host and ignores everyone else’s', () => {
    expect(chatsChanged(S, `${KEYS.conversations}.${S}.c1`)).toBe(true);
    expect(chatsChanged(S, `${KEYS.conversations}.${S}`)).toBe(true);
    expect(chatsChanged(S, `${KEYS.conversations}.other.c1`)).toBe(false);
    expect(chatsChanged(S, KEYS.invite)).toBe(false);
    expect(chatsChanged(S, null)).toBe(false);
  });

  it('deletes both the conversation and its place in the index', () => {
    saveChat(S, chat('c1', 10, msg('m1', 'one')));
    deleteChat(S, 'c1');
    expect(loadChats(S)).toHaveLength(0);
    expect(load(scopedKeys(S).conv('c1'), null)).toBeNull();
  });

  // Folded in from 013: a reload during generation keeps the turn and tells the truth about it.
  it('reopens a reply that was still streaming as interrupted, with its partial text', () => {
    saveChat(S, {
      ...chat('c1', 10, msg('m1', 'ask')),
      messages: [
        msg('m1', 'ask'),
        { id: 'r1', role: 'assistant', content: 'half an ans' },
      ],
    });
    // Reading leaves it as it is: another tab may be writing it (020 promise 6).
    expect(loadChats(S)[0]?.messages[1]?.status).toBeUndefined();
    // Taking the store is what makes it an orphan.
    const reply = reopenChats(loadChats(S))[0]?.messages[1];
    expect(reply?.content).toBe('half an ans');
    expect(reply?.status).toBe('interrupted');
    expect(reply?.note).toContain('closed or reloaded');
    expect(isAnswer(reply as Message)).toBe(false);
  });

  it('splits the pre-014 single-array layout into one key each, once', () => {
    save(scopedKeys(S).index, [chat('c1', 10, msg('m1', 'one')), chat('c2', 20, msg('m2', 'two'))]);
    const loaded = loadChats(S);
    expect(loaded.map((c) => c.id)).toEqual(['c1', 'c2']);
    expect(load<string[]>(scopedKeys(S).index, [])).toEqual(['c1', 'c2']);
    expect(loadChats(S).map((c) => c.id)).toEqual(['c1', 'c2']); // idempotent
  });

  // 020 promise 1, the blocker: "Not delivered" under an answered message, persisted for ever.
  describe('the pending mark is per turn', () => {
    const turn = (id: string, pending?: boolean): Message => ({ id, role: 'user', content: id, ...(pending ? { pending } : {}) });
    const answer = (id: string, status: Message['status']): Message => ({ id, role: 'assistant', content: status === 'no_answer' ? '' : 'a', status });

    it('clears exactly the turns that have their answer, and keeps the one that does not', () => {
      const thread = [
        turn('u1', true), answer('a1', 'complete'),
        turn('u2', true), answer('a2', 'stopped'),
        turn('u3', true), answer('a3', 'complete'),
        turn('u4', true), { id: 'a4', role: 'assistant' as const, content: '', status: 'interrupted' as const },
      ];
      const settled = settlePending(thread);
      expect(settled.filter((m) => m.pending === true).map((m) => m.id)).toEqual(['u4']);
    });

    it('does not call a reply with no answer in it a delivery', () => {
      expect(settlePending([turn('u1', true), answer('a1', 'no_answer')])[0]?.pending).toBe(true);
      expect(settlePending([turn('u1', true)])[0]?.pending).toBe(true); // nothing after it yet
    });

    it('is the same object when there is nothing to settle', () => {
      const thread = [turn('u1'), answer('a1', 'complete')];
      expect(settlePending(thread)).toBe(thread);
    });

    it('repairs a transcript an older release marked, on load', () => {
      saveChat(S, { ...chat('c1', 10, msg('m1', 'x')), messages: [turn('u1', true), answer('a1', 'complete'), turn('u2', true), answer('a2', 'complete')] });
      expect(loadChats(S)[0]?.messages.some((m) => m.pending === true)).toBe(false);
    });
  });
});

// 020 promise 6: one tab writes. The same Web Lock election as the tunnel identity, with a queue
// and a steal, stubbed the way transport.test.ts stubs it.
describe('the store leader', () => {
  function fakeLocks() {
    const held = new Map<string, { reject: (e: unknown) => void }>();
    const queue = new Map<string, (() => void)[]>();
    type Cb = (lock: { name: string } | null) => unknown;
    const grant = (name: string, cb: Cb, resolve: (v: unknown) => void, reject: (e: unknown) => void) => {
      held.set(name, { reject });
      Promise.resolve(cb({ name })).then(
        (v) => {
          held.delete(name);
          resolve(v);
          queue.get(name)?.shift()?.();
        },
        reject,
      );
    };
    return {
      request(name: string, a: unknown, b?: unknown) {
        const opts = (typeof a === 'function' ? {} : a) as { ifAvailable?: boolean; steal?: boolean };
        const cb = (typeof a === 'function' ? a : b) as Cb;
        return new Promise<unknown>((resolve, reject) => {
          const holder = held.get(name);
          if (holder && opts.steal) {
            held.delete(name);
            holder.reject(new DOMException('Lock broken by another request with the steal option.', 'AbortError'));
            grant(name, cb, resolve, reject);
          } else if (holder && opts.ifAvailable) {
            Promise.resolve(cb(null)).then(resolve, reject);
          } else if (holder) {
            queue.set(name, [...(queue.get(name) ?? []), () => grant(name, cb, resolve, reject)]);
          } else {
            grant(name, cb, resolve, reject);
          }
        });
      },
    };
  }
  const tick = () => new Promise((r) => setTimeout(r, 0));
  afterEach(() => Reflect.deleteProperty(globalThis, 'navigator'));

  it('makes the first tab the leader and the second a follower, and lets the second take over', async () => {
    Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { locks: fakeLocks() } });
    const a: boolean[] = [];
    const b: boolean[] = [];
    electStore('s', (l) => a.push(l));
    await tick();
    expect(a).toEqual([true]);
    const tabB = electStore('s', (l) => b.push(l));
    await tick();
    expect(b).toEqual([false]);
    tabB.takeOver();
    await tick();
    expect(a).toEqual([true, false]);
    expect(b).toEqual([false, true]);
  });

  it('promotes the follower when the leader lets go, without anyone asking', async () => {
    Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { locks: fakeLocks() } });
    const a: boolean[] = [];
    const b: boolean[] = [];
    const tabA = electStore('s', (l) => a.push(l));
    await tick();
    electStore('s', (l) => b.push(l));
    await tick();
    expect(b).toEqual([false]);
    tabA.release();
    await tick();
    expect(b).toEqual([false, true]);
    expect(a).toEqual([true]); // letting go is not losing
  });

  it('leads at once where Web Locks do not exist', () => {
    const a: boolean[] = [];
    electStore('s', (l) => a.push(l));
    expect(a).toEqual([true]);
  });
});

describe('the store leader’s me key', () => {
  it('lives under the host scope, beside the chats and settings', () => {
    expect(scopedKeys('abc').me).toBe('bn.me.abc');
  });

});

// 014 promise 17: "New chat" is the button that makes one. A list of rows named after the button
// says nothing about what is in them.
describe('naming an empty conversation', () => {
  it('does not name it after the button that made it', () => {
    expect(newConversation().title).toBe('Untitled chat');
    expect(titleFrom('   ')).toBe('Untitled chat');
    expect(titleFrom('Ask me about tunnels')).toBe('Ask me about tunnels');
  });
});
