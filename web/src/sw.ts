/// <reference lib="webworker" />
// Only build-generated static URLs enter these caches. No request body, invite, /me or API data.
declare const __PRECACHE__: { revision: string; version: string; shell: string[]; runtime: string[] };
const worker = self as unknown as ServiceWorkerGlobalScope;
const shellName = `infercat-shell-${__PRECACHE__.revision}`;
const runtimeName = `infercat-runtime-${__PRECACHE__.version}`;
const owned = (name: string) => name.startsWith('infercat-shell-') || name.startsWith('infercat-runtime-');
const allowed = new Set([...__PRECACHE__.shell, ...__PRECACHE__.runtime]);

worker.addEventListener('install', (event) => {
  event.waitUntil((async () => {
    const shell = await caches.open(shellName);
    await shell.addAll(__PRECACHE__.shell.map((url) => new Request(url, { cache: 'reload' })));
    const runtime = await caches.open(runtimeName);
    if (!(await Promise.all(__PRECACHE__.runtime.map((url) => runtime.match(url)))).every(Boolean)) {
      // Obtain the pair before writing either entry. Failed upgrades leave the active worker alone.
      const responses = await Promise.all(__PRECACHE__.runtime.map((url) => fetch(url, { cache: 'reload' })));
      if (responses.some((response) => !response.ok)) throw new Error('Runtime precache failed');
      await Promise.all(responses.map((response, index) => runtime.put(__PRECACHE__.runtime[index]!, response)));
    }
  })());
  // No skipWaiting: an existing page keeps its matching runtime/chunks until its window closes.
});
worker.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const names = await caches.keys();
    // A still-newer worker can already be installing: only retire older cache entries.
    await Promise.all(names.slice(0, names.indexOf(shellName)).filter((name) => owned(name) && name !== runtimeName).map((name) => caches.delete(name)));
    await worker.clients.claim();
  })());
});
worker.addEventListener('fetch', (event) => {
  const request = event.request, url = new URL(request.url);
  if (request.method !== 'GET' || url.origin !== worker.location.origin) return;
  if (request.mode === 'navigate') {
    if (url.pathname !== '/' && url.pathname !== '/index.html') return;
    event.respondWith((async () => {
      try { const response = await fetch(request); if (response.ok && !response.redirected && response.type !== 'opaqueredirect') return response; } catch { /* Offline shell. */ }
      // Never overwrite this version's HTML with a newer navigation response and older assets.
      return (await caches.match('/index.html', { cacheName: shellName })) ?? Response.error();
    })());
    return;
  }
  // Exact URLs only: a query containing an invite or API parameters cannot become an asset entry.
  if (url.search || !allowed.has(url.pathname)) return;
  event.respondWith((async () => {
    // A finishing old fetch must not recreate a cache the new worker has already retired.
    const cacheName = __PRECACHE__.runtime.includes(url.pathname) ? runtimeName : shellName;
    return (await caches.match(url.pathname, { cacheName })) ?? fetch(request);
  })());
});
