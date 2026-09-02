// The session state machine: one pure reducer that owns the answer to "are we connected, and is
// that still true". Everything that could make the app lie about a host — a transport left open
// after a failed /me, a path pill still showing the last good number, a revoked key swallowed by a
// background refresh — is a transition here rather than a flag somewhere in the UI.
//
// `reduce` is pure. It never closes anything: `dropped()` names the transports a transition
// orphaned and `useSession` (App.tsx) is the single place that closes them.
import { needsRedial, type FriendlyError, type Me } from './api';
import { describePath, type PingResult, type Transport } from './transport';

/**
 * What the host has done with this invite. `active` is the only state in which anything can be
 * sent. `paused` is temporary and the host can undo it; `revoked` and `invalid` (rotated or deleted)
 * are for good and the only move is a new code — but in every case the chat, the transport and the
 * reader's words stay exactly where they are (014 promise 2, 020 promise 5).
 */
export type KeyState = 'active' | 'paused' | 'revoked' | 'invalid';

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
   * False when the last /me did not reach the host — or the last request did not (020 promise 4).
   * `me` is then the last snapshot we had, which is history, not news: the meters render "—", the
   * pill says "not answering", and nothing about the engine is claimed until a /me gets through.
   */
  meOk: boolean;
  key: KeyState;
  /** True when another tab holds the persisted tunnel identity and this one connected fresh. */
  ephemeral: boolean;
  /**
   * How many self-probes have found the host still unreachable or its engine still down since the
   * session last degraded (022 promise 1). The schedule is probeDelay(probed); zero while connected.
   */
  probed: number;
}

/**
 * What is wrong while still being usable. Anything worse is a `disconnected`.
 *
 * `key` is an invite the host has paused, revoked or replaced: news about the *invite*, not about
 * the chat — the conversation, the half-written answer and the transport all stay where they are.
 * It is deliberately a reason and not a state, so the chat screen keeps rendering from one payload
 * (007's reading (ii), 008 ruling).
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
  /** A self-probe came back, whatever it found: the next one waits longer (022 promise 1). */
  | { t: 'probed' }
  /** The attempt failed, or the reader pressed Disconnect (`error` null). */
  | { t: 'abort'; error: FriendlyError | null };

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
      if (s.name === 'verifying' && s.transport === e.live.transport) return settle(e.live);
      // A self-probe dialled the host afresh because this session stopped reaching it (022
      // promise 1): the new session takes over and dropped() closes the one it replaces. While the
      // session it has does reach the host there is nothing to replace: the candidate is closed.
      return l && !l.meOk ? settle(e.live) : s;
    case 'meOk':
      // A /me that answers is the one source of the engine's health and the key's state (020
      // promise 4): whatever it says is what the header says, and a pause ends the moment it says
      // `active`.
      return l ? settle({ ...l, me: e.me, meOk: true, key: e.me.key.status }) : s;
    case 'meError':
      // Verifying: the invite never checked out, so the transport we opened for it goes. At verify
      // time there is no chat to keep, so even a pause has to be said on the connect screen.
      if (s.name === 'verifying') return { name: 'disconnected', reason: e.error };
      if (!l) return s;
      return settle(afterFailure(l, e.error));
    case 'pingOk':
      return l ? settle({ ...l, path: e.path, pathAt: e.at, pathOk: true }) : s;
    case 'pingFail':
      return l ? settle({ ...l, pathOk: false }) : s;
    case 'streamError':
      if (!l) return s;
      // A request that did not reach the host at all — no head in 15 s, or the transport broke
      // under it — leaves everything we hold as history: the path stops being a live number and
      // the meters go blank until a /me gets through (014 promise 3). It asserts nothing about
      // the engine: that is /me's to say, never a failed request's (020 promise 4).
      if (needsRedial(e.code)) return settle({ ...l, pathOk: false, meOk: false });
      // The host answered. A code about the invite changes the key's state; every other error
      // (busy, too fast, the engine's own failure) is the message's to say, and the /me that
      // follows every request is what updates the header.
      if (e.error.paused || e.error.fatal) return settle(afterFailure(l, e.error));
      return s;
    // A tunnel session that has broken stays broken: retrying a request over it is what made the
    // reader wait 30 s three times for a host that was up (014 promise 13). Going back to
    // `connecting` is what drops it — `dropped()` closes it — and the redial effect in App.tsx is
    // what dials again. Only from a live session: there is nothing to redial from anywhere else.
    case 'redial':
      return l ? { name: 'connecting' } : s;
    case 'probed':
      return s.name === 'degraded' ? settle({ ...s.live, probed: s.live.probed + 1 }) : s;
    case 'abort':
      return { name: 'disconnected', reason: e.error };
  }
}

/**
 * What a failed /me or request says about a live session. A code about the *invite* changes the
 * key's state and nothing else — the host answered, so the path and the snapshot are still news.
 * Anything else transient keeps the last snapshot but stops presenting it as current.
 */
function afterFailure(l: Live, error: FriendlyError): Live {
  if (error.paused) return { ...l, key: 'paused' };
  if (error.fatal) return { ...l, key: error.code === 'key_revoked' ? 'revoked' : 'invalid' };
  return { ...l, meOk: false };
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
 * than one thing is wrong the reader is told the most actionable one: a paused or revoked invite
 * is something their host can fix in a second, a dead engine is the host's to restart, and an
 * unmeasurable path is the least of it. The engine is only ever called unhealthy on the word of a
 * /me that got through: a snapshot we could not refresh claims nothing (020 promise 4).
 */
function settle(l: Live): SessionState {
  const engine = l.meOk && !l.me.host.upstream.healthy;
  const path = !l.pathOk || !l.meOk;
  const reason: Degradation | null =
    l.key !== 'active'
      ? 'key'
      : engine && path
        ? 'both'
        : engine
          ? 'engine'
          : path
            ? 'path'
            : null;
  if (reason) return { name: 'degraded', live: l, reason };
  return { name: 'connected', live: l.probed === 0 ? l : { ...l, probed: 0 } };
}

// ---- the self-probe (022 promise 1) --------------------------------------------------------------

/**
 * Whether the session asks after the host by itself. Every degradation but a switched-off invite
 * can end without the reader doing anything — the host wakes, the engine comes back, the path is
 * measurable again — so the session keeps asking, on a schedule that backs off; a pause heals on
 * the ordinary poll, and a revoked invite cannot heal at all.
 */
export function probing(s: SessionState): boolean {
  return s.name === 'degraded' && s.reason !== 'key';
}

/** How long after the last probe the next one goes out: 5 s, then 10, 20, and 30 s from there. */
export function probeDelay(probed: number): number {
  return Math.min(30_000, 5_000 * 2 ** probed);
}

/**
 * One probe on the schedule: `probe` runs after probeDelay(probed) and dispatches what it finds;
 * `probed` follows, so the reducer counts the miss and the next one waits longer. The caller
 * re-arms on every change of `probed` and cancels when the session is no longer degraded (or is
 * gone), so a host that heals — by a probe, by the poll, by Reconnect — is asked nothing more.
 */
export function scheduleProbe(
  probed: number,
  probe: () => Promise<void>,
  dispatch: (e: SessionEvent) => void,
): () => void {
  let armed = true;
  const timer = setTimeout(() => {
    void probe().finally(() => {
      if (armed) dispatch({ t: 'probed' });
    });
  }, probeDelay(probed));
  return () => {
    armed = false;
    clearTimeout(timer);
  };
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
 * An invite the host has switched off has no current numbers either: /me refuses it.
 */
export function metersUnknown(l: Live): boolean {
  return !l.meOk || l.key !== 'active';
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

/**
 * The third meter (020 promise 3): how much of the model's context this chat has filled — the one
 * limit that ends a conversation rather than a reply. `used` is what the last reply reported
 * (prompt + completion, which is the whole thread as the engine saw it); null before any reply
 * has, and then it reads "—" rather than a zero. Null when the host has not said how big the
 * context is: a meter with no ceiling is not a meter.
 */
export function contextMeter(used: number | null, modelContext: number): MeterView | null {
  if (modelContext <= 0) return null;
  const unknown = used === null;
  return {
    label: unknown ? `— / ${compact(modelContext)} context` : `${compact(used)}/${compact(modelContext)} context`,
    value: unknown ? 0 : Math.min(1, used / modelContext),
    unknown,
  };
}

export function compact(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10_000 ? 0 : 1)}k`;
  return String(n);
}

/**
 * One line for the header when the host itself is degraded. `path` alone speaks in the pill; the
 * engine line only ever quotes a /me that got through (settle() guarantees it).
 */
export function degradedLine(reason: Degradation, l: Live): string | null {
  if (reason === 'path') return null;
  const who = l.me.host.name.trim() || 'Your host';
  if (reason === 'key') {
    switch (l.key) {
      case 'paused':
        return `${who} paused your invite. Your message is still here — try again once they resume.`;
      case 'revoked':
        return `This invite was revoked — ask ${who} for a new code.`;
      case 'invalid':
        return `${who} no longer recognises this invite — ask them for a new code.`;
      default:
        return null;
    }
  }
  return `${l.me.host.upstream.kind} is not answering on the host — messages will fail until it is back`;
}

/** An invite that no waiting can bring back: the only move is a new code (020 promise 5). */
export function keyDead(l: Live): boolean {
  return l.key === 'revoked' || l.key === 'invalid';
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
