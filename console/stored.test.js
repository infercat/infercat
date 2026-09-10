// @vitest-environment jsdom
import { afterEach, expect, test, vi } from 'vitest';
import fixture from './test/fixture.json';
import { createConsole } from './app';
import { storedSection } from './stored';
const stored = { cursor:'e_fixture:12',reserved:2048,retry_cleanup:1,key_id:fixture.keys[0].id,total:12,terminal:9,bytes:1048576,budget:67108864,images:3,image_bytes:41943040,image_budget:268435456,clear_images:2,kinds:{agent:{stored:9,live:2},image:{stored:3,live:1}},expires_first:'2026-09-08T12:00:00Z',expires_last:'2026-09-13T12:00:00Z',pending_cleanup:1,runs:[{id:'r_safe',kind:'image',state:'done',bytes:1024,expires:'2026-09-08T12:00:00Z',image:{bytes:41943040,expires:'2026-09-08T12:00:00Z'}}],truncated:true };
let app;afterEach(()=>{app?.stop();vi.useRealTimers();});
for(const lang of ['en','zh'])test('stored fixture shows budgets, expiry, cleanup and exact clear counts '+lang,()=>{
 document.body.innerHTML=storedSection({value:stored,confirm:{cursor:stored.cursor,terminal:9,images:2,cleanup:1}},lang,false,Date.parse('2026-09-06T12:00:00Z'));
 expect(document.body.textContent).toContain('41 MiB');expect(document.body.textContent).toContain('64 MiB');expect(document.body.textContent).toContain('256 MiB');
 expect(document.body.textContent).toContain(lang==='en'?'9 finished runs':'9 次已结束运行');expect(document.body.textContent).toContain(lang==='en'?'including up to 2 images':'含最多 2 张图片');
 const unsafe=structuredClone(stored);unsafe.runs[0].kind='<img src=x>';document.body.innerHTML=storedSection({value:unsafe},lang,false,Date.now());expect(document.querySelector('img')).toBeNull();
});
test('selected-key reads and clear use auth, one DELETE, and authoritative refresh even on ambiguous failure',async()=>{
 vi.useFakeTimers();document.body.innerHTML='<div id="app"></div>';let value=structuredClone(stored), writes=0;
 const request=vi.fn(async(url,init)=>{
  expect(init.headers.Authorization).toBe('Bearer admin-test');
  if(url.startsWith('/api/stored')) {expect(url).toBe('/api/stored?key_id='+fixture.keys[0].id);if(init.method==='DELETE'){expect(JSON.parse(init.body)).toEqual({cursor:'e_fixture:12',terminal:9,images:2,cleanup:1});writes++;value={...value,total:3,terminal:0,images:1,clear_images:0,pending_cleanup:0,retry_cleanup:0};throw new Error('lost after commit');}return new Response(JSON.stringify(value));}
  const path=url.slice(5);return new Response(JSON.stringify(path==='usage?window=today'?fixture.today:path==='usage?window=week'?fixture.week:fixture[path]));
 });
 app=createConsole(document.querySelector('#app'),'admin-test',request);await vi.advanceTimersByTimeAsync(0);expect(request).toHaveBeenCalledTimes(6);
 document.querySelector(`[data-key="${fixture.keys[0].id}"]`).click();await vi.advanceTimersByTimeAsync(0);
 document.querySelector('[data-action="stored-confirm"]').click();expect(writes).toBe(0);
 document.querySelector('[data-action="stored-clear"]').click();document.querySelector('[data-action="stored-clear"]').click();await vi.advanceTimersByTimeAsync(0);
 expect(writes).toBe(1);expect(document.querySelector('#stored-details').textContent).toContain('3 runs');expect(document.body.textContent).toContain('Nothing was sent again');
 expect(document.querySelector('[data-action="stored-confirm"]').disabled).toBe(true);
});

test('small retained data is never rounded down to a false zero MiB',()=>{
 document.body.innerHTML=storedSection({value:{...stored,bytes:1515,image_bytes:0,images:0}},'en',false,Date.now());
 expect(document.querySelector('summary').textContent).toContain('1.5 KiB kept');
});

for(const status of [409,429,503])test('clear HTTP '+status+' is not described as an ambiguous network outcome',async()=>{
 vi.useFakeTimers();document.body.innerHTML='<div id="app"></div>';
 const request=vi.fn(async(url,init)=>{
  if(url.startsWith('/api/stored'))return init.method==='DELETE'?new Response('Stored data changed; refresh before confirming.',{status,headers:{'Content-Type':'text/plain'}}):new Response(JSON.stringify(stored));
  const p=url.slice(5);return new Response(JSON.stringify(p==='usage?window=today'?fixture.today:p==='usage?window=week'?fixture.week:fixture[p]));
 });
 app=createConsole(document.querySelector('#app'),'test',request);await vi.advanceTimersByTimeAsync(0);
 document.querySelector(`[data-key="${fixture.keys[0].id}"]`).click();await vi.advanceTimersByTimeAsync(0);
 document.querySelector('[data-action="stored-confirm"]').click();document.querySelector('[data-action="stored-clear"]').click();await vi.advanceTimersByTimeAsync(0);
 expect(document.body.textContent).toContain('Stored data changed; refresh before confirming.');expect(document.body.textContent).not.toContain('No answer from the host');
 expect(request.mock.calls.filter(([,i])=>i.method==='DELETE')).toHaveLength(1);
});
test('stored read waits for dashboard reads and drawer polling uses four seconds',async()=>{
 vi.useFakeTimers();document.body.innerHTML='<div id="app"></div>';let active=0,maximum=0,reads=0;
 const request=async(url)=>{active++;maximum=Math.max(maximum,active);await new Promise(r=>setTimeout(r,20));active--;
  if(url.startsWith('/api/stored')){reads++;return new Response(JSON.stringify(stored));}
  const p=url.slice(5);return new Response(JSON.stringify(p==='usage?window=today'?fixture.today:p==='usage?window=week'?fixture.week:fixture[p]));};
 app=createConsole(document.querySelector('#app'),'test',request);await vi.advanceTimersByTimeAsync(30);
 const refresh=app.refresh();document.querySelector(`[data-key="${fixture.keys[0].id}"]`).click();await vi.advanceTimersByTimeAsync(60);await refresh;
 expect(maximum).toBe(6);const initial=reads;await vi.advanceTimersByTimeAsync(8100);expect(reads-initial).toBe(2);
});
test('live rows never present the live-age deadline as stored-data expiry',()=>{
 const s={...stored,expires_first:undefined,expires_last:undefined,runs:[{id:'r_live',kind:'agent',state:'running',bytes:1,expires:'2099-01-01T00:00:00Z'}]};
 document.body.innerHTML=storedSection({value:s},'en',false,Date.now());expect(document.querySelector('table').textContent).not.toContain('2099');
});

test('polling cannot silently replace the counts and cursor the host confirmed',async()=>{
 vi.useFakeTimers();document.body.innerHTML='<div id="app"></div>';let value=structuredClone(stored),sent;
 const request=async(url,init)=>{
  if(url.startsWith('/api/stored')){if(init.method==='DELETE'){sent=JSON.parse(init.body);return new Response('{}',{status:409});}return new Response(JSON.stringify(value));}
  const p=url.slice(5);return new Response(JSON.stringify(p==='usage?window=today'?fixture.today:p==='usage?window=week'?fixture.week:fixture[p]));
 };
 app=createConsole(document.querySelector('#app'),'test',request);await vi.advanceTimersByTimeAsync(0);
 document.querySelector(`[data-key="${fixture.keys[0].id}"]`).click();await vi.advanceTimersByTimeAsync(0);document.querySelector('[data-action="stored-confirm"]').click();
 value={...value,cursor:'e_fixture:13',terminal:10,clear_images:3};await vi.advanceTimersByTimeAsync(4100);
 expect(document.querySelector('.confirm').textContent).toContain('9 finished runs');
 document.querySelector('[data-action="stored-clear"]').click();await vi.advanceTimersByTimeAsync(0);
 expect(sent).toEqual({cursor:'e_fixture:12',terminal:9,images:2,cleanup:1});expect(document.body.textContent).toContain('HTTP 409');
});

test('committed clear warning and actual skipped count survive a failed refresh',async()=>{
 vi.useFakeTimers();document.body.innerHTML='<div id="app"></div>';let cleared=false;
 const request=async(url,init)=>{
  if(url.startsWith('/api/stored')){if(init.method==='DELETE'){cleared=true;return new Response(JSON.stringify({cleared:2,skipped:1,warning:'Stored data cleared; cleanup step failed: permission denied'}));}if(cleared)throw new Error('read failed');return new Response(JSON.stringify(stored));}
  const p=url.slice(5);return new Response(JSON.stringify(p==='usage?window=today'?fixture.today:p==='usage?window=week'?fixture.week:fixture[p]));
 };
 app=createConsole(document.querySelector('#app'),'test',request);await vi.advanceTimersByTimeAsync(0);document.querySelector(`[data-key="${fixture.keys[0].id}"]`).click();await vi.advanceTimersByTimeAsync(0);
 document.querySelector('[data-action="stored-confirm"]').click();document.querySelector('[data-action="stored-clear"]').click();await vi.advanceTimersByTimeAsync(0);
 expect(document.body.textContent).toContain('Cleared 2 finished runs; 1 with outstanding work kept.');expect(document.body.textContent).toContain('Stored data cleared; cleanup step failed: permission denied');expect(document.body.textContent).not.toContain('No answer from the host');
});

test('invite drawer keeps two-second polling and does not fetch Stored',async()=>{
 vi.useFakeTimers();document.body.innerHTML='<div id="app"></div>';
 const request=vi.fn(async(url)=>{expect(url).not.toContain('/stored');const p=url.slice(5);return new Response(JSON.stringify(p==='usage?window=today'?fixture.today:p==='usage?window=week'?fixture.week:fixture[p]));});
 app=createConsole(document.querySelector('#app'),'test',request);await vi.advanceTimersByTimeAsync(0);document.querySelector('[data-action="new"]').click();const before=request.mock.calls.length;await vi.advanceTimersByTimeAsync(8100);expect(request.mock.calls.length-before).toBe(24);
});

for(const outcome of ['refusal','timeout','unauthorized'])test('Stored GET handles '+outcome+' without raw JS errors',async()=>{
 vi.useFakeTimers();document.body.innerHTML='<div id="app"></div>';
 const request=async(url,init)=>{
  if(url.startsWith('/api/stored')){
   if(outcome==='timeout')return new Promise((_,reject)=>init.signal.addEventListener('abort',()=>reject(new DOMException('raw abort','AbortError'))));
   return new Response('run store unavailable',{status:outcome==='unauthorized'?401:503,headers:{'Content-Type':'text/plain'}});
  }
  const p=url.slice(5);return new Response(JSON.stringify(p==='usage?window=today'?fixture.today:p==='usage?window=week'?fixture.week:fixture[p]));
 };
 app=createConsole(document.querySelector('#app'),'test',request);await vi.advanceTimersByTimeAsync(0);document.querySelector(`[data-key="${fixture.keys[0].id}"]`).click();await vi.advanceTimersByTimeAsync(outcome==='timeout'?5100:0);
 expect(document.body.textContent).not.toMatch(/Error:|HTTP 503|raw abort/);
 if(outcome==='refusal')expect(document.body.textContent).toContain('run store unavailable');
 if(outcome==='timeout')expect(document.body.textContent).toContain('The host took too long to answer. Try refreshing.');
 if(outcome==='unauthorized')expect(document.querySelector('[data-action="stored-confirm"]')).toBeNull();
});
test('cleanup-only confirmation omits zero clauses and uses singular file',()=>{
 document.body.innerHTML=storedSection({value:stored,confirm:{cursor:stored.cursor,terminal:0,images:0,cleanup:1}},'en',false,Date.now());
 const line=document.querySelector('.confirm').textContent;expect(line).toContain('Retry cleanup of up to 1 file.');expect(line).not.toContain('up to 0');expect(line).not.toContain('1 files');
});
