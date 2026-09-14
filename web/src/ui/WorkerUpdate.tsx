import { useEffect, useRef, useState } from 'react';
import { appLanguage, tr } from '../i18n/text';
/** Installation alone never activates an update. Only this page's explicit click reloads it. */
export default function WorkerUpdate() {
  const [language, setLanguage] = useState(appLanguage);
  const [waiting, setWaiting] = useState<ServiceWorker | null>(null), requested = useRef(false);
  useEffect(() => {
    if (!import.meta.env.PROD || !('serviceWorker' in navigator)) return;
    let disposed = false, reloaded = false, lastCheck = Date.now(), clean = () => {};
    const changed = () => { if (requested.current && !reloaded) { reloaded = true; location.reload(); } };
    const languageChanged = () => setLanguage(appLanguage());
    window.addEventListener('languagechange', languageChanged);
    navigator.serviceWorker.addEventListener('controllerchange', changed);
    void navigator.serviceWorker.register('/sw.js', { updateViaCache: 'none' }).then(registration => {
      if (disposed) return;
      const seen = new Set<ServiceWorker>(), listeners = new Map<ServiceWorker, () => void>();
      const offer = (worker: ServiceWorker | null) => { if (worker && navigator.serviceWorker.controller && !seen.has(worker)) { seen.add(worker); setWaiting(worker); } };
      const found = () => {
        const worker = registration.installing; if (!worker || listeners.has(worker)) return;
        const state = () => { if (worker.state === 'installed') offer(worker); };
        listeners.set(worker, state); worker.addEventListener('statechange', state); state();
      };
      const visible = () => { const now = Date.now(); if (document.visibilityState === 'visible' && now - lastCheck >= 3600000) { lastCheck = now; void registration.update().catch(() => {}); } };
      registration.addEventListener('updatefound', found); document.addEventListener('visibilitychange', visible);
      found(); offer(registration.waiting);
      clean = () => { registration.removeEventListener('updatefound', found); document.removeEventListener('visibilitychange', visible); listeners.forEach((fn, worker) => worker.removeEventListener('statechange', fn)); };
    }).catch((error: unknown) => console.warn('Offline shell unavailable', error));
    return () => { disposed = true; clean(); window.removeEventListener('languagechange', languageChanged); navigator.serviceWorker.removeEventListener('controllerchange', changed); };
  }, []);
  if (!waiting) return null;
  return <div className={`degraded follower worker-update ${language}`} role="status">
    <button className="ghost tiny" onClick={() => { if (!requested.current) { requested.current = true; waiting.postMessage({ type: 'skip-waiting' }); } }}>{tr('app_update_ready', {}, language)}</button>
    <button className="ghost tiny" onClick={() => setWaiting(null)}>{tr('app_dismiss', {}, language)}</button>
  </div>;
}
