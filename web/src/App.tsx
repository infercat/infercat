import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { getMe, hostName } from './api';
import { PRODUCT_NAME } from './product';
import Connect from './ui/Connect';
import {
  dropped,
  IDLE,
  live,
  reduce,
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
    void (async () => {
      try {
        const opened = await openTransport(from.addr, {
          mode: from.mode,
          directURL: __DEFAULT_DIRECT_URL__,
          ...(from.ephemeral ? {} : { privateKey: load<string>(KEYS.privateKey, '') }),
        });
        if (!live) {
          opened.transport.close();
          return;
        }
        dispatch({ t: 'sessionUp', transport: opened.transport });
        const me = await getMe(opened.transport, from.secret);
        dispatch({
          t: 'verified',
          live: {
            ...from,
            transport: opened.transport,
            me,
            path: opened.path,
            pathAt: Date.now(),
            pathOk: opened.path !== null,
            meOk: true,
            key: me.key.status,
          },
        });
      } catch (err) {
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
      } finally {
        if (live) setDialling(false);
      }
    })();
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

export default function App() {
  const [state, dispatch] = useSession();
  const [dialling, redial] = useRedial(state, dispatch);
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
