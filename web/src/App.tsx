import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import Connect from './ui/Connect';
import {
  dropped,
  IDLE,
  live,
  reduce,
  transportOf,
  type SessionEvent,
  type SessionState,
} from './session';
import type { Transport } from './transport';

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

export default function App() {
  const [state, dispatch] = useSession();
  const l = live(state);

  if (!l) return <Connect state={state} dispatch={dispatch} />;

  return (
    <Suspense fallback={<div className="booting">Opening…</div>}>
      {/* Remounting per host is what makes the host-scoped store load cleanly for the new one. */}
      <Chat key={`${l.addr}/${l.me.key.id}`} state={state} live={l} dispatch={dispatch} />
    </Suspense>
  );
}
