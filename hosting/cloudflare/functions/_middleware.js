// The first-party counter (hosting/README.md): one data point per page view and per app load,
// recorded at the edge and then the asset is served as usual. Nothing runs in the friend's browser
// for this, no cookie is set, no third party is contacted, and the invite in the URL fragment never
// reaches a server because browsers do not send fragments. _routes.json limits this function to the
// two counted paths and the signup endpoint; every other asset is served statically.
export async function onRequest({ request, env, next }) {
  const response = await next();
  try {
    const url = new URL(request.url);
    if (url.pathname !== '/' && url.pathname !== '/infercat.wasm.gz') return response;
    const kind = url.pathname === '/infercat.wasm.gz' ? 'load' : 'view';
    const cf = request.cf || {};
    const ua = request.headers.get('user-agent') || '';
    let referrer = '';
    try { referrer = new URL(request.headers.get('referer') || '').hostname; } catch {}
    const from = (url.searchParams.get('from') || '').slice(0, 32);
    env.LOADS?.writeDataPoint({
      indexes: [kind],
      blobs: [String(cf.country || ''), family(ua), referrer, from],
      doubles: [1],
    });
  } catch {}
  return response;
}

// Browser and platform family only, never a full user agent string.
function family(ua) {
  const os = /iPhone|iPad/.test(ua) ? 'ios' : /Android/.test(ua) ? 'android' : /Macintosh/.test(ua) ? 'mac' : /Windows/.test(ua) ? 'windows' : /Linux/.test(ua) ? 'linux' : 'other';
  const browser = /Firefox\//.test(ua) ? 'firefox' : /Edg\//.test(ua) ? 'edge' : /OPR\//.test(ua) ? 'opera' : /Chrome\//.test(ua) ? 'chrome' : /Safari\//.test(ua) ? 'safari' : 'other';
  return `${os}/${browser}`;
}
