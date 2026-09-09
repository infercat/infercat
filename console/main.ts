document.querySelector('#version')!.textContent = import.meta.env.VITE_APP_VERSION || 'development';
// The URL fragment is consumed once; never persist or transmit it in a URL.
const token = new URLSearchParams(location.hash.slice(1)).get('token');
history.replaceState(null, '', location.pathname);
export function adminHeaders(): HeadersInit { return token ? { Authorization: `Bearer ${token}` } : {}; }
