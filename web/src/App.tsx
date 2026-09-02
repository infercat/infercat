import { lazy, Suspense, useState } from 'react';
import Connect from './ui/Connect';
import type { Me } from './api';
import type { PingResult, Transport } from './transport';

// The chat screen pulls in the markdown renderer and its highlighter. Keeping it out of the entry
// chunk means the landing page — the thing a stranger sees first — stays small.
const Chat = lazy(() => import('./ui/Chat'));

/** Everything a live connection to one host consists of. */
export interface Live {
  transport: Transport;
  secret: string;
  me: Me;
  path: PingResult | null;
  mode: 'direct' | 'tunnel';
}

export default function App() {
  const [live, setLive] = useState<Live | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  if (!live) {
    return (
      <Connect
        notice={notice}
        onConnected={(next) => {
          setNotice(null);
          setLive(next);
        }}
      />
    );
  }

  return (
    <Suspense fallback={<div className="booting">Opening…</div>}>
      <Chat
        live={live}
        onDisconnect={(reason) => {
          live.transport.close();
          setLive(null);
          setNotice(reason ?? null);
        }}
      />
    </Suspense>
  );
}
