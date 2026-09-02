import { beforeEach, describe, expect, it } from 'vitest';
import { forget, load, prune, save, titleFrom, type Conversation } from './storage';

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
