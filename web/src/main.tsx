import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { initializeInstall } from './install';
import './styles.css';

initializeInstall();
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  void navigator.serviceWorker.register('/sw.js', { updateViaCache: 'none' }).catch((error: unknown) => console.warn('Offline shell unavailable', error));
}

async function boot(): Promise<void> {
  // Dev-only: ?fake installs the in-page stand-in for the wasm bridge so Tunnel mode can be
  // exercised (and screenshotted) before ticket 001's artifact exists.
  const q = new URLSearchParams(location.search);
  if (import.meta.env.DEV && q.has('fake')) {
    const { installFakeTunnel } = await import('../dev/fake-infercat-tunnel.ts');
    installFakeTunnel({
      hostTools: q.get('hostTools') ? q.get('hostTools')!.split(',') : q.has('hostTools'), imageJobs: q.has('imageJobs'), agent: q.has('agent'), runState: q.get('runState') ?? 'running',
      transcriptions: q.has('transcriptions'), speech: q.has('speech'),
      ...(q.has('audioFailure') ? { audioFailure: Number(q.get('audioFailure')) } : {}),
      ...(q.has('audioDelay') ? { audioDelayMs: Number(q.get('audioDelay')) } : {}),
      ...(q.has('vision') ? { vision: q.get('vision') === 'true' ? true : q.get('vision') === 'false' ? false : null } : {}),
      ...(q.has('rejectImages') ? { rejectImages: true } : {}),
      ...(q.has('connectMs') ? { connectMs: Number(q.get('connectMs')) } : {}),
      ...(q.has('tokenDelay') ? { tokenDelayMs: Number(q.get('tokenDelay')) } : {}),
      // Dev-only switches for the states ticket 007 has to show: a host that logs prompts, an
      // engine that is down, a host with nothing loaded, a path that cannot be measured, and the
      // three ways a stream can end badly.
      ...(q.has('logPrompts') ? { logPrompts: true } : {}),
      ...(q.has('upstreamDown') ? { upstreamDown: true } : {}),
      // 014's three surfaces: a host that went away, an invite the host paused, and a /me that
      // stops answering so the meters have to say "unknown" instead of "zero".
      ...(q.has('hostAsleep') ? { hostAsleep: true } : {}),
      ...(q.has('keyPaused') ? { keyPaused: true } : {}),
      ...(q.has('meFailsAfter') ? { meFailsAfter: Number(q.get('meFailsAfter')) } : {}),
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
