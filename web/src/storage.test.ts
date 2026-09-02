import { beforeEach, describe, expect, it } from 'vitest';
import {
  dropLegacyHistory,
  forget,
  hostScope,
  isAnswer,
  KEYS,
  load,
  modelFor,
  prune,
  save,
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
    expect(titleFrom('   ')).toBe('New chat');
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
    expect(scopedKeys(A).conversations).toBe(`${KEYS.conversations}.${A}`);
    expect(scopedKeys(A).conversations).not.toContain('tcHOSTA');
  });

  it('connecting to a different host starts with that host\u2019s own empty list', () => {
    save(scopedKeys(A).conversations, [{ id: 'c1', title: 'mine', createdAt: 0, updatedAt: 0, messages: [] }]);
    expect(load<Conversation[]>(scopedKeys(A).conversations, [])).toHaveLength(1);
    expect(load<Conversation[]>(scopedKeys(B).conversations, [])).toHaveLength(0);
    expect(load<Conversation[]>(scopedKeys(A2).conversations, [])).toHaveLength(0);
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
