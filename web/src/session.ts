// The session state machine: one pure reducer that owns the answer to "are we connected, and is
// that still true". Everything that could make the app lie about a host — a transport left open
// after a failed /me, a path pill still showing the last good number, a revoked key swallowed by a
// background refresh — is a transition here rather than a flag somewhere in the UI.
//
// `reduce` is pure. It never closes anything: `dropped()` names the transports a transition
// orphaned and `useSession` (App.tsx) is the single place that closes them.
import type { FriendlyError, Me } from './api';
import { describePath, type PingResult, type Transport } from './transport';

/** One live connection to one host. `path`/`pathAt` are the last *successful* measurement. */
export interface Live {
  transport: Transport;
  secret: string;
  /** The tunnel address from the invite. Public, not the secret: it names the host for storage. */
  addr: string;
  me: Me;
  mode: 'direct' | 'tunnel';
  path: PingResult | null;
  pathAt: number;
  /** False when the last ping failed: the pill must stop presenting `path` as current. */
  pathOk: boolean;
  /**
   * False when the last /me failed. `me` is then the last snapshot we had, which is history, not
   * news: the meters must render "—", not the numbers that were true a minute ago (014 promise 3).
   */
  meOk: boolean;
  /** The host has paused this invite: nothing can be sent until they resume it (014 promise 2). */
  paused: boolean;
  /** True when another tab holds the persisted tunnel identity and this one connected fresh. */
  ephemeral: boolean;
}

/**
 * What is wrong while still being usable. Anything worse is a `disconnected`.
 *
 * `key` (014 promise 2) is a paused invite: the host has switched the friend off for now, which is
 * news about the *invite*, not about the chat — the conversation, the composer's text and the
 * transport all stay exactly where they are. It is deliberately a reason and not a state, so the
 * chat screen keeps rendering from one payload (007's reading (ii), 008 ruling).
 */
export type Degradation = 'path' | 'engine' | 'both' | 'key';

export type SessionState =
  | { name: 'idle' }
  | { name: 'loadingWasm'; pct: number | null }
  | { name: 'connecting' }
  | { name: 'verifying'; transport: Transport }
  | { name: 'connected'; live: Live }
  | { name: 'degraded'; live: Live; reason: Degradation }
  /** `reason` is null only when the reader left on purpose. */
  | { name: 'disconnected'; reason: FriendlyError | null };

export type SessionEvent =
  | { t: 'start'; mode: 'direct' | 'tunnel' }
  | { t: 'wasmProgress'; pct: number | null }
  | { t: 'wasmLoaded' }
  | { t: 'sessionUp'; transport: Transport }
  /** The first /me answered: this is the transition that makes a connection real. */
  | { t: 'verified'; live: Live }
  /** A later /me answered; only the snapshot changes. */
  | { t: 'meOk'; me: Me }
  | { t: 'meError'; error: FriendlyError }
  | { t: 'pingOk'; path: PingResult; at: number }
  | { t: 'pingFail' }
  | { t: 'streamError'; code: string; error: FriendlyError }
  /** Throw this session away and dial the same host again (014 promise 13). */
  | { t: 'redial' }
  /** The attempt failed, or the reader pressed Disconnect (`error` null). */
  | { t: 'abort'; error: FriendlyError | null }
  | { t: 'revoked'; error: FriendlyError };

export const IDLE: SessionState = { name: 'idle' };

export function reduce(s: SessionState, e: SessionEvent): SessionState {
  const l = live(s);
  switch (e.t) {
    case 'start':
      return e.mode === 'direct' ? { name: 'connecting' } : { name: 'loadingWasm', pct: null };
    case 'wasmProgress':
      return s.name === 'loadingWasm' ? { name: 'loadingWasm', pct: e.pct } : s;
    case 'wasmLoaded':
      return s.name === 'loadingWasm' ? { name: 'connecting' } : s;
    case 'sessionUp':
      // Out of `connecting` this is a superseded attempt; the state is unchanged and dropped()
      // hands the orphan to the closer.
      return s.name === 'connecting' ? { name: 'verifying', transport: e.transport } : s;
    case 'verified':
      return s.name === 'verifying' && s.transport === e.live.transport ? settle(e.live) : s;
    case 'meOk':
      // A /me that answers is also the end of a pause: `paused` is whatever the host last said.
      return l ? settle({ ...l, me: e.me, meOk: true, paused: e.me.key.status === 'paused' }) : s;
    case 'meError':
      // Verifying: the invite never checked out, so the transport we opened for it goes.
      // Connected: a fatal code ends the session; a pause degrades it; anything else transient
      // keeps the last snapshot but stops presenting it as current (014 promise 3).
      // At verify time there is no chat to keep, so even a pause has to be said on the connect
      // screen; `paused` only degrades a session that already exists.
      if (s.name === 'verifying') return { name: 'disconnected', reason: e.error };
      if (!l) return s;
      if (e.error.fatal) return { name: 'disconnected', reason: e.error };
      return settle({ ...l, meOk: false, paused: l.paused || e.error.paused === true });
    case 'pingOk':
      return l ? settle({ ...l, path: e.path, pathAt: e.at, pathOk: true }) : s;
    case 'pingFail':
      return l ? settle({ ...l, pathOk: false }) : s;
    case 'streamError':
      if (e.error.fatal) return { name: 'disconnected', reason: e.error };
      if (!l) return s;
      // A paused invite is not an ejection any more (014 promise 2): the chat stays, the header
      // says why nothing can be sent, and the next /me that says `active` clears it.
      if (e.error.paused) return settle({ ...l, paused: true });
      // The host's engine answering 503, or not answering at all, is news about the host: the
      // header stops claiming healthy until a /me says otherwise. A host we declared asleep failed
      // a ping to earn that word, so the pill must stop showing a round-trip time too — otherwise
      // the header says "not answering" next to "relayed via New York · 64 ms" (014 promise 3).
      if (e.code === 'host_asleep') {
        const host = { ...l.me.host, upstream: { ...l.me.host.upstream, healthy: false } };
        return settle({ ...l, me: { ...l.me, host }, pathOk: false });
      }
      if (e.code === 'upstream_down') {
        const host = { ...l.me.host, upstream: { ...l.me.host.upstream, healthy: false } };
        return settle({ ...l, me: { ...l.me, host } });
      }
      return s;
    // A tunnel session that has broken stays broken: retrying a request over it is what made the
    // reader wait 30 s three times for a host that was up (014 promise 13). Going back to
    // `connecting` is what drops it — `dropped()` closes it — and the redial effect in App.tsx is
    // what dials again. Only from a live session: there is nothing to redial from anywhere else.
    case 'redial':
      return l ? { name: 'connecting' } : s;
    case 'abort':
    case 'revoked':
      return { name: 'disconnected', reason: e.error };
  }
}

/**
 * The transports this transition orphaned — the whole "no leaked sessions" promise, in one place.
 * A transport is orphaned when the state stops holding it, or when an event carried one into a
 * state that cannot accept it (a connect attempt that landed after a newer one superseded it).
 */
export function dropped(prev: SessionState, e: SessionEvent, next: SessionState): Transport[] {
  const out: Transport[] = [];
  const before = transportOf(prev);
  const after = transportOf(next);
  if (before && before !== after) out.push(before);
  const carried = e.t === 'sessionUp' ? e.transport : e.t === 'verified' ? e.live.transport : null;
  if (carried && carried !== after && !out.includes(carried)) out.push(carried);
  return out;
}

export function transportOf(s: SessionState): Transport | null {
  if (s.name === 'verifying') return s.transport;
  const l = live(s);
  return l ? l.transport : null;
}

/** The live connection, when there is one. `connected` and `degraded` differ only in what is wrong. */
export function live(s: SessionState): Live | null {
  return s.name === 'connected' || s.name === 'degraded' ? s.live : null;
}

/**
 * One place decides which of `connected` / `degraded(reason)` the live payload is in. When more
 * than one thing is wrong the reader is told the most actionable one: a paused invite is something
 * their host can fix in a second, a dead engine is the host's to restart, and an unmeasurable path
 * is the least of it.
 */
function settle(l: Live): SessionState {
  const engine = !l.me.host.upstream.healthy;
  const path = !l.pathOk || !l.meOk;
  const reason: Degradation | null = l.paused
    ? 'key'
    : engine && path
      ? 'both'
      : engine
        ? 'engine'
        : path
          ? 'path'
          : null;
  return reason ? { name: 'degraded', live: l, reason } : { name: 'connected', live: l };
}

// ---- what the surfaces say -------------------------------------------------------------------

/**
 * The path, as text, never a dot (pm/BELIEFS.md). When the last measurement failed the line names
 * the host that stopped answering and dates the last good number, so nothing on screen is a live
 * latency that is not live (014 promise 3).
 */
export function pathLine(l: Live, now: number): string {
  if (l.pathOk && l.meOk) return describePath(l.path, l.me.host.relay.region);
  const who = l.me.host.name.trim() || 'The host';
  if (!l.path) return `${who} — not answering`;
  return `${who} — not answering · last ${Math.round(l.path.rttMs)} ms ${ago(now - l.pathAt)}`;
}

/**
 * True while what `me` holds is history rather than news: the meters must render "—" instead of
 * numbers that were true a minute ago (014 promise 3). A fabricated zero is the worst of the three.
 */
export function metersUnknown(l: Live): boolean {
  return !l.meOk;
}

export interface MeterView {
  label: string;
  /** 0–1, and meaningless while `unknown`: the surface draws no bar at all then. */
  value: number;
  unknown: boolean;
}

/**
 * The two numbers in the header, as the friend's own units (014 promise 5): they send messages, so
 * the meter counts messages left, not requests used. When the last /me failed the numbers are
 * history and read "—" rather than a zero nobody measured (promise 3).
 */
export function meters(l: Live): [MeterView, MeterView] {
  const unknown = metersUnknown(l);
  const { rpm, daily_tokens: daily } = l.me.limits;
  const { rpm_used: used, today_tokens: today } = l.me.usage;
  const left = Math.max(0, rpm - used);
  return [
    {
      label: unknown
        ? `— / ${rpm} per minute`
        : rpm > 0
          ? `${left} message${left === 1 ? '' : 's'} left this minute`
          : `${used} messages this minute`,
      value: unknown || rpm === 0 ? 0 : used / rpm,
      unknown,
    },
    {
      label: unknown
        ? `— / ${compact(daily)} tokens today`
        : daily > 0
          ? `${compact(today)}/${compact(daily)} tokens today`
          : `${compact(today)} tokens today`,
      value: unknown || daily === 0 ? 0 : today / daily,
      unknown,
    },
  ];
}

export function compact(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10_000 ? 0 : 1)}k`;
  return String(n);
}

/** One line for the header when the host itself is degraded. `path` alone speaks in the pill. */
export function degradedLine(reason: Degradation, me: Me): string | null {
  if (reason === 'path') return null;
  if (reason === 'key') {
    const who = me.host.name.trim() || 'Your host';
    return `${who} paused your invite. Your message is still here — send it again once they resume.`;
  }
  return `${me.host.upstream.kind} is not answering on the host — messages will fail until it is back`;
}

export function ago(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} s ago`;
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  return `${Math.round(s / 3600)} h ago`;
}

/** A wait, rendered as a wait: seconds up to ten minutes, then something a human can plan around. */
export function waitText(ms: number): string {
  const s = Math.max(0, Math.ceil(ms / 1000));
  if (s <= 600) return `${s}s`;
  const m = Math.round(s / 60);
  return m < 120 ? `${m} min` : `${Math.round(m / 60)} h`;
}
