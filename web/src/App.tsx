import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { describeError, getMe, hostName, ME_TIMEOUT_MS, timeoutSignal } from './api';
import { PRODUCT_NAME } from './product';
import Connect from './ui/Connect';
import {
  dropped,
  IDLE,
  live,
  probing,
  reduce,
  scheduleProbe,
  transportOf,
  type Live,
  type SessionEvent,
  type SessionState,
} from './session';
import { KEYS, load } from './storage';
import { openTransport, type Transport } from './transport';

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
  const [state, setState] = useState<SessionState>(IDLE);
  const cur = useRef<SessionState>(IDLE);

  const dispatch = useCallback((e: SessionEvent) => {
    const prev = cur.current;
    const next = reduce(prev, e);
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

function shut(t: Transport): void {
  try {
    t.close();
  } catch {
    /* already gone; closing twice is not an error worth showing anyone */
  }
}

/**
 * The same host, dialled again: a new transport to the same address, verified with the same invite,
 * carrying everything the old session knew. What Reconnect does by hand (014 promise 13) and the
 * self-probe does by itself (022 promise 1). `onOpened` sees the transport before /me is asked; a
 * transport the invite does not verify over is closed here.
 */
async function dialAgain(from: Live, onOpened?: (t: Transport) => void): Promise<Live> {
  const opened = await openTransport(from.addr, {
    mode: from.mode,
    directURL: __DEFAULT_DIRECT_URL__,
    ...(from.ephemeral ? {} : { privateKey: load<string>(KEYS.privateKey, '') }),
  });
  onOpened?.(opened.transport);
  try {
    const me = await getMe(opened.transport, from.secret);
    return {
      ...from,
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
 * Dialling the same host again after its session broke (014 promise 13).
 *
 * A tunnel session that has failed stays failed, so retrying a request over it costs the reader
 * another 30 s and tells them nothing. `redial` drops the session — the reducer returns to
 * `connecting`, and the one closer above closes what it dropped — and this effect does what a
 * reload plus Connect would do, without the reload: open a new transport to the same address and
 * re-verify the same invite. It runs only on a redial (the ref is set by the dispatcher it
 * returns), so the connect screen's own attempt is never raced by it.
 */
function useRedial(
  state: SessionState,
  dispatch: (e: SessionEvent) => void,
): [boolean, (l: Live) => void] {
  const target = useRef<Live | null>(null);
  const [dialling, setDialling] = useState(false);

  useEffect(() => {
    const from = target.current;
    if (state.name !== 'connecting' || !from) return;
    target.current = null;
    let live = true;
    // From `sessionUp` on the machine owns the transport: every path out of `verifying` closes it.
    void dialAgain(from, (t) => (live ? dispatch({ t: 'sessionUp', transport: t }) : shut(t)))
      .then((next) => (live ? dispatch({ t: 'verified', live: next }) : shut(next.transport)))
      .catch((err: unknown) => {
        if (!live) return;
        const who = hostName(from.me);
        // A second failure is not a third invitation to wait: say what actually works.
        dispatch({
          t: 'abort',
          error: {
            title: `Still can’t reach ${who || 'your host'}`,
            detail: 'Reload this page to start a fresh connection.',
            ...(err instanceof Error && err.message.trim() !== ''
              ? { hostSaid: err.message.trim() }
              : {}),
          },
        });
      })
      .finally(() => {
        if (live) setDialling(false);
      });
    return () => {
      live = false;
    };
  }, [state.name, dispatch]);

  return [
    dialling,
    (l: Live) => {
      target.current = l;
      setDialling(true);
      dispatch({ t: 'redial' });
    },
  ];
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
        if (!l) return;
        try {
          if (l.meOk) {
            dispatch({ t: 'meOk', me: await getMe(l.transport, l.secret, timeoutSignal(ME_TIMEOUT_MS)) });
          } else {
            // Lands in the reducer, which keeps it only while this session still needs replacing;
            // a candidate that arrives after Reconnect or Disconnect is closed by dropped().
            dispatch({ t: 'verified', live: await dialAgain(l) });
          }
        } catch (err) {
          if (l.meOk) dispatch({ t: 'meError', error: describeError(err, hostName(l.me)) });
        }
      },
      dispatch,
    );
  }, [on, probed, dispatch]);
}

export default function App() {
  const [state, dispatch] = useSession();
  const [dialling, redial] = useRedial(state, dispatch);
  useProbe(state, dispatch);
  const l = live(state);

  if (!l) {
    return dialling ? (
      <main className="connect">
        <div className="connect-card">
          <h1>{PRODUCT_NAME}</h1>
          <p className="pitch">Opening a fresh connection to your host…</p>
        </div>
      </main>
    ) : (
      <Connect state={state} dispatch={dispatch} />
    );
  }

  return (
    <Suspense fallback={<div className="booting">Opening…</div>}>
      {/* Remounting per host is what makes the host-scoped store load cleanly for the new one. */}
      <Chat key={`${l.addr}/${l.me.key.id}`} state={state} live={l} dispatch={dispatch} onRedial={redial} />
    </Suspense>
  );
}
