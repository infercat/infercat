import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  KEYS,
  adoptInviteScope,
  carriedAfter,
  chatsChanged,
  countChats,
  deleteChat,
  dialsOnArrival,
  dropLegacyHistory,
  forget,
  hostScope,
  isAnswer,
  load,
  loadChats,
  mergeChats,
  modelFor,
  electStore,
  rememberLeft,
  reopenChats,
  undelivered,
  newConversation,
  save,
  saveChat,
  scopedKeys,
  titleFrom,
  type Conversation,
  type LastHost,
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

});

// Promise 7: two hosts, or two invites on one host, never see each other's history or settings.
describe('host-scoped storage', () => {

  it('gives every host its own namespace, stably — and one host one drawer, whatever the code (024)', () => {
    expect(hostScope('tcHOST')).toBe(hostScope('tcHOST'));
    expect(hostScope('tcHOST')).not.toBe(hostScope('tcOTHER'));
    // A revoke and a re-issue, or a rotation, is the same host: the same chats.
    const keys = scopedKeys(hostScope('tcHOST'));
    saveChat(hostScope('tcHOST'), { id: 'c1', title: 't', createdAt: 1, updatedAt: 1, messages: [{ id: 'm', role: 'user', content: 'x' }] });
    expect(load<string[]>(keys.index, [])).toEqual(['c1']);
    expect(countChats(hostScope('tcHOST'))).toBe(1);
  });

  it('keeps the address out of the key while still keying on it', () => {
    const scope = hostScope('tcSECRETLOOKINGADDRESS');
    expect(scope).not.toContain('tcSECRET');
    expect(scope.length).toBeLessThan(12);
  });

  it('connecting to a different host starts with that host’s own empty list', () => {
    saveChat(hostScope('tcHOST'), { id: 'c1', title: 'mine', createdAt: 1, updatedAt: 1, messages: [{ id: 'm', role: 'user', content: 'x' }] });
    expect(loadChats(hostScope('tcOTHER'))).toHaveLength(0);
    expect(loadChats(hostScope('tcHOST'))).toHaveLength(1);
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
  const S = hostScope('tcHOST');
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
  // 022 promise 2: the mark derives from delivery. No flag on the turn, so nothing persisted can
  // contradict what the next request will actually carry.
  describe('undelivered turns derive from delivery', () => {
    const turn = (id: string): Message => ({ id, role: 'user', content: id });
    const answer = (id: string, status: Message['status'] | undefined): Message => ({
      id, role: 'assistant', content: status === 'no_answer' || status === undefined ? '' : 'a', ...(status ? { status } : {}),
    });
    const ids = (thread: Message[]) => [...undelivered(thread)].sort();

    it('marks exactly the turns after the last reply that got through', () => {
      const thread = [
        turn('u1'), answer('a1', 'complete'),
        turn('u2'), answer('a2', 'stopped'),
        turn('u3'), answer('a3', 'complete'),
        turn('u4'), answer('a4', 'interrupted'),
      ];
      expect(ids(thread)).toEqual(['u4']);
    });

    it('clears a turn the moment a later send succeeds with it in the history (ZEBRA)', () => {
      // Sent while paused, refused; the reader resumes and sends the next message, whose history
      // holds ZEBRA — the model recalls it, so nothing about that turn may say "not delivered".
      const thread = [turn('zebra'), answer('a1', 'interrupted'), turn('u2'), answer('a2', 'complete')];
      expect(ids(thread)).toEqual([]);
    });

    it('keeps every turn since the last delivery when several fail in a row', () => {
      const thread = [turn('u1'), answer('a1', 'complete'), turn('u2'), answer('a2', 'interrupted'), turn('u3'), answer('a3', 'interrupted')];
      expect(ids(thread)).toEqual(['u2', 'u3']);
    });

    it('counts a reply with no answer in it as a request that got through', () => {
      // The host said done; the model only thought. The reply row says so; the turn was asked.
      expect(ids([turn('u1'), answer('a1', 'no_answer')])).toEqual([]);
    });

    it('holds a turn whose reply has not ended, and one with no reply at all', () => {
      expect(ids([turn('u1')])).toEqual(['u1']);
      expect(ids([turn('u1'), answer('a1', undefined)])).toEqual(['u1']); // the view hides it while answering
    });

    it('ignores the flag an older release stored on the turn', () => {
      const marked = { ...turn('u1'), pending: true } as unknown as Message;
      saveChat(S, { ...chat('c1', 10, msg('m1', 'x')), messages: [marked, answer('a1', 'complete'), { ...turn('u2'), pending: true } as unknown as Message, answer('a2', 'complete')] });
      expect(undelivered(loadChats(S)[0]?.messages ?? [])).toEqual(new Set());
    });
  });
});

// 022 promise 5: Disconnect outlives the tab.
describe('a reader who left', () => {
  const HOST: LastHost = { name: 'desk', scope: 'abc' };

  it('is remembered beside the host, and a fresh verify forgets it', () => {
    save(KEYS.lastHost, HOST);
    rememberLeft();
    expect(load<LastHost | null>(KEYS.lastHost, null)).toEqual({ ...HOST, left: true });
    save(KEYS.lastHost, HOST); // what a successful connect writes
    expect(load<LastHost | null>(KEYS.lastHost, null)?.left).toBeUndefined();
  });

  it('does not write a host record that does not exist', () => {
    rememberLeft();
    expect(load(KEYS.lastHost, null)).toBeNull();
  });

  it('is not dialled again on arrival — unless the code came by link', () => {
    expect(dialsOnArrival({ ...HOST, left: true }, false)).toBe(false);
    expect(dialsOnArrival({ ...HOST, left: true }, true)).toBe(true);
    expect(dialsOnArrival(HOST, false)).toBe(true);
    expect(dialsOnArrival(null, false)).toBe(true);
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

// 024 promise 1: chats an earlier build kept under the invite move into the host scope, once.
describe('adopting an earlier build’s invite-scoped chats', () => {
  const conv = (id: string, at: number): Conversation => ({ id, title: id, createdAt: at, updatedAt: at, messages: [{ id: `${id}m`, role: 'user', content: id }] });
  // What a pre-024 build wrote: FNV over "addr keyId" — reproduced here so the fixture is real-shaped.
  const invite = (addr: string, keyId: string) => {
    let h = 0x811c9dc5;
    for (const ch of `${addr} ${keyId}`) { h ^= ch.codePointAt(0) ?? 0; h = Math.imul(h, 0x01000193) >>> 0; }
    return h.toString(36);
  };

  it('moves the chats and the settings over, merges with what the host scope has, and runs once', () => {
    const old = invite('tcHOST', 'k_old');
    saveChat(old, conv('a', 1));
    saveChat(old, conv('b', 2));
    save(scopedKeys(old).settings, { model: 'm', systemPrompt: 'be brief', temperature: 0.5 });
    saveChat(hostScope('tcHOST'), conv('b', 3)); // already there under the host: kept, not duplicated
    adoptInviteScope('tcHOST', 'k_old');
    const ids = loadChats(hostScope('tcHOST')).map((c) => c.id);
    expect(ids.sort()).toEqual(['a', 'b']);
    expect(loadChats(hostScope('tcHOST')).find((c) => c.id === 'b')?.updatedAt).toBe(3);
    expect(load(scopedKeys(hostScope('tcHOST')).settings, null)).toEqual({ model: 'm', systemPrompt: 'be brief', temperature: 0.5 });
    expect(load(scopedKeys(old).index, null)).toBeNull();
    expect(load(scopedKeys(old).conv('a'), null)).toBeNull();
    adoptInviteScope('tcHOST', 'k_old'); // nothing left to move: nothing changes
    expect(loadChats(hostScope('tcHOST')).map((c) => c.id).sort()).toEqual(['a', 'b']);
  });

  it('is a no-op for a host this build has always known', () => {
    saveChat(hostScope('tcHOST'), conv('a', 1));
    adoptInviteScope('tcHOST', 'k_new');
    expect(loadChats(hostScope('tcHOST')).map((c) => c.id)).toEqual(['a']);
  });
});

// 024 promise 3: the row under a turn a later question carried stops saying "Not sent".
describe('a failed reply whose turn was carried anyway', () => {
  const turn = (id: string): Message => ({ id, role: 'user', content: id });
  const failed = (id: string): Message => ({ id, role: 'assistant', content: '', status: 'interrupted', note: 'Not sent — your invite is paused.' });
  const answer = (id: string): Message => ({ id, role: 'assistant', content: 'a', status: 'complete' });

  it('is softened once the next question got through, and not before', () => {
    expect([...carriedAfter([turn('zebra'), failed('r')])]).toEqual([]);
    expect([...carriedAfter([turn('zebra'), failed('r'), turn('q'), answer('a')])]).toEqual(['r']);
  });

  it('never touches a reply that answered, or a failure with nothing delivered after it', () => {
    expect([...carriedAfter([turn('u1'), answer('a1'), turn('u2'), failed('r2')])]).toEqual([]);
  });
});
