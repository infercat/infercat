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
  /** True when another tab holds the persisted tunnel identity and this one connected fresh. */
  ephemeral: boolean;
}

/** What is wrong while still being usable. Anything worse is a `disconnected`. */
export type Degradation = 'path' | 'engine' | 'both';

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
      return l ? settle({ ...l, me: e.me }) : s;
    case 'meError':
      // Verifying: the invite never checked out, so the transport we opened for it goes.
      // Connected: a fatal code ends the session; anything transient keeps the last snapshot,
      // because a momentary refresh failure is not news about the host.
      if (s.name === 'verifying') return { name: 'disconnected', reason: e.error };
      return l && e.error.fatal ? { name: 'disconnected', reason: e.error } : s;
    case 'pingOk':
      return l ? settle({ ...l, path: e.path, pathAt: e.at, pathOk: true }) : s;
    case 'pingFail':
      return l ? settle({ ...l, pathOk: false }) : s;
    case 'streamError':
      if (e.error.fatal) return { name: 'disconnected', reason: e.error };
      // The host's engine answering 503 is news about the host: the header stops claiming healthy.
      if (l && e.code === 'upstream_down') {
        const host = { ...l.me.host, upstream: { ...l.me.host.upstream, healthy: false } };
        return settle({ ...l, me: { ...l.me, host } });
      }
      return s;
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

function settle(l: Live): SessionState {
  const engine = !l.me.host.upstream.healthy;
  const path = !l.pathOk;
  const reason: Degradation | null = engine && path ? 'both' : engine ? 'engine' : path ? 'path' : null;
  return reason ? { name: 'degraded', live: l, reason } : { name: 'connected', live: l };
}

// ---- what the surfaces say -------------------------------------------------------------------

/**
 * The path, as text, never a dot (pm/BELIEFS.md). A failed measurement says so and dates the last
 * good one instead of presenting it as current.
 */
export function pathLine(l: Live, now: number): string {
  if (l.pathOk) return describePath(l.path, l.me.host.relay.region);
  if (!l.path) return 'path unknown';
  return `path unknown · last ${Math.round(l.path.rttMs)} ms ${ago(now - l.pathAt)}`;
}

/** One line for the header when the host itself is degraded. */
export function degradedLine(reason: Degradation, me: Me): string | null {
  if (reason === 'path') return null;
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
