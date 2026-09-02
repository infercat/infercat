// The session machine. These are the promises that used to be scattered flags: a transport is
// never left open, a stale measurement is never presented as current, and a revoked key is never
// swallowed by a background refresh.
import { describe, expect, it } from 'vitest';
import type { Me } from './api';
import {
  ago,
  compact,
  degradedLine,
  dropped,
  IDLE,
  live,
  meters,
  metersUnknown,
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
    meOk: true,
    paused: false,
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
    // 014 promise 3: the line names the host that stopped answering, and dates the last number.
    expect(pathLine(l, 1000 + 120_000)).toBe('desk — not answering · last 32 ms 2 min ago');
    expect(pathLine(liveOn(t), 1000)).toBe('relayed via San Francisco · 32 ms');
  });

  it('a later good ping clears the degradation', () => {
    const failed = reduce(connected, { t: 'pingFail' });
    const back = reduce(failed, { t: 'pingOk', path: { ...PATH, rttMs: 40 }, at: 5000 });
    expect(back.name).toBe('connected');
    expect(pathLine(live(back)!, 5000)).toBe('relayed via San Francisco · 40 ms');
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
    for (const title of ['This invite was revoked', 'The host does not recognise this invite']) {
      const s = reduce(connected, { t: 'meError', error: { title, detail: 'd', fatal: true } });
      expect(s).toEqual({ name: 'disconnected', reason: { title, detail: 'd', fatal: true } });
    }
  });

  // 014 promise 3: the snapshot is kept — losing it would blank the screen — but it stops being
  // presented as current, because "unknown" and "zero" are different claims.
  it('keeps the last snapshot when a refresh fails transiently, and stops calling it current', () => {
    const s = reduce(connected, { t: 'meError', error: { title: 'The connection to the host broke', detail: 'x' } });
    expect(s).toMatchObject({ name: 'degraded', reason: 'path' });
    expect(live(s)?.me).toBe(ME);
    expect(metersUnknown(live(s)!)).toBe(true);
    expect(pathLine(live(s)!, 1000)).toContain('not answering');
    // And a /me that answers again puts the numbers back.
    const back = reduce(s, { t: 'meOk', me: ME });
    expect(back.name).toBe('connected');
    expect(metersUnknown(live(back)!)).toBe(false);
  });
});

// 014 promise 2: a paused invite is a `degraded` reason, never an ejection. The chat, the
// transport and the reader's words all stay exactly where they are.
describe('a paused invite', () => {
  const t = fakeTransport();
  const connected: SessionState = { name: 'connected', live: liveOn(t) };
  const pausedErr = { title: 'Your invite is paused', detail: 'Ask them to resume it.', paused: true };

  it('degrades with reason key, keeps the session, and says so in the header', () => {
    const s = reduce(connected, { t: 'streamError', code: 'key_paused', error: pausedErr });
    expect(s).toMatchObject({ name: 'degraded', reason: 'key' });
    expect(live(s)?.transport).toBe(t);
    expect(t.closes).toBe(0);
    expect(degradedLine('key', ME)).toContain('paused your invite');
    expect(degradedLine('key', ME)).toContain('send it again once they resume');
  });

  it('clears the moment /me says the invite is active again', () => {
    const s = reduce(connected, { t: 'streamError', code: 'key_paused', error: pausedErr });
    const back = reduce(s, { t: 'meOk', me: ME });
    expect(back.name).toBe('connected');
  });

  it('stays paused while /me still says paused, and outranks the other reasons', () => {
    const stopped = { ...ME, key: { ...ME.key, status: 'paused' as const } };
    const s = reduce(reduce(connected, { t: 'pingFail' }), { t: 'meOk', me: stopped });
    expect(s).toMatchObject({ name: 'degraded', reason: 'key' });
  });

  it('still ejects for the two codes no waiting can fix', () => {
    for (const code of ['key_revoked', 'invalid_key']) {
      const s = reduce(connected, {
        t: 'streamError',
        code,
        error: { title: 'gone', detail: 'd', fatal: true },
      });
      expect(s.name).toBe('disconnected');
    }
  });

  // At connect time there is no chat to keep, so a pause has to be said on the connect screen.
  it('is a disconnect when it happens before the session exists', () => {
    const verifying: SessionState = { name: 'verifying', transport: t };
    expect(reduce(verifying, { t: 'meError', error: pausedErr }).name).toBe('disconnected');
  });
});

// 014 promise 1: a host that never answers is news about the host, not about the reply alone.
describe('a host that did not answer', () => {
  const t = fakeTransport();
  const connected: SessionState = { name: 'connected', live: liveOn(t) };

  it('moves the session to degraded(engine) so the header tells the truth', () => {
    const s = reduce(connected, {
      t: 'streamError',
      code: 'host_asleep',
      error: { title: 'desk didn’t answer', detail: 'asleep or offline' },
    });
    expect(s).toMatchObject({ name: 'degraded', reason: 'both' });
    expect(live(s)?.me.host.upstream.healthy).toBe(false);
    expect(t.closes).toBe(0);
  });

  // It failed a ping to earn the word "asleep", so the pill must stop showing a round-trip time:
  // "not answering" next to "relayed via New York · 64 ms" is two claims and one of them is false.
  it('stops showing a live latency for a host it just called asleep', () => {
    const s = reduce(connected, {
      t: 'streamError',
      code: 'host_asleep',
      error: { title: 'desk didn’t answer', detail: 'asleep or offline' },
    });
    expect(live(s)?.pathOk).toBe(false);
    expect(pathLine(live(s)!, 1000)).toContain('not answering');
    expect(pathLine(live(s)!, 1000)).not.toContain('relayed via');
  });
});

// 018: a busy host is a host that answered. The session it answered on is fine.
describe('a host that was busy', () => {
  const t = fakeTransport();
  const connected: SessionState = { name: 'connected', live: liveOn(t) };

  it('stays connected, with its engine healthy and its path current, on a queue timeout', () => {
    const s = reduce(connected, {
      t: 'streamError',
      code: 'queue_timeout',
      error: { title: 'desk is busy', detail: 'Every slot was taken.', retryAfterS: 5 },
    });
    expect(s).toBe(connected);
    expect(t.closes).toBe(0);
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

// 014 promise 5 and promise 3: the meter counts what the friend sends, and says "—" rather than a
// number nobody measured.
describe('the header meters', () => {
  const t = fakeTransport();
  const used = (rpm_used: number, today_tokens: number): Me => ({
    ...ME,
    usage: { ...ME.usage, rpm_used, today_tokens },
  });

  it('counts messages left, not requests used', () => {
    const [rpm] = meters(liveOn(t, { me: used(12, 1000) }));
    expect(rpm.label).toBe('8 messages left this minute');
    expect(rpm.value).toBeCloseTo(0.6);
  });

  it('says "message" once, and never a negative allowance', () => {
    expect(meters(liveOn(t, { me: used(19, 0) }))[0].label).toBe('1 message left this minute');
    expect(meters(liveOn(t, { me: used(40, 0) }))[0].label).toBe('0 messages left this minute');
  });

  it('renders an unmeasured limit as a dash, never as a zero', () => {
    const [rpm, tokens] = meters(liveOn(t, { meOk: false, me: used(12, 150_000) }));
    expect(rpm.label).toBe('— / 20 per minute');
    expect(tokens.label).toBe('— / 200k tokens today');
    expect(rpm.unknown).toBe(true);
    expect(tokens.unknown).toBe(true);
    // Nothing that reads as a measurement: no "0", no bar to draw.
    expect(rpm.label).not.toMatch(/\b0\b/);
    expect(tokens.value).toBe(0);
  });

  it('counts tokens in the units the daily budget is set in', () => {
    expect(meters(liveOn(t, { me: used(0, 150_000) }))[1].label).toBe('150k/200k tokens today');
    expect(compact(1_500_000)).toBe('1.5M');
    expect(compact(1500)).toBe('1.5k');
    expect(compact(999)).toBe('999');
  });
});

// 014 promise 13: a tunnel session that has broken stays broken. Retrying a request over it is
// what made three Regenerates cost 30 s each against a host that was up.
describe('redialling a host whose session broke', () => {
  it('drops the dead session so a new one can be dialled', () => {
    const t = fakeTransport();
    const connected: SessionState = { name: 'connected', live: liveOn(t) };
    const s = run([{ t: 'redial' }], connected);
    expect(s.name).toBe('connecting');
    expect(t.closes).toBe(1); // the dead transport is closed, exactly once, by the one closer
  });

  it('re-verifying puts a live session back without touching the connect screen', () => {
    const t = fakeTransport();
    const fresh = fakeTransport();
    const connected: SessionState = { name: 'connected', live: liveOn(t) };
    const s = run(
      [
        { t: 'redial' },
        { t: 'sessionUp', transport: fresh },
        { t: 'verified', live: liveOn(fresh) },
      ],
      connected,
    );
    expect(s.name).toBe('connected');
    expect(live(s)?.transport).toBe(fresh);
    expect(fresh.closes).toBe(0);
    expect(t.closes).toBe(1);
  });

  it('is ignored when there is no session to redial', () => {
    expect(reduce(IDLE, { t: 'redial' })).toBe(IDLE);
  });
});
