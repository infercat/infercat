import { fakeTransport as makeTransport } from './test/fakes';
// The session machine. These are the promises that used to be scattered flags: a transport is
// never left open, a stale measurement is never presented as current, and a revoked key is never
// swallowed by a background refresh.
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { FriendlyError, Me } from './api';
import {
  ago,
  boundHandshake,
  compact,
  contextMeter,
  degradedLine,
  keyDead,
  dropped,
  handshakeFailure,
  IDLE,
  live,
  meters,
  metersUnknown,
  pathLine,
  probeDelay,
  probing,
  reduce,
  scheduleProbe,
  waitText,
  type Live,
  type SessionEvent,
  type SessionState,
} from './session';
import type { PingResult, Transport } from './transport';

function fakeTransport(): Transport & { closes: number } {
  const t = makeTransport(() => Promise.resolve(new Response('{}')), { kind: 'tunnel' as const, closes: 0, close(): void {
      t.closes++;
    } });
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
    vision: { m: null },
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
    key: 'active',
    ephemeral: false,
    probed: 0,
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

  // 020 promise 4: one health source. A failed request never asserts anything about the engine;
  // the /me that follows every request is what does.
  it('a failed request never asserts the engine is down — only /me does', () => {
    const s = reduce(connected, {
      t: 'streamError',
      code: 'upstream_down',
      error: { title: 'offline', detail: 'x', code: 'upstream_down' },
    });
    expect(s).toBe(connected);
    const sick = { ...ME, host: { ...ME.host, upstream: { ...ME.host.upstream, healthy: false } } };
    expect(reduce(s, { t: 'meOk', me: sick })).toMatchObject({ name: 'degraded', reason: 'engine' });
  });

  it('a snapshot that could not be refreshed claims nothing about the engine', () => {
    const sick = { ...ME, host: { ...ME.host, upstream: { ...ME.host.upstream, healthy: false } } };
    const s = reduce({ name: 'connected', live: liveOn(t, { me: sick }) }, { t: 'meError', error: { title: 'x', detail: 'y' } });
    // "Reconnect" and "llama.cpp is not answering" on one screen are two causes, one of them stale.
    expect(s).toMatchObject({ name: 'degraded', reason: 'path' });
    expect(degradedLine('path', live(s)!)).toBeNull();
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

  // 020 promise 5: revoke stays in the thread, like pause. The chat, the half-written answer and
  // the transport stay; the header says what happened and the one move is a new code.
  it('keeps the session when the refresh says the invite is dead, and says which way it died', () => {
    const revoked = reduce(connected, { t: 'meError', error: { title: 'gone', detail: 'd', fatal: true, code: 'key_revoked' } });
    expect(revoked).toMatchObject({ name: 'degraded', reason: 'key' });
    expect(live(revoked)?.key).toBe('revoked');
    expect(live(revoked)?.transport).toBe(t);
    expect(t.closes).toBe(0);
    expect(degradedLine('key', live(revoked)!)).toBe('This invite was revoked — ask desk for a new code.');
    expect(keyDead(live(revoked)!)).toBe(true);
    expect(metersUnknown(live(revoked)!)).toBe(true);
    // The host answered, so the path is still a live number: "not answering" would be a lie.
    expect(pathLine(live(revoked)!, 1000)).toBe('relayed via San Francisco · 32 ms');

    const unknown = reduce(connected, { t: 'meError', error: { title: 'gone', detail: 'd', fatal: true, code: 'invalid_key' } });
    expect(live(unknown)?.key).toBe('invalid');
    expect(degradedLine('key', live(unknown)!)).toContain('no longer recognises this invite');
    expect(keyDead(live(unknown)!)).toBe(true);
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
    expect(live(s)?.key).toBe('paused');
    expect(degradedLine('key', live(s)!)).toContain('paused your invite');
    expect(degradedLine('key', live(s)!)).toContain('try again once they resume');
    expect(metersUnknown(live(s)!)).toBe(true); // /me refuses a paused key: no current numbers
    expect(keyDead(live(s)!)).toBe(false);
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

  it('stays in the thread for the two codes no waiting can fix, and says which (020 promise 5)', () => {
    for (const [code, key] of [['key_revoked', 'revoked'], ['invalid_key', 'invalid']] as const) {
      const s = reduce(connected, {
        t: 'streamError',
        code,
        error: { title: 'gone', detail: 'd', fatal: true, code },
      });
      expect(s).toMatchObject({ name: 'degraded', reason: 'key' });
      expect(live(s)?.key).toBe(key);
      expect(t.closes).toBe(0);
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

  // 020 promise 4: what we know is that the host did not answer — so the snapshot is history
  // (meters "—", Reconnect on offer) and nothing is claimed about the engine.
  it('turns everything held into history, and says nothing about the engine', () => {
    const s = reduce(connected, {
      t: 'streamError',
      code: 'host_asleep',
      error: { title: 'desk didn’t answer', detail: 'asleep or offline' },
    });
    expect(s).toMatchObject({ name: 'degraded', reason: 'path' });
    expect(live(s)?.meOk).toBe(false);
    expect(metersUnknown(live(s)!)).toBe(true);
    expect(live(s)?.me.host.upstream.healthy).toBe(true);
    expect(degradedLine('path', live(s)!)).toBeNull();
    expect(t.closes).toBe(0);
    // And the /me that follows the failure is what puts the numbers back — or not.
    expect(reduce(s, { t: 'meOk', me: ME }).name).toBe('degraded'); // pathOk is still false until a ping
    expect(reduce(reduce(s, { t: 'meOk', me: ME }), { t: 'pingOk', path: PATH, at: 2000 }).name).toBe('connected');
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

// 020 promise 3: the one limit that ends conversations gets a meter.
describe('the context meter', () => {
  it('reads what the last reply cost against the model context', () => {
    expect(contextMeter(3200, 4096)).toEqual({ label: '3.2k/4.1k context', value: 3200 / 4096, unknown: false });
    expect(contextMeter(4096, 4096)?.value).toBe(1);
  });

  it('is a dash before any reply has said, never a zero', () => {
    expect(contextMeter(null, 8192)).toMatchObject({ label: '— / 8.2k context', unknown: true });
  });

  it('is absent when the host has not said how big the context is', () => {
    expect(contextMeter(3200, 0)).toBeNull();
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
  it('leaves the connect screen and carries the dead session as the redial target', () => {
    const t = fakeTransport();
    const connected: SessionState = { name: 'connected', live: liveOn(t) };
    const s = run([{ t: 'redial' }], connected);
    expect(s.name).toBe('connecting');
    // The dead transport is kept as the redial target, not closed here (023): it is closed once the
    // fresh session is adopted, so a same-identity dial is never poisoned by an early close.
    expect(s.name === 'connecting' && s.redial?.transport).toBe(t);
    expect(t.closes).toBe(0);
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

// 022 promise 1: a degraded session asks after the host by itself, backing off, and heals the
// moment a probe finds it — by a /me over the session it has, or by a fresh dial when the host
// stopped answering over that session (measured: such a session never answers again).
describe('the self-probe', () => {
  const connected = (t: Transport, over: Partial<Live> = {}): SessionState => ({ name: 'connected', live: liveOn(t, over) });
  const noAnswer: SessionEvent = { t: 'streamError', code: 'host_asleep', error: { title: 'no', detail: 'no', code: 'host_asleep' } };

  afterEach(() => vi.useRealTimers());

  it('backs off 5 s, 10 s, 20 s, then holds at 30 s', () => {
    expect([0, 1, 2, 3, 4, 9].map(probeDelay)).toEqual([5_000, 10_000, 20_000, 30_000, 30_000, 30_000]);
  });

  it('runs for every degradation that can heal, never for an invite the host switched off', () => {
    const t = fakeTransport();
    expect(probing(reduce(connected(t), noAnswer))).toBe(true);
    expect(probing(reduce(connected(t), { t: 'pingFail' }))).toBe(true);
    expect(probing(reduce(connected(t), { t: 'meOk', me: { ...ME, host: { ...ME.host, upstream: { ...ME.host.upstream, healthy: false } } } }))).toBe(true);
    expect(probing(reduce(connected(t), { t: 'meOk', me: { ...ME, key: { ...ME.key, status: 'paused' } } }))).toBe(false);
    expect(probing(connected(t))).toBe(false);
  });

  it('counts each miss while degraded, and forgets the count on the way back to connected', () => {
    const t = fakeTransport();
    let s = reduce(connected(t), noAnswer);
    s = reduce(reduce(s, { t: 'probed' }), { t: 'probed' });
    expect(s.name).toBe('degraded');
    expect(live(s)?.probed).toBe(2);
    expect(reduce(connected(t), { t: 'probed' })).toEqual(connected(t)); // nothing to count while healthy
    s = reduce(s, { t: 'verified', live: liveOn(fakeTransport(), { probed: 0 }) });
    expect(s.name).toBe('connected');
    expect(live(s)?.probed).toBe(0);
  });

  it('a fresh session replaces the one the host stopped answering over, closing it once', () => {
    const old = fakeTransport();
    const fresh = fakeTransport();
    let s = run([noAnswer], connected(old));
    expect(s.name).toBe('degraded');
    s = run([{ t: 'verified', live: liveOn(fresh, { probed: 0 }) }], s);
    expect(s.name).toBe('connected');
    expect(live(s)?.transport).toBe(fresh);
    expect(live(s)?.meOk).toBe(true);
    expect(old.closes).toBe(1);
    expect(fresh.closes).toBe(0);
  });

  it('a candidate that lands while the session it has still reaches the host is closed, not adopted', () => {
    const t = fakeTransport();
    const stray = fakeTransport();
    // Engine down: /me got through, so the probe is a /me, and a stray dial has nothing to replace.
    const sick = reduce(connected(t), { t: 'meOk', me: { ...ME, host: { ...ME.host, upstream: { ...ME.host.upstream, healthy: false } } } });
    expect(run([{ t: 'verified', live: liveOn(stray) }], sick)).toBe(sick);
    expect(stray.closes).toBe(1);
    expect(t.closes).toBe(0);
    // And one that lands after Disconnect took the session away. (After Reconnect it is adopted:
    // that is the dial Reconnect joined — 023, below.)
    const late = fakeTransport();
    const gone = run([noAnswer, { t: 'abort', error: null }], connected(t));
    expect(run([{ t: 'verified', live: liveOn(late) }], gone)).toBe(gone);
    expect(late.closes).toBe(1);
  });

  it('host dead for 40 s, then back: connected within 30 s of its return, on a fake clock', async () => {
    vi.useFakeTimers();
    const t = fakeTransport();
    let s = reduce(connected(t), noAnswer);
    let dead = true;
    const asked: number[] = [];
    let cancel = () => {};
    let armed = { on: false, probed: -1 };
    // The effect in App.tsx: armed on the miss count and on whether the session is degraded at
    // all — never on the poll's other news — and cancelled once healed.
    const rearm = () => {
      const now = { on: probing(s), probed: live(s)?.probed ?? 0 };
      if (now.on === armed.on && now.probed === armed.probed) return;
      armed = now;
      cancel();
      cancel = now.on ? scheduleProbe(now.probed, probe, dispatch) : () => {};
    };
    const dispatch = (e: SessionEvent) => {
      s = reduce(s, e);
      rearm();
    };
    const probe = async () => {
      asked.push(Date.now());
      if (dead) dispatch({ t: 'meError', error: { title: 'no', detail: 'no' } });
      else dispatch({ t: 'verified', live: liveOn(fakeTransport()) });
    };
    rearm();
    const t0 = Date.now();
    await vi.advanceTimersByTimeAsync(40_000);
    expect(s.name).toBe('degraded');
    expect(asked.map((at) => (at - t0) / 1000)).toEqual([5, 15, 35]);
    dead = false;
    const back = Date.now();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(s.name).toBe('connected');
    expect((asked[asked.length - 1] as number) - back).toBeLessThanOrEqual(30_000);
    // Healed: nothing more is asked.
    await vi.advanceTimersByTimeAsync(120_000);
    expect(asked).toHaveLength(4);
  });
});

// 023: one dial per host. A Reconnect carries its target through the attempt, adopts whichever dial
// verifies for that host (its own, or the self-probe's, which it joins), and falls back to the
// session it was replacing — degraded — when the dial fails, never to the connect screen.
describe('reconnect joins the dial in flight', () => {
  const dead: SessionEvent = { t: 'streamError', code: 'host_asleep', error: { title: 'no', detail: 'no', code: 'host_asleep' } };
  const degraded = (t: Transport): SessionState => run([dead], { name: 'connected', live: liveOn(t) });

  it('carries the target through connecting and verifying, and a second Reconnect meanwhile is a no-op', () => {
    const old = fakeTransport();
    const s = run([{ t: 'redial' }], degraded(old));
    expect(s.name).toBe('connecting');
    expect(s.name === 'connecting' && s.redial?.transport).toBe(old);
    // Kept alive until the new session is adopted (023): closing it now, while the fresh
    // same-identity session is handshaking, poisons the relay for both.
    expect(old.closes).toBe(0);
    expect(reduce(s, { t: 'redial' })).toBe(s);
    const fresh = fakeTransport();
    const v = reduce(s, { t: 'sessionUp', transport: fresh });
    expect(v.name === 'verifying' && v.redial?.transport).toBe(old);
    // Adoption is when the old one finally goes — exactly once, and never the new one.
    const done = run([{ t: 'verified', live: liveOn(fresh) }], v);
    expect(done.name).toBe('connected');
    expect(old.closes).toBe(1);
    expect(fresh.closes).toBe(0);
  });

  it('adopts the self-probe\'s dial straight from connecting, for the host it is redialling only', () => {
    const old = fakeTransport();
    const s = run([{ t: 'redial' }], degraded(old));
    const stranger = fakeTransport();
    expect(run([{ t: 'verified', live: liveOn(stranger, { addr: 'tcOTHER' }) }], s)).toBe(stranger && s);
    expect(stranger.closes).toBe(1);
    const joined = fakeTransport();
    const next = run([{ t: 'verified', live: liveOn(joined) }], s);
    expect(next.name).toBe('connected');
    expect(live(next)?.transport).toBe(joined);
    expect(joined.closes).toBe(0);
    expect(old.closes).toBe(1); // the joined dial's session takes over; the old one is closed at adoption
    // The card's own attempt carries no target: a stray candidate is never adopted there.
    const stray = fakeTransport();
    expect(run([{ t: 'verified', live: liveOn(stray) }], { name: 'connecting' })).toEqual({ name: 'connecting' });
    expect(stray.closes).toBe(1);
  });

  it('a dial that fails, or a /me that hangs past its bound, resolves to the degraded session, not the connect screen', () => {
    const old = fakeTransport();
    const s = run([{ t: 'redial' }], degraded(old));
    const timedOut: SessionEvent = { t: 'meError', error: { title: 'no', detail: 'no' } };
    // No dial at all.
    let back = run([timedOut], s);
    expect(back.name).toBe('degraded');
    expect(live(back)?.meOk).toBe(false);
    expect(live(back)?.me).toBe(ME); // the chat's payload is intact
    expect(probing(back)).toBe(true); // and the self-probe keeps asking
    // A dial that opened, then a /me that never answered: the new transport goes too.
    const fresh = fakeTransport();
    back = run([{ t: 'sessionUp', transport: fresh }, timedOut], s);
    expect(back.name).toBe('degraded');
    expect(fresh.closes).toBe(1);
    // A card attempt whose /me fails still ends on the connect screen, as before.
    expect(run([{ t: 'sessionUp', transport: fakeTransport() }, timedOut], { name: 'connecting' }).name).toBe('disconnected');
  });
});

describe('the handshake bound (033)', () => {
  afterEach(() => vi.useRealTimers());

  /** The card's effect as it runs: armed in `connecting`, stopped on the way out, failing like Cancel. */
  function card(log: string[]) {
    let s: SessionState = run([{ t: 'start', mode: 'tunnel' }, { t: 'wasmLoaded' }]);
    let stop = (): void => {};
    const dispatch = (e: SessionEvent): void => {
      s = run([e], s);
      if (s.name !== 'connecting') stop();
    };
    stop = boundHandshake(dispatch, () => dispatch({ t: 'abort', error: handshakeFailure(log, 'desk') }));
    return { dispatch, state: () => s, reason: (): FriendlyError | null => (s.name === 'disconnected' ? s.reason : null) };
  }

  it('no sessionUp for 20 s: the wait is marked at 8 s, and the attempt ends as the host not answering', async () => {
    vi.useFakeTimers();
    const c = card(['handshake attempt 3: context deadline exceeded']);
    await vi.advanceTimersByTimeAsync(8_100);
    expect(c.state()).toEqual({ name: 'connecting', slow: true });
    await vi.advanceTimersByTimeAsync(11_800);
    expect(c.state().name).toBe('connecting');
    await vi.advanceTimersByTimeAsync(200);
    expect(c.reason()).toEqual({ title: 'desk didn’t answer', detail: expect.stringMatching(/asleep or offline/), hostSaid: 'handshake attempt 3: context deadline exceeded' }); // no `fatal`: Try again, not a new code
  });

  it('sessionUp at 19 s: verifying, then connected, and nothing fails at 20', async () => {
    vi.useFakeTimers();
    const c = card([]);
    const t = fakeTransport();
    await vi.advanceTimersByTimeAsync(19_000);
    c.dispatch({ t: 'sessionUp', transport: t });
    await vi.advanceTimersByTimeAsync(5_000);
    expect(c.state()).toEqual({ name: 'verifying', transport: t });
    c.dispatch({ t: 'verified', live: liveOn(t) });
    expect(c.state().name).toBe('connected');
    expect(t.closes).toBe(0);
  });

  it('Cancel at 5 s stops the clock: no failure lands at 20', async () => {
    vi.useFakeTimers();
    const c = card([]);
    await vi.advanceTimersByTimeAsync(5_000);
    c.dispatch({ t: 'abort', error: null });
    await vi.advanceTimersByTimeAsync(20_000);
    expect(c.state()).toEqual({ name: 'disconnected', reason: null });
  });

  it('reads the bridge’s log: a relay map it could not fetch is this network, anything else is the host', () => {
    const line = 'handshake attempt 12: fetching DERPMap for region 301: Get "https://tailcat.dev/derpmap.json": net/http: fetch() failed: TypeError: Failed to fetch';
    const relay = handshakeFailure(['handshake attempt 11: context deadline exceeded', line], 'desk');
    expect(relay).toMatchObject({ title: 'Can’t reach the relay from this network', hostSaid: line });
    expect(relay.detail).toMatch(/tailcat\.dev.*region 301/);
    const host = handshakeFailure(['handshake attempt 4: context deadline exceeded'], '  ');
    expect(host).toMatchObject({ title: 'The host didn’t answer', hostSaid: 'handshake attempt 4: context deadline exceeded' });
    expect(handshakeFailure([], 'desk')).toEqual({ title: 'desk didn’t answer', detail: expect.stringMatching(/asleep/) });
  });

  it('marks slow only while connecting, once', () => {
    expect(reduce(IDLE, { t: 'slow' })).toBe(IDLE);
    const slow = run([{ t: 'start', mode: 'direct' }, { t: 'slow' }]);
    expect(slow).toEqual({ name: 'connecting', slow: true });
    expect(reduce(slow, { t: 'slow' })).toBe(slow);
  });
});
