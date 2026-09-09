// @vitest-environment jsdom
import { test, expect, vi, afterEach } from 'vitest';
import fixture from './test/fixture.json';
import { render } from './render';
import { createConsole } from './app';
import { copy } from './copy';
const now = Date.parse('2026-09-06T12:00:00Z');
const data = () => structuredClone(fixture);
function mount(snapshot = data(), lang = 'en', selected = null, stale = null) {
 document.body.innerHTML = render(snapshot, lang, selected, stale, now);
 return document.body;
}
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

test('four truths use live counters and today counts', () => {
 const root = mount();
 expect(root.querySelector('.facts').textContent).toContain('1 in flight · 0 waiting');
 expect(root.querySelector('.facts').textContent).toContain('312 model calls');
 expect(root.querySelector('.facts').textContent).toContain('41 tok/s');
});
test('friends join limits and live counters, fold revoked keys', () => {
 const root = mount();
 expect(root.querySelector('#friends > .sec-in > .tbl tbody').children.length).toBe(4);
 expect(root.querySelector('[data-key="k_7f3a2b"]').textContent).toContain('7/20 this minute');
 expect(root.querySelector('[data-key="k_2c9e10"]').textContent).toContain('60 rpm');
 expect(root.querySelector('details').open).toBe(false);
 expect(root.querySelector('summary').textContent).toContain('2 revoked keys');
});
test('engine shows metrics, per-model calls, key allowlists, and unknown model contexts', () => {
 const root = mount();
 expect(root.querySelectorAll('.facts8 .fact')).toHaveLength(8);
 expect(root.querySelector('.models tbody').textContent).toContain('298');
 expect(root.querySelector('.models tbody').textContent).toContain('all 4 keys');
 expect(root.querySelector('.models tbody').textContent).toContain('not reported');
});
test('usage renders exact aggregate percentiles and seven UTC meters without inventing downtime', () => {
 const root = mount();
 expect(root.querySelectorAll('.days .day')).toHaveLength(7);
 expect(root.querySelector('#usage').textContent).toContain('265 ms · 1.2 s');
 expect(root.querySelector('#usage').textContent).toContain('no recorded calls');
 expect(root.textContent).not.toContain('host was off');
});
test('settings distinguish configured/effective URL and report prompt logging truth', () => {
 const d = data(); d.settings.configured_web_url = ''; d.settings.log_prompts = true;
 const root = mount(d);
 expect(root.querySelector('#settings .truth').textContent).toContain('Prompt logging is on');
 expect([...root.querySelectorAll('#settings input')].every(el => el.disabled)).toBe(true);
 expect(root.querySelector('#settings').textContent).toContain('https://infercat.ai');
 expect(root.querySelector('#settings').textContent).toContain('settings apply on restart');
});
test('drawer includes live TPM, seven limits, per-key exact usage and inert actions', () => {
 const root = mount(data(), 'en', 'k_7f3a2b');
 expect(root.querySelector('[role="dialog"]').textContent).toContain('3.1k/20k tpm');
 expect(root.querySelectorAll('.lims input:not([type="checkbox"])')).toHaveLength(6);
 expect(root.querySelector('.lims .models')).not.toBeNull();
 expect(root.querySelector('.u2').textContent).toContain('262 ms');
 expect(root.querySelectorAll('.u2 tbody tr')).toHaveLength(5);
 expect(root.querySelector('.u2').textContent).toContain('38k → 3.1k');
 expect([...root.querySelectorAll('.drawer button:not([data-close])')].every(el => el.disabled)).toBe(true);
 expect(root.querySelector('#page').hasAttribute('inert')).toBe(true);
});
test('complete bilingual copy and ZH section/drawer rendering', () => {
 for (const pair of Object.values(copy)) expect(pair.length === 2 && pair.every(s => typeof s === 'string' && s.length > 0)).toBe(true);
 const root = mount(data(),'zh','k_7f3a2b');
 for (const text of ['朋友','用量','设置','每分钟 token 数','只读']) expect(root.textContent).toContain(text);
 expect([...root.querySelectorAll('[data-t]')].every(el => copy[el.dataset.t]?.[1])).toBe(true);
});
test('empty, unknown engine, failed probe and missing auth states stay truthful', () => {
 const d = data(); d.keys=[]; d.engine.kind='unknown'; d.engine.health.ok=false; d.engine.health.err='connection refused'; d.engine.models=null;
 d.week.total.requests=0;
 const root=mount(d);
 expect(root.textContent).toContain('No keys yet'); expect(root.textContent).toContain('not identified yet');
 expect(root.textContent).toContain('connection refused'); expect(root.textContent).toContain('nothing yet');
 document.body.innerHTML=render(null,'en',null,null,now,false);
 expect(document.body.textContent).toContain('infercat console');
});
test('untrusted names, model IDs, paths and errors are text, never executable markup', () => {
 const d=data(); d.keys[0].name='<img src=x onerror=alert(1)>'; d.engine.models=['<script>bad()</script>']; d.settings.data_dir='" onmouseover="bad()';
 const root=mount(d,'en',d.keys[0].id);
 expect(root.querySelector('img,script,[onmouseover],[onerror]')).toBeNull();
 expect(root.textContent).toContain('<img src=x onerror=alert(1)>');
});

test('polling is GET-only, stale data remains grey, recovery refreshes counters, keyboard focus returns', async () => {
 vi.useFakeTimers(); history.replaceState(null,'','/');
 document.body.innerHTML='<div id="app"></div>';
 let offline=false; const d=data();
 const request=vi.fn(async url => {
  if(offline) throw new Error('offline');
  const key=url.slice(5); const value=key==='usage?window=today'?d.today:key==='usage?window=week'?d.week:d[key];
  return new Response(JSON.stringify(value),{status:200});
 });
 const app=createConsole(document.querySelector('#app'),'secret',request,()=>now+Date.now());
 await vi.advanceTimersByTimeAsync(0);
 const row=document.querySelector('[data-key="k_7f3a2b"]'); row.focus(); row.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));
 expect(document.activeElement.dataset.close).toBe('true');
 document.activeElement.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));
 expect(document.activeElement.dataset.key).toBe('k_7f3a2b');
 offline=true; await vi.advanceTimersByTimeAsync(2000);
 expect(document.querySelector('.stale')).not.toBeNull(); expect(document.body.textContent).toContain("Max's laptop");
 offline=false; d.status.keys[0].in_flight=2; await vi.advanceTimersByTimeAsync(2000);
 expect(document.querySelector('.stale')).toBeNull(); expect(document.querySelector('[data-key="k_7f3a2b"]').textContent).toContain('2 in flight');
 for(const [url,init] of request.mock.calls) { expect(url).not.toContain('secret'); expect(init.method || 'GET').toBe('GET'); expect(init.headers.Authorization).toBe('Bearer secret'); }
 expect(localStorage.length).toBe(0); expect(sessionStorage.length).toBe(0); app.stop();
 const count=request.mock.calls.length; await vi.advanceTimersByTimeAsync(6000); expect(request.mock.calls).toHaveLength(count);
});

test('a replaced admin token asks for a fresh console URL instead of retrying unauthorized reads', async () => {
 vi.useFakeTimers();document.body.innerHTML='<div id="app"></div>';
 const request=vi.fn(async()=>new Response('{}',{status:401}));
 const app=createConsole(document.querySelector('#app'),'expired',request);
 await vi.advanceTimersByTimeAsync(0);
 expect(document.body.textContent).toContain('Open this page with infercat console');
 const count=request.mock.calls.length;await vi.advanceTimersByTimeAsync(6000);
 expect(request.mock.calls.length).toBe(count);app.stop();
});

test('refresh label reports the age of the last successful snapshot', () => {
 document.body.innerHTML=render(data(),'en',null,null,now,true,7);
 expect(document.body.textContent).toContain('live · refreshed 7 s ago');
});

test('host model pin is visible and constrains the model open-to count', () => {
 const d=data();d.status.models_pinned=[d.engine.models[1]];
 const root=mount(d), rows=root.querySelectorAll('.models tbody tr');
 expect(rows[1].textContent).toContain('pinned');
 expect(rows[0].textContent).not.toContain('pinned');
 expect(rows[0].textContent).toContain('0 of 4 keys');
 expect(rows[1].textContent).toContain('3 of 4 keys');
});
