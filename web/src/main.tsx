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
    });
  }
  const root = document.getElementById('root');
  if (root) createRoot(root).render(<StrictMode><App /></StrictMode>);
}

void boot();
