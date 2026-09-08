import { tr } from './i18n/text';
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
  /** `redial` (023): the session Reconnect is replacing, carried through the attempt so it adopts
   *  whichever dial verifies for that host — its own or the self-probe's, which it joins — and falls
   *  back to that session, degraded, if the dial fails, never to the connect screen. */
  | { name: 'connecting'; redial?: Live; slow?: boolean }
  | { name: 'verifying'; transport: Transport; redial?: Live }
  | { name: 'connected'; live: Live }
  | { name: 'degraded'; live: Live; reason: Degradation }
  /** `reason` is null only when the reader left on purpose. */
  | { name: 'disconnected'; reason: FriendlyError | null };

export type SessionEvent =
  | { t: 'start'; mode: 'direct' | 'tunnel' }
  | { t: 'wasmProgress'; pct: number | null }
  | { t: 'wasmLoaded' }
  | { t: 'sessionUp'; transport: Transport }
  /** The handshake has run past the point a wait reads as alive by itself (033): say so. */
  | { t: 'slow' }
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
      return s.name === 'connecting' ? { name: 'verifying', transport: e.transport, ...(s.redial ? { redial: s.redial } : {}) } : s;
    case 'slow':
      return s.name === 'connecting' && !s.slow ? { ...s, slow: true } : s;
    case 'verified':
      if (s.name === 'verifying' && s.transport === e.live.transport) return settle(e.live);
      // A redial that joined the self-probe's dial (023) verifies straight from `connecting`, for
      // the host it is redialling. The card's own attempts carry no target and adopt nothing.
      if (s.name === 'connecting' && sameHost(s.redial, e.live)) return settle(e.live);
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
      // A redial that failed — no dial, or a /me that hung past its bound — goes back to the session
      // it was replacing, degraded (023): the chat stays, the header says the host is not answering,
      // the self-probe keeps asking. Never a permanent "Opening a fresh connection…".
      if ((s.name === 'connecting' || s.name === 'verifying') && s.redial) return settle(afterFailure(s.redial, e.error));
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
      return l ? { name: 'connecting', redial: l } : s;
    case 'probed':
      return s.name === 'degraded' ? settle({ ...s.live, probed: s.live.probed + 1 }) : s;
    case 'abort':
      return { name: 'disconnected', reason: e.error };
  }
}

/** The candidate is for the host a redial is replacing: same address, same invite. */
function sameHost(target: Live | undefined, candidate: Live): boolean {
  return target !== undefined && target.addr === candidate.addr && target.secret === candidate.secret;
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
  // The session being redialled (023) stays open across the attempt and is closed when the new one
  // is adopted, or the attempt is abandoned — never at `redial`: the timing a self-probe heal uses.
  const keep = redialTarget(next)?.transport;
  if (before && before !== after && before !== keep) out.push(before);
  const stale = redialTarget(prev)?.transport;
  if (stale && !redialTarget(next) && stale !== after && !out.includes(stale)) out.push(stale);
  const carried = e.t === 'sessionUp' ? e.transport : e.t === 'verified' ? e.live.transport : null;
  if (carried && carried !== after && !out.includes(carried)) out.push(carried);
  return out;
}

/** The session a `connecting`/`verifying` attempt is redialling (023), if it is one. Rendered by
 * App so the chat screen stays mounted across a redial — the way it does across a self-probe heal. */
export function redialTarget(s: SessionState): Live | null {
  return (s.name === 'connecting' || s.name === 'verifying') && s.redial ? s.redial : null;
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

// ---- the handshake bound (033) ---------------------------------------------------------------

export const HANDSHAKE_SLOW_MS = 8_000;
export const HANDSHAKE_MS = 20_000;

/**
 * Bounds one attempt's handshake: `slow` at 8 s so the wait is visibly alive, `fail` at 20 s — the
 * bridge's own `connect` retries for 60 s, and a stranger reads a spinner that long as broken. Armed
 * on entering `connecting`; the returned stop runs on the way out, so 19 s is never failed at 20.
 */
export function boundHandshake(dispatch: (e: SessionEvent) => void, fail: () => void): () => void {
  const slow = setTimeout(() => dispatch({ t: 'slow' }), HANDSHAKE_SLOW_MS);
  const dead = setTimeout(fail, HANDSHAKE_MS);
  return () => {
    clearTimeout(slow);
    clearTimeout(dead);
  };
}

/**
 * What a handshake that never came up says, from the only witness: the bridge's log. A relay map it
 * could not fetch is this network, not the host; every other line is the host not answering the
 * meow — a sleeping machine's shape and (measured) a blocked relay's too. Last line → Details.
 */
export function handshakeFailure(log: string[], host: string): FriendlyError {
  const last = log[log.length - 1] ?? '';
  const said = last === '' ? {} : { hostSaid: last };
  const map = /fetching DERPMap for region (\S+): Get "https?:\/\/([^/"]+)/.exec(last);
  if (map) {
    return {
      title: tr('app_can_t_reach_the_relay_from_this_network'),
      detail: tr('app_relay_directory_unreachable', { directory: map[2]!, region: map[1]! }),
      ...said,
    };
  }
  return {
    title: tr('app_host_didn_t_answer', { host: host.trim() || tr('app_the_host') }),
    detail: tr('app_it_s_probably_asleep_or_offline_ask_them_to'),
    ...said,
  };
}

// ---- what the surfaces say -------------------------------------------------------------------

/**
 * The path, as text, never a dot (docs/PRINCIPLES.md). When the last measurement failed the line names
 * the host that stopped answering and dates the last good number, so nothing on screen is a live
 * latency that is not live (014 promise 3).
 */
export function pathLine(l: Live, now: number): string {
  if (l.pathOk && l.meOk) return describePath(l.path, l.me.host.relay.region);
  const who = l.me.host.name.trim() || tr('app_the_host');
  if (!l.path) return tr('app_path_not_answering', { host: who });
  return tr('app_path_not_answering_last', { host: who, rtt: Math.round(l.path.rttMs), ago: ago(now - l.pathAt) });
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
        ? tr('app_meter_minute_unknown', { limit: rpm })
        : rpm > 0
          ? tr(left === 1 ? 'app_meter_message_left' : 'app_meter_messages_left', { count: left })
          : tr('app_meter_messages_used', { count: used }),
      value: unknown || rpm === 0 ? 0 : used / rpm,
      unknown,
    },
    {
      label: unknown
        ? tr('app_meter_daily_unknown', { limit: compact(daily) })
        : daily > 0
          ? tr('app_meter_daily', { used: compact(today), limit: compact(daily) })
          : tr('app_meter_daily_uncapped', { used: compact(today) }),
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
    label: unknown ? tr('app_meter_context_unknown', { limit: compact(modelContext) }) : tr('app_meter_context', { used: compact(used), limit: compact(modelContext) }),
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
  const who = l.me.host.name.trim() || tr('app_your_host_variant');
  if (reason === 'key') {
    switch (l.key) {
      case 'paused':
        return tr('app_invite_paused_banner', { host: who });
      case 'revoked':
        return tr('app_invite_revoked_banner', { host: who });
      case 'invalid':
        return tr('app_invite_invalid_banner', { host: who });
      default:
        return null;
    }
  }
  return tr('app_engine_offline_banner', { engine: l.me.host.upstream.kind });
}

/** An invite that no waiting can bring back: the only move is a new code (020 promise 5). */
export function keyDead(l: Live): boolean {
  return l.key === 'revoked' || l.key === 'invalid';
}

export function ago(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return tr('app_ago_seconds', { count: s });
  if (s < 3600) return tr('app_ago_minutes', { count: Math.round(s / 60) });
  return tr('app_ago_hours', { count: Math.round(s / 3600) });
}

/** A wait, rendered as a wait: seconds up to ten minutes, then something a human can plan around. */
export function waitText(ms: number): string {
  const s = Math.max(0, Math.ceil(ms / 1000));
  if (s <= 600) return `${s}s`;
  const m = Math.round(s / 60);
  return m < 120 ? `${m} min` : `${Math.round(m / 60)} h`;
}
