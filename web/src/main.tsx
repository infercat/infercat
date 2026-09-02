import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import './styles.css';

async function boot(): Promise<void> {
  // Dev-only: ?fake installs the in-page stand-in for the wasm bridge so Tunnel mode can be
  // exercised (and screenshotted) before ticket 001's artifact exists.
  const q = new URLSearchParams(location.search);
  if (import.meta.env.DEV && q.has('fake')) {
    const { installFakeTunnel } = await import('../dev/fake-bunny-tunnel.ts');
    installFakeTunnel({
      ...(q.has('connectMs') ? { connectMs: Number(q.get('connectMs')) } : {}),
      ...(q.has('tokenDelay') ? { tokenDelayMs: Number(q.get('tokenDelay')) } : {}),
      // Dev-only switches for the states ticket 007 has to show: a host that logs prompts, an
      // engine that is down, a host with nothing loaded, a path that cannot be measured, and the
      // three ways a stream can end badly.
      ...(q.has('logPrompts') ? { logPrompts: true } : {}),
      ...(q.has('upstreamDown') ? { upstreamDown: true } : {}),
      ...(q.has('pingFails') ? { pingFails: true } : {}),
      ...(q.has('pingFailsAfter') ? { pingFailsAfter: Number(q.get('pingFailsAfter')) } : {}),
      ...(q.has('models') ? { models: (q.get('models') ?? '').split(',').filter((m) => m !== '') } : {}),
      ...(q.has('streamMode')
        ? { streamMode: q.get('streamMode') as 'error-mid-stream' | 'eof-no-done' | 'reasoning-only' }
        : {}),
    });
  }
  const root = document.getElementById('root');
  if (root) createRoot(root).render(<StrictMode><App /></StrictMode>);
}

void boot();
