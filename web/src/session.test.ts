// The session machine. These are the promises that used to be scattered flags: a transport is
// never left open, a stale measurement is never presented as current, and a revoked key is never
// swallowed by a background refresh.
import { describe, expect, it } from 'vitest';
import type { Me } from './api';
import {
  ago,
  dropped,
  IDLE,
  live,
  pathLine,
  reduce,
  waitText,
  type Live,
  type SessionEvent,
  type SessionState,
} from './session';
import type { PingResult, Transport } from './transport';

function fakeTransport(): Transport & { closes: number } {
  const t = {
    kind: 'tunnel' as const,
    closes: 0,
    fetch: () => Promise.resolve(new Response('{}')),
    ping: async () => null,
    close(): void {
      t.closes++;
    },
  };
  return t;
}

const ME: Me = {
  key: { id: 'k_1', name: 'alice', status: 'active' },
  limits: { rpm: 20, tpm: 20000, max_concurrent: 1, max_output_tokens: 2048, max_context: 0, daily_tokens: 200000 },
  usage: { rpm_used: 0, tpm_used: 0, today_tokens: 0, in_flight: 0 },
  host: {
    name: 'desk',
    upstream: { kind: 'llama.cpp', healthy: true, model_context: 8192 },
    models: ['m'],
    relay: { region: 'sfo' },
  },
};

const PATH: PingResult = { rttMs: 32, via: 'DERP(sfo)', direct: false };

function liveOn(t: Transport, over: Partial<Live> = {}): Live {
  return {
    transport: t,
    secret: 's',
    addr: 'tcADDR',
    me: ME,
    mode: 'tunnel',
    path: PATH,
    pathAt: 1000,
    pathOk: true,
    ephemeral: false,
    ...over,
  };
}

/** Runs a sequence the way App.tsx does: reduce, then close whatever the transition dropped. */
function run(events: SessionEvent[], from: SessionState = IDLE): SessionState {
  let s = from;
  for (const e of events) {
    const next = reduce(s, e);
    for (const t of dropped(s, e, next)) t.close();
    s = next;
  }
  return s;
}

describe('connecting', () => {
  it('walks idle → loadingWasm → connecting → verifying → connected', () => {
    const t = fakeTransport();
    const names: string[] = [];
    let s: SessionState = IDLE;
    for (const e of [
      { t: 'start', mode: 'tunnel' },
      { t: 'wasmProgress', pct: 40 },
      { t: 'wasmLoaded' },
      { t: 'sessionUp', transport: t },
      { t: 'verified', live: liveOn(t) },
    ] as SessionEvent[]) {
      s = reduce(s, e);
      names.push(s.name);
    }
    expect(names).toEqual(['loadingWasm', 'loadingWasm', 'connecting', 'verifying', 'connected']);
    expect(t.closes).toBe(0);
  });

  it('skips the wasm step in direct mode', () => {
    expect(reduce(IDLE, { t: 'start', mode: 'direct' }).name).toBe('connecting');
  });
});

// Promise 3, the whole of it: the transport is closed by the transition, not by a call site.
describe('no leaked sessions', () => {
  it('closes the provisional transport exactly once when /me fails', () => {
    const t = fakeTransport();
    const s = run([
      { t: 'start', mode: 'tunnel' },
      { t: 'wasmLoaded' },
      { t: 'sessionUp', transport: t },
      { t: 'meError', error: { title: 'no', detail: 'no' } },
    ]);
    expect(s.name).toBe('disconnected');
    expect(t.closes).toBe(1);
  });

  it('closes a connect attempt that lands after a newer one superseded it', () => {
    const old = fakeTransport();
    const fresh = fakeTransport();
    let s = run([
      { t: 'start', mode: 'tunnel' },
      { t: 'wasmLoaded' },
      { t: 'sessionUp', transport: old },
    ]);
    // The reader pressed Connect again; the old attempt is still in flight behind us.
    s = run([{ t: 'start', mode: 'tunnel' }], s);
    expect(old.closes).toBe(1);
    s = run([{ t: 'sessionUp', transport: old }], s); // the stale dial finally resolves
    expect(old.closes).toBe(2);
    expect(s.name).toBe('loadingWasm');

    s = run([{ t: 'wasmLoaded' }, { t: 'sessionUp', transport: fresh }, { t: 'verified', live: liveOn(fresh) }], s);
    expect(s.name).toBe('connected');
    expect(fresh.closes).toBe(0);
  });

  it('closes a verified payload that arrives for a session we already left', () => {
    const t = fakeTransport();
    const s = run([{ t: 'verified', live: liveOn(t) }], { name: 'disconnected', reason: null });
    expect(s.name).toBe('disconnected');
    expect(t.closes).toBe(1);
  });

  it('closes the live transport when the reader disconnects', () => {
    const t = fakeTransport();
    const s = run([{ t: 'verified', live: liveOn(t) }], { name: 'verifying', transport: t });
    run([{ t: 'abort', error: null }], s);
    expect(t.closes).toBe(1);
  });
});

// Promise 4: degraded is a state, and it carries the last good measurement rather than showing it.
describe('degraded states', () => {
  const t = fakeTransport();
  const connected: SessionState = { name: 'connected', live: liveOn(t) };

  it('a failed ping degrades the path and keeps the last good number, dated', () => {
    const s = reduce(connected, { t: 'pingFail' });
    expect(s).toMatchObject({ name: 'degraded', reason: 'path' });
    const l = live(s)!;
    expect(pathLine(l, 1000 + 120_000)).toBe('path unknown · last 32 ms 2 min ago');
    expect(pathLine(liveOn(t), 1000)).toBe('relayed via sfo · 32 ms');
  });

  it('a later good ping clears the degradation', () => {
    const failed = reduce(connected, { t: 'pingFail' });
    const back = reduce(failed, { t: 'pingOk', path: { ...PATH, rttMs: 40 }, at: 5000 });
    expect(back.name).toBe('connected');
    expect(pathLine(live(back)!, 5000)).toBe('relayed via sfo · 40 ms');
  });

  it('an unhealthy engine in /me degrades without dropping the session', () => {
    const sick = { ...ME, host: { ...ME.host, upstream: { ...ME.host.upstream, healthy: false } } };
    const s = reduce(connected, { t: 'meOk', me: sick });
    expect(s).toMatchObject({ name: 'degraded', reason: 'engine' });
    expect(live(s)).not.toBeNull();
  });

  it('an upstream_down while streaming stops the header claiming the engine is up', () => {
    const s = reduce(connected, {
      t: 'streamError',
      code: 'upstream_down',
      error: { title: 'offline', detail: 'x' },
    });
    expect(s).toMatchObject({ name: 'degraded', reason: 'engine' });
  });

  it('reports both when the path and the engine are gone', () => {
    const sick = { ...ME, host: { ...ME.host, upstream: { ...ME.host.upstream, healthy: false } } };
    const s = reduce(reduce(connected, { t: 'pingFail' }), { t: 'meOk', me: sick });
    expect(s).toMatchObject({ name: 'degraded', reason: 'both' });
  });
});

// Promise 5.
describe('a key that stops working mid-session', () => {
  const t = fakeTransport();
  const connected: SessionState = { name: 'connected', live: liveOn(t) };

  it('returns to connect with the reason when the refresh says the invite is dead', () => {
    for (const title of ['This invite was revoked', 'Your access is paused']) {
      const s = reduce(connected, { t: 'meError', error: { title, detail: 'd', fatal: true } });
      expect(s).toEqual({ name: 'disconnected', reason: { title, detail: 'd', fatal: true } });
    }
  });

  it('keeps the last snapshot when a refresh fails transiently', () => {
    const s = reduce(connected, { t: 'meError', error: { title: 'The connection to the host broke', detail: 'x' } });
    expect(s).toBe(connected);
    expect(live(s)?.me).toBe(ME);
  });
});

describe('elapsed time, in words', () => {
  it('dates a measurement', () => {
    expect(ago(0)).toBe('0 s ago');
    expect(ago(45_000)).toBe('45 s ago');
    expect(ago(120_000)).toBe('2 min ago');
    expect(ago(7_200_000)).toBe('2 h ago');
  });

  // Promise 8: over ten minutes, a countdown in seconds is not a thing anyone can act on.
  it('renders a wait as seconds, then as a duration', () => {
    expect(waitText(42_000)).toBe('42s');
    expect(waitText(600_000)).toBe('600s');
    expect(waitText(601_000)).toBe('10 min');
    expect(waitText(3_600_000)).toBe('60 min');
    expect(waitText(9_000_000)).toBe('3 h');
  });
});
