import { takeAdminRoute } from './admin-route';
import { tr } from './i18n/text';
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { describeError, getMe, hostName, ME_TIMEOUT_MS, timeoutSignal, type Me } from './api';
import Connect, { arrivedByLink } from './ui/Connect';
import { decodeInvite } from './invite';
import { platform } from './install';
import {
  dropped,
  IDLE,
  live,
  probing,
  reduce,
  redialTarget,
  scheduleProbe,
  transportOf,
  type Live,
  type SessionEvent,
  type SessionState,
} from './session';
import { KEYS, load, save, scopedKeys, hostScope, loadChats, type LastHost } from './storage';
import { VERSION } from './product';
import { claimTunnelIdentity, openTransport, type Transport } from './transport';

declare const __DEFAULT_DIRECT_URL__: string;

// The chat screen pulls in the markdown renderer and its highlighter. Keeping it out of the entry
// chunk means the landing page — the thing a stranger sees first — stays small.
const Chat = lazy(() => import('./ui/Chat'));

/**
 * The session machine, plus the one place in the app that closes a Transport. `reduce` decides
 * what each transition drops (including a connect attempt that landed after a newer one superseded
 * it); this enacts that decision and nothing else does. That is why there is no leak to find.
 */
export function useSession(): [SessionState, (e: SessionEvent) => void] {
  const [state, setState] = useState<SessionState>(() => restoredSession());
  const cur = useRef<SessionState>(state);

  const dispatch = useCallback((e: SessionEvent) => {
    const prev = cur.current;
    const next = reduce(prev, e);
    if (acceptedVerification(prev, e, next)) {
      save(scopedKeys(hostScope(e.live.addr)).me, e.live.me);
      if (!e.live.ephemeral && e.live.privateKeyJSON) save(KEYS.privateKey, e.live.privateKeyJSON);
    }
    for (const t of dropped(prev, e, next)) shut(t);
    if (next === prev) return;
    cur.current = next;
    setState(next);
  }, []);

  // Leaving the page is an exit transition too.
  useEffect(() => {
    return () => {
      const t = transportOf(cur.current);
      if (t) shut(t);
    };
  }, []);

  return [state, dispatch];
}

/** A closed shell transport cannot accidentally issue requests before verification. */
export function shellTransport(): Transport {
  return { kind: 'tunnel', close() {}, ping: async () => null, fetch: async () => { throw new TypeError('No verified connection'); } };
}
export function restoredSession(online = typeof navigator === 'undefined' || navigator.onLine !== false, standalone = typeof matchMedia !== 'undefined' && platform().standalone, viaLink = arrivedByLink()): SessionState {
  if (viaLink || (online && !standalone)) return IDLE;
  const last = load<LastHost | null>(KEYS.lastHost, null);
  if (!last || last.left) return IDLE;
  try {
    const { addr, secret } = decodeInvite(load<string>(KEYS.invite, ''));
    const scope = hostScope(addr), me = load<Me | null>(scopedKeys(scope).me, null);
    if (scope !== last.scope || !loadChats(scope).length || !usableSnapshot(me)) return IDLE;
    const held: Live = { addr, secret, me, transport: shellTransport(), mode: 'tunnel', path: null, pathAt: 0, pathOk: false, meOk: false, key: me.key.status, ephemeral: true, probed: 0, snapshot: true, offline: !online };
    return online ? { name: 'connecting', redial: held } : { name: 'degraded', reason: 'path', live: held };
  } catch { return IDLE; }
}
function usableSnapshot(me: Me | null): me is Me {
  return !!me && typeof me.key?.id === 'string' && typeof me.key.name === 'string' && ['active', 'paused', 'revoked'].includes(me.key.status)
    && typeof me.host?.name === 'string' && Array.isArray(me.host.models) && me.host.models.every((m) => typeof m === 'string')
    && typeof me.host.relay?.region === 'string' && typeof me.host.upstream?.kind === 'string' && typeof me.host.upstream.healthy === 'boolean'
    && Number.isFinite(me.host.upstream.model_context)
    && ['rpm', 'tpm', 'max_concurrent', 'max_output_tokens', 'max_context', 'daily_tokens'].every((k) => Number.isFinite(me.limits?.[k as keyof Me['limits']]))
    && ['rpm_used', 'tpm_used', 'today_tokens', 'in_flight'].every((k) => Number.isFinite(me.usage?.[k as keyof Me['usage']]));
}

/** Snapshot only an accepted verify, never a candidate the pure reducer rejected. */
export function acceptedVerification(before: SessionState, event: SessionEvent, after: SessionState): event is Extract<SessionEvent, { t: 'verified' }> {
  return event.t === 'verified' && before !== after && live(after)?.transport === event.live.transport;
}

function shut(t: Transport): void {
  try {
    t.close();
  } catch {
    /* already gone; closing twice is not an error worth showing anyone */
  }
}

/** One dial per host at a time (023): whoever asks while one is in flight joins it. */
const dialling = new Map<Transport, Promise<Live>>();

/**
 * The same host, dialled again, carrying everything the old session knew: Reconnect by hand (014
 * promise 13) and the self-probe by itself (022 promise 1). A second ask while a dial is in flight
 * joins it (023) — two sessions under one identity leave one deaf at the relay. A candidate stays
 * private until /me verifies; failed or timed-out candidates are closed here. A new shell transport
 * after going offline cannot accidentally join the obsolete dial.
 */
function dialAgain(from: Live): Promise<Live> {
  const joined = dialling.get(from.transport);
  if (joined) return joined;
  const dial = dialFresh(from);
  dialling.set(from.transport, dial);
  const done = () => dialling.delete(from.transport);
  dial.then(done, done);
  return dial;
}

let restoredIdentity: Promise<boolean> | undefined;
async function dialFresh(from: Live): Promise<Live> {
  const exclusive = from.snapshot ? await (restoredIdentity ??= claimTunnelIdentity()) : !from.ephemeral;
  const opened = await openTransport(from.addr, {
    mode: from.mode,
    assetBase: import.meta.env.PROD ? `/runtime/${encodeURIComponent(VERSION)}/` : undefined,
    directURL: __DEFAULT_DIRECT_URL__,
    ...(!exclusive ? {} : { privateKey: load<string>(KEYS.privateKey, '') }),
  });
  try {
    const me = await getMe(opened.transport, from.secret, timeoutSignal(ME_TIMEOUT_MS));
    return {
      ...from,
      offline: false, snapshot: false, ephemeral: !exclusive, privateKeyJSON: opened.privateKeyJSON,
      transport: opened.transport,
      me,
      path: opened.path,
      pathAt: Date.now(),
      pathOk: opened.path !== null,
      meOk: true,
      key: me.key.status,
      probed: 0,
    };
  } catch (err) {
    shut(opened.transport);
    throw err;
  }
}

/**
 * Dialling the same host again after its session broke (014 promise 13): a failed tunnel session
 * stays failed, so `redial` carries it into `connecting` as the target and this effect opens a new
 * transport and re-verifies the same invite. Its lifetime is the target's (023): it spans
 * `connecting` and `verifying`, joins the self-probe's dial when one is in flight, and a failure
 * hands the target back as a degraded session, not the connect screen. The card's own attempts
 * carry no target, so they are never raced by it.
 */
function useRedial(state: SessionState, dispatch: (e: SessionEvent) => void): void {
  const from = state.name === 'connecting' || state.name === 'verifying' ? (state.redial ?? null) : null;
  const active = useRef<Live | null>(null);
  useEffect(() => {
    if (!from || navigator.onLine === false) return;
    active.current = from;
    // StrictMode may replay the effect: both observers share one dial and the current owner.
    // The candidate stays private until /me answers, then the reducer adopts or closes it.
    void dialAgain(from)
      .then((next) => (active.current === from ? dispatch({ t: 'verified', live: next }) : shut(next.transport)))
      .catch((err: unknown) => {
        if (active.current === from) dispatch({ t: 'meError', error: describeError(err, hostName(from.me)) });
      });
    return () => {
      active.current = null;
    };
  }, [from, dispatch]);
}

/**
 * The self-probe (022 promise 1). A session degraded for a reason that can heal — the host stopped
 * answering, its engine is down, the path cannot be measured — asks again by itself: 5 s after it
 * noticed, then 10, 20 and every 30 s (session.probeDelay), so a host that is up is never called
 * "not answering" for more than one step. What it asks depends on what failed. A host that answers
 * /me is asked /me over the session it has, exactly as the poll does. A host that did not gets a
 * fresh dial: a tunnel session whose host has restarted behind it never answers again (measured
 * against the real host — four 30 s polls over two minutes, nothing), so the only probe that can
 * find it awake is the one Reconnect makes by hand; the new session replaces the old in the
 * reducer the moment the invite verifies over it, and the old is closed there. One effect, armed
 * on the count of misses, never on the poll's other news, so a 30 s poll cannot keep resetting a
 * 30 s probe.
 */
function useProbe(state: SessionState, dispatch: (e: SessionEvent) => void): void {
  const latest = useRef(state);
  useEffect(() => {
    latest.current = state;
  });
  const on = probing(state);
  const probed = live(state)?.probed ?? 0;
  useEffect(() => {
    if (!on) return;
    return scheduleProbe(
      probed,
      async () => {
        const l = live(latest.current);
        if (!l || l.offline || navigator.onLine === false) return;
        try {
          if (l.meOk) {
            const me = await getMe(l.transport, l.secret, timeoutSignal(ME_TIMEOUT_MS));
            if (live(latest.current)?.transport === l.transport && !live(latest.current)?.offline) dispatch({ t: 'meOk', me });
          } else {
            // Lands in the reducer, which keeps it only while this session still needs replacing;
            // a candidate that arrives after Reconnect or Disconnect is closed by dropped().
            const next = await dialAgain(l);
            const current = live(latest.current) ?? redialTarget(latest.current);
            if (current?.transport === l.transport && !current.offline) dispatch({ t: 'verified', live: next });
            else shut(next.transport);
          }
        } catch (err) {
          if (l.meOk && live(latest.current)?.transport === l.transport && !live(latest.current)?.offline) dispatch({ t: 'meError', error: describeError(err, hostName(l.me)) });
        }
      },
      dispatch,
    );
  }, [on, probed, dispatch]);
}

/** Device events reuse the redial path; pending messages are never replayed. */
function useDevice(state: SessionState, dispatch: (e: SessionEvent) => void): boolean {
  const [offline, setOffline] = useState(() => navigator.onLine === false);
  const latest = useRef(state); latest.current = state;
  useEffect(() => {
    let hiddenAt: number | null = document.hidden ? Date.now() : null;
    const off = () => { setOffline(true); dispatch({ t: 'offline', transport: shellTransport() }); };
    const on = () => {
      if (navigator.onLine === false) return;
      setOffline(false);
      if (live(latest.current)?.offline) dispatch({ t: 'redial' });
    };
    const visibility = () => {
      if (document.hidden) { hiddenAt = Date.now(); return; }
      const resume = hiddenAt !== null && Date.now() - hiddenAt > 30_000; hiddenAt = null;
      if (resume && platform().standalone && navigator.onLine !== false && live(latest.current)) dispatch({ t: 'redial' });
    };
    window.addEventListener('offline', off); window.addEventListener('online', on); document.addEventListener('visibilitychange', visibility);
    return () => { window.removeEventListener('offline', off); window.removeEventListener('online', on); document.removeEventListener('visibilitychange', visibility); };
  }, [dispatch]);
  return offline;
}

function ChatApp({onAdmin}:{onAdmin:(code:string)=>void}) {
  const [state, dispatch] = useSession();
  const offline = useDevice(state, dispatch);
  useRedial(state, dispatch);
  useProbe(state, dispatch);
  // During a redial the chat stays mounted, rendered from the session being redialled (023): same
  // host and key, same React key, nothing torn down. A chat remount mid-reconnect leaves the fresh
  // tunnel session unusable (measured); a self-probe heal never remounts, and now nor does Reconnect.
  const reconnecting = redialTarget(state);
  const l = live(state) ?? reconnecting;

  if (!l) return <Connect state={state} dispatch={dispatch} offline={offline} onAdmin={onAdmin} />;

  return (
    <Suspense fallback={<div className="booting">{tr('app_opening')}</div>}>
      {/* Remounting per host is what makes the host-scoped store load cleanly for the new one. */}
      <Chat
        key={l.addr}
        state={state}
        live={l}
        reconnecting={reconnecting !== null}
        dispatch={dispatch}
        onRedial={() => dispatch({ t: 'redial' })}
      />
    </Suspense>
  );
}

const RemoteConsole=lazy(()=>import('./ui/RemoteConsole'));
let incoming=takeAdminRoute(),routeNumber=0;
export default function App(){
 const [route,setRoute]=useState(()=>({...incoming,version:routeNumber}));
 const openAdmin=useCallback((code:string)=>{history.pushState(null,'','/console'+location.search);setRoute({console:true,code,version:++routeNumber});},[]);
 const leave=useCallback(()=>{history.pushState(null,'','/'+location.search);setRoute({console:false,code:'',version:++routeNumber});},[]);
 useEffect(()=>{incoming={console:false,code:''};const pop=()=>{const next=takeAdminRoute();setRoute(prev=>!next.code&&prev.console===next.console?prev:{...next,version:++routeNumber});};window.addEventListener('popstate',pop);window.addEventListener('hashchange',pop);return()=>{window.removeEventListener('popstate',pop);window.removeEventListener('hashchange',pop);};},[]);
 return route.console?<Suspense fallback={<div className="booting">{tr('app_opening')}</div>}><RemoteConsole key={route.version} code={route.code} onLeave={leave}/></Suspense>:<ChatApp onAdmin={openAdmin}/>;
}
