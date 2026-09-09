import { render } from './render';
import type { Lang } from './copy';
import type { Snapshot } from './types';

export function createConsole(root: HTMLElement, token: string | null, request: typeof fetch = fetch, now = Date.now) {
 let data: Snapshot | null = null, selected: string | null = null, stale: number | null = null;
 let lang: Lang = new URLSearchParams(location.search).get('lang') === 'zh' ? 'zh' : 'en';
 let lastAnswer = now(), busy = false, stopped = false, authorized = !!token;
 let activeRequest: AbortController | null = null;
 const headers: HeadersInit = token ? { Authorization: `Bearer ${token}` } : {};
 function draw(focusClose = false) {
  const scroll = root.querySelector('.drawer')?.scrollTop || 0;
  const open = root.querySelector('details')?.open || false;
  const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const key = focused?.dataset.key, close = focused?.dataset.close, language = focused?.dataset.lang;
  root.innerHTML = render(data, lang, selected, stale, now(), authorized, Math.max(0, Math.floor((now() - lastAnswer) / 1000)));
  document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en';
  document.body.classList.toggle('zh', lang === 'zh');
  const details = root.querySelector('details'); if (details) details.open = open;
  const drawer = root.querySelector('.drawer'); if (drawer) drawer.scrollTop = scroll;
  if (focusClose || close) root.querySelector<HTMLButtonElement>('button[data-close]')?.focus();
  else if (key) [...root.querySelectorAll<HTMLElement>('[data-key]')].find(el => el.dataset.key === key)?.focus({ preventScroll: true });
  else if (language) root.querySelector<HTMLButtonElement>(`[data-lang="${language}"]`)?.focus({ preventScroll: true });
 }
 function closeDrawer() {
  const id = selected; selected = null; draw();
  [...root.querySelectorAll<HTMLElement>('[data-key]')].find(el => el.dataset.key === id)?.focus({ preventScroll: true });
 }
 const click = (event: Event) => {
  const target = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-key],[data-close],[data-lang]') : null;
  if (target?.dataset.close) { closeDrawer(); return; }
  if (target?.dataset.lang) { lang = target.dataset.lang as Lang; draw(); return; }
  if (target?.dataset.key) { selected = target.dataset.key; draw(true); }
 };
 const keydown = (event: KeyboardEvent) => {
  if (selected && event.key === 'Escape') { event.preventDefault(); closeDrawer(); }
  else if (selected && event.key === 'Tab') { event.preventDefault(); root.querySelector<HTMLButtonElement>('button[data-close]')?.focus(); }
  else if ((event.key === 'Enter' || event.key === ' ') && (event.target as HTMLElement)?.dataset.key) { event.preventDefault(); click(event); }
 };
 async function refresh() {
  if (!authorized || busy || stopped) return;
  busy = true; activeRequest = new AbortController();
  const timeout = setTimeout(() => activeRequest?.abort(), 5000);
  try {
   const paths = ['status', 'keys', 'engine', 'settings', 'usage?window=today', 'usage?window=week'];
   const values = await Promise.all(paths.map(async path => {
    const response = await request('/api/' + path, { headers, signal: activeRequest!.signal, cache: 'no-store' });
    if (response.status === 401) authorized = false;
    if (!response.ok) throw new Error('host did not answer');
    return response.json();
   }));
   if (stopped) return;
   data = { status: values[0], keys: values[1], engine: values[2], settings: values[3], today: values[4], week: values[5] };
   if (selected && !data.keys.some(k => k.id === selected)) selected = null;
   lastAnswer = now(); stale = null;
  } catch { activeRequest?.abort(); if (!stopped) stale = Math.max(1, Math.floor((now() - lastAnswer) / 1000)); }
  finally { clearTimeout(timeout); busy = false; if (!stopped) draw(); }
 }
 root.addEventListener('click', click); root.addEventListener('keydown', keydown);
 draw(); void refresh();
 const timer = setInterval(() => {
  if (stale !== null) { stale = Math.max(1, Math.floor((now() - lastAnswer) / 1000)); draw(); }
  void refresh();
 }, 2000);
 return { refresh, stop() { stopped = true; activeRequest?.abort(); clearInterval(timer); root.removeEventListener('click', click); root.removeEventListener('keydown', keydown); } };
}
