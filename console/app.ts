import type { RemoteState } from './remote';
import { render } from './render';
import { text, type Lang } from './copy';
import { draft, type DrawerState, type Invite } from './actions';
import { inviteQR } from './qr';
import { settingValues, type SettingsUI, type SettingsPatch } from './settings';
import type { Snapshot } from './types';

export function createConsole(root: HTMLElement, token: string | null, request: typeof fetch = fetch, now = Date.now, encodeQR = inviteQR, remote?:RemoteState) {
 let data: Snapshot | null = null, selected: string | null = null, stale: number | null = null;
 let lang: Lang = new URLSearchParams(location.search).get('lang') === 'zh' ? 'zh' : new URLSearchParams(location.search).get('lang')==='en'?'en':remote?.language||'en';
 let lastAnswer = now(), stopped = false, authorized = !!token, pending = false;
 const settingsUI:SettingsUI={draft:{}};
 let ui: DrawerState | undefined, generation = 0, revision = 0;
 let activeRequest: AbortController | undefined, mutation: AbortController | undefined;
 let reading: Promise<boolean> | undefined, copyTimer: ReturnType<typeof setTimeout> | undefined;
 const headers: Record<string,string> = token ? { Authorization: `Bearer ${token}` } : {};
 const originalRequest=request;
 request=async(input,init)=>{
  const response=await originalRequest(input,init);
  if(remote){
   if(response.status===401){remote.refused=true;authorized=false;discard();selected=null;}
   if(response.status===404&&(input==='/api/status'||input==='/api/settings')){remote.closedAt=now();authorized=false;discard();selected=null;}
   if(response.status===429){const seconds=Number(response.headers.get('Retry-After'));remote.retryAt=now()+Math.max(1,Number.isFinite(seconds)&&seconds>0?seconds:60)*1000;}
  }
  return response;
 };
 const errorLine=(response:Response,value:{error?:unknown})=>response.status===429&&remote?text(lang,'remote_budget',Math.max(1,Math.ceil(((remote.retryAt||now())-now())/1000))):typeof value.error==='string'?value.error:'HTTP '+response.status;
 function draw(focusClose = false) {
  const scroll = root.querySelector('.drawer')?.scrollTop || 0;
  const open = root.querySelector('details')?.open || false;
  const focused = (root.getRootNode() instanceof ShadowRoot ? (root.getRootNode() as ShadowRoot).activeElement : document.activeElement) as HTMLElement|null;
  const identity = focused?.id || '', action = focused?.dataset.action, model = focused?.dataset.model;
  const key = focused?.dataset.key, close = focused?.dataset.close, language = focused?.dataset.lang;
  const selection = focused instanceof HTMLInputElement && focused.type === 'text' ? [focused.selectionStart, focused.selectionEnd] : null;
  root.innerHTML = render(data, lang, selected, stale, now(), authorized, Math.max(0, Math.floor((now() - lastAnswer) / 1000)), ui, pending,settingsUI,remote);
  document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en';
  (remote?root:document.body).classList.toggle('zh', lang === 'zh');
  if(remote)document.title=(data?.status.name||'Infercat')+' · Console · '+text(lang,'remote_suffix');
  const details = root.querySelector('details'); if (details) details.open = open;
  const drawer = root.querySelector('.drawer'); if (drawer) drawer.scrollTop = scroll;
  let next: HTMLElement | undefined | null;
  if (focusClose || close) next = root.querySelector<HTMLButtonElement>('button[data-close]');
  else if (identity) next = root.querySelector<HTMLElement>('#' + identity);
  else if (key) next = [...root.querySelectorAll<HTMLElement>('[data-key]')].find(el => el.dataset.key === key);
  else if (action) next = [...root.querySelectorAll<HTMLElement>('[data-action]')].find(el => el.dataset.action === action);
  else if (model) next = [...root.querySelectorAll<HTMLElement>('[data-model]')].find(el => el.dataset.model === model);
  else if (language) next = root.querySelector<HTMLButtonElement>(`[data-lang="${language}"]`);
  next?.focus({ preventScroll: true });
  if (selection && next instanceof HTMLInputElement) next.setSelectionRange(selection[0], selection[1]);
 }
 function discard() { generation++; if (ui) { ui.once = undefined; ui.qr = undefined; } ui = undefined; clearTimeout(copyTimer); }
 function closeDrawer() {
  const id = selected; discard(); selected = null; draw();
  ([...root.querySelectorAll<HTMLElement>('[data-key]')].find(el => el.dataset.key === id) || root.querySelector<HTMLElement>('[data-action="new"]'))?.focus({ preventScroll: true });
 }
 function openKey(id: string) {
  const key = data?.keys.find(k => k.id === id); if (!key) return;
  discard(); selected = id; ui = draft(key); draw(true);
 }
 const input = (event: Event) => {
  const el = event.target;
  if (!(el instanceof HTMLInputElement) || pending) return;
  if (el.dataset.setting && data) {
   const key=el.dataset.setting as keyof SettingsPatch, value=el.type==='checkbox'?el.checked:el.type==='number'&&el.value!==''?Number(el.value):el.value;
   if(value===settingValues(data.settings)[key])delete settingsUI.draft[key];else Object.assign(settingsUI.draft,{[key]:value});settingsUI.saved=false;settingsUI.error=undefined;draw();return;
  }
  if(!ui)return;
  if (el.id === 'invite-name') ui.name = el.value;
  else if (el.dataset.model !== undefined) {
   const models = new Set(ui.limits.models?.length ? ui.limits.models : data?.engine.models || []); if (el.checked) models.add(el.dataset.model); else models.delete(el.dataset.model);
   ui.limits.models = [...models];
  } else if (el.name in ui.limits && el.name !== 'models') {
   // Preserve incomplete number input across polling through DOM values below.
   Object.assign(ui.limits, { [el.name]: el.value === '' ? '' : Number(el.value) });
  } else return;
  ui.dirty = true;
 };
 async function copy(kind: 'link' | 'code') {
  const card = ui?.once, version = generation;
  if (!card) return;
  try {
   await navigator.clipboard.writeText(kind === 'link' ? card.link || card.invite : card.invite);
   if (version !== generation || !ui || stopped) return;
   ui.copied = kind; ui.error = undefined; draw(); clearTimeout(copyTimer);
   copyTimer = setTimeout(() => { if (version === generation && ui) { ui.copied = undefined; draw(); } }, 2000);
  } catch { if (version === generation && ui && !stopped) { ui.error = text(lang,'copy_failed'); draw(); } }
 }
 const click = (event: Event) => {
  const anchor=event.target instanceof Element?event.target.closest<HTMLAnchorElement>('a[href^="#"]'):null;
  if(remote&&anchor){event.preventDefault();root.querySelector(anchor.getAttribute('href')!)?.scrollIntoView();return;}
  const target = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-key],[data-close],[data-lang],[data-action]') : null;
  if (target instanceof HTMLButtonElement && target.disabled) return;
  if (target?.dataset.action==='leave'){remote?.leave();return;}
  if (target?.dataset.close) { closeDrawer(); return; }
  if (target?.dataset.lang) { lang = target.dataset.lang as Lang; draw(); return; }
  if (target?.dataset.key) { openKey(target.dataset.key); return; }
  const action = target?.dataset.action;
  if (action === 'copy-link' || action === 'copy-code') { void copy(action === 'copy-link' ? 'link' : 'code'); return; }
  if (action === 'done' && ui?.once) {
   if(ui.mode==='admin'){closeDrawer();return;}
   const id = ui.once.key_id; discard(); selected = id;
   const key = data?.keys.find(k => k.id === id); ui = key ? draft(key) : undefined; if (!key) selected = null;
   draw(true); return;
  }
  if (!authorized || pending) return;
  if(action==='remote-confirm'){settingsUI.confirm=true;draw();return;}
  if(action==='remote-keep'){settingsUI.confirm=false;draw();return;}
  if(action==='remote-off'){if(settingsUI.confirm)void saveSettings(true);return;}
  if(action==='remote-enable'||action==='remote-rotate'){discard();selected=null;ui=draft();ui.mode='admin';draw(true);void mutate(action);return;}
  if (action === 'new') { discard(); selected = null; ui = draft(); draw(true); return; }
  if (action === 'confirm' && ui) { ui.confirm = true; draw(); return; }
  if (action === 'keep' && ui) { ui.confirm = false; draw(); return; }
  if (action && ['pause','resume','rotate','revoke'].includes(action)) void mutate(action);
 };
 const submit = (event: Event) => {
  if (!(event.target instanceof HTMLFormElement))return;
  if(event.target.dataset.form==='settings'){event.preventDefault();if(event.target.reportValidity())void saveSettings();return;}
  if(!ui)return;
  event.preventDefault();
  const name = root.querySelector<HTMLInputElement>('#invite-name');
  if (name && (!name.reportValidity() || !name.value.trim())) { name.focus(); return; }
  if (event.target.reportValidity()) void mutate(ui.mode === 'mint' ? 'mint' : 'limits');
 };
 const keydown = (event: KeyboardEvent) => {
  if (ui && event.key === 'Escape') { event.preventDefault(); closeDrawer(); }
  else if (ui && event.key === 'Tab') {
   const controls = [...root.querySelectorAll<HTMLElement>('.drawer button:not(:disabled),.drawer input:not(:disabled)')];
   const index = controls.indexOf((root.getRootNode() instanceof ShadowRoot ? (root.getRootNode() as ShadowRoot).activeElement : document.activeElement) as HTMLElement);
   if (index < 0 || (!event.shiftKey && index === controls.length - 1) || (event.shiftKey && index === 0)) {
    event.preventDefault(); controls[event.shiftKey ? controls.length - 1 : 0]?.focus();
   }
  } else if ((event.key === 'Enter' || event.key === ' ') && (event.target as HTMLElement)?.dataset.key) { event.preventDefault(); click(event); }
 };
 async function readSnapshot(): Promise<boolean> {
  if(remote?.retryAt&&remote.retryAt>now())return false;
  const controller = new AbortController(), readRevision = revision; activeRequest = controller;
  const timeout = setTimeout(() => controller.abort(), 5000);
  try {
   const paths = ['status', 'keys', 'engine', 'settings', 'usage?window=today', 'usage?window=week'];
   const values = await Promise.all(paths.map(async path => {
    const response = await request('/api/' + path, { headers, signal: controller.signal, cache: 'no-store' });
    if (response.status === 401) { authorized = false; discard(); selected = null; }
    if (!response.ok) throw new Error('host did not answer');
    return response.json();
   }));
   if (stopped || readRevision !== revision || !authorized) return false;
   data = { status: values[0], keys: values[1], engine: values[2], settings: values[3], today: values[4], week: values[5] };
   const key = data.keys.find(k => k.id === selected);
   if (ui?.mode === 'key' && !ui.once && !ui.dirty && key) ui.limits = structuredClone(key.limits);
   if (selected && !key && !ui?.once) { discard(); selected = null; }
   if (ui?.message === 'refresh_write') ui.message = undefined;
   lastAnswer = now(); stale = null; return true;
  } catch { controller.abort(); if (!stopped) stale = Math.max(1, Math.floor((now() - lastAnswer) / 1000)); return false; }
  finally { clearTimeout(timeout); if (!stopped) draw(); }
 }
 function refresh(): Promise<boolean> {
  if (!authorized || stopped || pending || remote?.retryAt && remote.retryAt>now()) return Promise.resolve(false);
  return reading ||= readSnapshot().finally(() => { reading = undefined; });
 }
 async function mutate(action: string) {
  if (!ui || !authorized || pending || stopped || !data) return;
  const remoteAction=action.startsWith('remote-');
  if (!remoteAction && action !== 'mint' && (!selected || data.keys.find(k => k.id === selected)?.status === 'revoked')) return;
  if (action === 'revoke' && !ui.confirm) return;
  const version = generation, id = selected, current = ui;
  const body = action === 'mint' ? { name: ui.name.trim(), limits: ui.limits } : action === 'limits' ? ui.limits : undefined;
  pending = true; revision++; activeRequest?.abort(); ui.error = undefined; ui.message = undefined; draw();
  const controller = new AbortController(); mutation = controller;
  const timeout = setTimeout(() => controller.abort(), 10000);
  let committed = false;
  try {
   const path = remoteAction ? 'remote/'+action.slice(7) : action === 'mint' ? 'keys' : 'keys/' + encodeURIComponent(id!) + (action === 'limits' ? '' : '/' + action);
   const response = await request('/api/' + path, { method: action === 'limits' ? 'PATCH' : 'POST', headers: { ...headers, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, signal: controller.signal, cache: 'no-store' });
   if (response.status === 401) { authorized = false; discard(); selected = null; }
   if (!response.ok) {
    const error = await response.json().catch(() => ({}));
    if (version === generation && ui) { ui.error = errorLine(response,error); if (action === 'mint') ui.duplicateId = data?.keys.find(k => k.status !== 'revoked' && k.name === ui?.name.trim())?.id; }
   } else {
    committed = true;
    const value = await response.json();
    if (version === generation && ui && !stopped) {
     ui.confirm = false; ui.dirty = false;
     if (remoteAction || action === 'mint' || action === 'rotate') {
      const invite = value as Invite;
      if (typeof invite.invite !== 'string' || typeof invite.key_id !== 'string') throw new Error('invalid invite result');
      if(remote&&action==='remote-rotate'&&invite.invite.startsWith('ia1.'))headers.Authorization='Bearer '+invite.invite.split('.')[2];
      ui.once = invite; ui.rotated = action === 'rotate'||action==='remote-rotate'; selected = invite.key_id;
      try { ui.qr = encodeQR(invite.link || invite.invite); } catch { ui.qrFailed = true; }
     } else if (action === 'limits') ui.message = 'saved_limits';
    }
   }
  } catch { if (version === generation && ui && !stopped) ui.error = text(lang,'unknown_write'); }
  finally {
   clearTimeout(timeout); mutation = undefined;
   // Wait out any pre-write snapshot; only a GET started after the write may update rows.
   await reading;
   if (!stopped && authorized) {
    const fresh = await readSnapshot();
    if (version === generation && ui && !fresh && committed && !ui.error) ui.message = 'refresh_write';
    if (fresh && ui === current && !ui.once && !ui.dirty) {
     const key = data?.keys.find(k => k.id === selected); if (key) ui.limits = structuredClone(key.limits);
    }
   }
   pending = false; if (!stopped) draw();
  }
 }
 async function saveSettings(off=false) {
  if(pending||stopped||!authorized||!data?.settings.writes_supported)return;
  pending=true;revision++;activeRequest?.abort();settingsUI.error=undefined;settingsUI.saved=false;draw();
  const controller=new AbortController();mutation=controller;const timeout=setTimeout(()=>controller.abort(),10000);
  try {
   const response=await request('/api/'+(off?'remote/off':'settings'),{method:off?'POST':'PATCH',headers:{...headers,'Content-Type':'application/json'},body:off?undefined:JSON.stringify(remote?Object.fromEntries(Object.entries(settingsUI.draft).filter(([key])=>key!=='console')):settingsUI.draft),signal:controller.signal,cache:'no-store'});
   if(response.status===401){authorized=false;discard();selected=null;}
   const value=await response.json().catch(()=>({}));
   if(!response.ok){settingsUI.error=errorLine(response,value);}
   else {if(!off)settingsUI.draft={};settingsUI.confirm=false;settingsUI.saved=true;}
  } catch {settingsUI.error=text(lang,'unknown_write');}
  finally {clearTimeout(timeout);mutation=undefined;await reading;if(!stopped&&authorized)await readSnapshot();pending=false;if(!stopped)draw();}
 }
 root.addEventListener('click', click); root.addEventListener('keydown', keydown); root.addEventListener('input',input); root.addEventListener('submit',submit);
 draw(); void refresh();
 const timer = setInterval(() => {
  if (stale !== null) { stale = Math.max(1, Math.floor((now() - lastAnswer) / 1000)); draw(); }
  void refresh();
 }, 2000);
 return { refresh, stop() { stopped = true; discard(); activeRequest?.abort(); mutation?.abort(); clearInterval(timer); root.innerHTML = ''; root.removeEventListener('click', click); root.removeEventListener('keydown', keydown); root.removeEventListener('input',input); root.removeEventListener('submit',submit); } };
}
