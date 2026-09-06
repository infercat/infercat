// Promise 6 (abort during the dial) and promise 10 (two tabs, one stored identity), against the
// same fake tunnel the connect flow uses.
import { afterEach, describe, expect, it } from 'vitest';
import { FakeSession, makeFakeTunnel } from '../../dev/fake-infercat-tunnel.ts';
import { claimTunnelIdentity, dialOrAbort, TunnelTransport } from './index';

const FAST = { connectMs: 4, tokenDelayMs: 0, ttftMs: 0 };

async function session(over = {}): Promise<FakeSession> {
  const s = await makeFakeTunnel({ ...FAST, ...over }).connect({ addr: 'tcFAKE' });
  return s as FakeSession;
}

describe('abort during the dial', () => {
  it('rejects immediately when the signal is already aborted, without dialling', async () => {
    const s = await session();
    const ac = new AbortController();
    ac.abort();
    await expect(dialOrAbort(s, 80, ac.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(s.dialled).toHaveLength(0);
  });

  it('rejects while the dial is still pending, and closes the conn when it lands', async () => {
    const s = await session({ dialMs: 300 });
    const ac = new AbortController();
    const started = Date.now();
    const dialing = dialOrAbort(s, 80, ac.signal);
    setTimeout(() => ac.abort(), 5);
    await expect(dialing).rejects.toMatchObject({ name: 'AbortError' });
    // The point of the promise: we did not wait out the dial.
    expect(Date.now() - started).toBeLessThan(200);

    // …and the conn that arrives afterwards is closed rather than left open on the host.
    await new Promise((r) => setTimeout(r, 400));
    expect(s.dialled).toHaveLength(1);
    expect(s.dialled[0]?.closedByCaller).toBe(true);
  });

  it('a request aborted mid-dial never reaches the host', async () => {
    const s = await session({ dialMs: 300 });
    const ac = new AbortController();
    const t = new TunnelTransport(s);
    const res = t.fetch('/me', { signal: ac.signal });
    setTimeout(() => ac.abort(), 5);
    await expect(res).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('dials normally when nothing aborts', async () => {
    const s = await session();
    const conn = await dialOrAbort(s, 80, new AbortController().signal);
    expect(conn).toBeTruthy();
    conn.close();
  });
});

describe('two tabs, one stored tunnel identity', () => {
  const held = new Set<string>();

  function stubLocks(): void {
    Object.defineProperty(globalThis, 'navigator', {
      configurable: true,
      value: {
        locks: {
          // ifAvailable: the second caller is handed null rather than queued behind the first.
          request(name: string, _opts: unknown, fn: (lock: object | null) => unknown) {
            if (held.has(name)) return Promise.resolve(fn(null));
            held.add(name);
            void fn({ name });
            return new Promise(() => {});
          },
        },
      },
    });
  }

  afterEach(() => {
    held.clear();
    Reflect.deleteProperty(globalThis, 'navigator');
  });

  it('gives the identity to the first tab and an ephemeral one to the second', async () => {
    stubLocks();
    expect(await claimTunnelIdentity()).toBe(true);
    expect(await claimTunnelIdentity()).toBe(false);
  });

  it('behaves as a single tab where Web Locks do not exist', async () => {
    expect(await claimTunnelIdentity()).toBe(true);
  });
});
