// @vitest-environment jsdom
import { test, expect, vi, afterEach } from 'vitest';
import fixture from './test/fixture.json';
import { createConsole } from './app';
import { inviteQR } from './qr';
import jsQR from 'jsqr';
let app;
afterEach(() => { app?.stop(); vi.useRealTimers(); vi.unstubAllGlobals(); });
const tick = () => vi.advanceTimersByTimeAsync(0);
const click = action => document.querySelector(`[data-action="${action}"]`).click();
function edit(selector,value) { const el=document.querySelector(selector); el.value=value; el.dispatchEvent(new Event('input',{bubbles:true})); }
function submit() { document.querySelector('form[data-form="limits"]').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true})); }
async function setup(outcome='success', qr=inviteQR) {
 vi.useFakeTimers(); history.replaceState(null,'','/'); document.body.innerHTML='<div id="app"></div>';
 const data=structuredClone(fixture), writes=[];
 let finish, readsFail=false;
 const response={key_id:'k_new',name:'erin',invite:'ic1.host.secret',link:'https://infercat.ai/#ic1.host.secret'};
 const request=vi.fn(async (url,init) => {
  const path=url.slice(5);
  if(init.method) {
   writes.push({path,...init});
   if(outcome==='network') throw new Error('offline');
   if(outcome==='refusal') return new Response(JSON.stringify({error:'refused <img src=x onerror=bad()>'}),{status:400});
   if(outcome==='pending') await new Promise(resolve=>{finish=resolve;});
   const [,id,verb]=path.split('/');
   const k=data.keys.find(k=>k.id===id);
   if(path==='keys') { const body=JSON.parse(init.body); response.name=body.name; data.keys.push({...data.keys[0],id:'k_new',name:body.name,limits:body.limits,last_seen:''}); }
   else if(verb==='pause') k.status='paused';
   else if(verb==='resume') k.status='active';
   else if(verb==='revoke') k.status='revoked';
   else if(verb==='rotate') { response.key_id=id;response.name=k.name; }
   else if(init.method==='PATCH') k.limits=JSON.parse(init.body);
   return new Response(JSON.stringify(path==='keys'||verb==='rotate'?response:{ok:true}));
  }
  if(readsFail) throw new Error('offline');
  const value=path==='usage?window=today'?data.today:path==='usage?window=week'?data.week:data[path];
  return new Response(JSON.stringify(value));
 });
 app=createConsole(document.querySelector('#app'),'admin-secret',request,Date.now,qr); await tick();
 return {data,writes,request,finish:()=>finish(),failReads:()=>{readsFail=true;},response};
}
function open() { document.querySelector('[data-key="k_7f3a2b"]').click(); }
async function act(action) {
 if(action==='mint') { click('new'); edit('#invite-name','erin');submit(); }
 else { open(); if(action==='limits') { edit('#limit-rpm','77');submit(); } else { if(action==='revoke') click('confirm');click(action); } }
 await tick();
}
for(const action of ['mint','rotate','pause','resume','revoke','limits']) for(const outcome of ['success','refusal','network']) {
 test(`${action}: ${outcome}, authoritative result and no automatic replay`,async()=>{
  const s=await setup(outcome); if(action==='resume') {s.data.keys[0].status='paused';await app.refresh();}
  const before=structuredClone(s.data.keys);await act(action);
  expect(s.writes).toHaveLength(1);
  expect(s.writes[0].headers.Authorization).toBe('Bearer admin-secret');
  if(outcome==='success') {
   if(action==='mint'||action==='rotate') expect(document.querySelector('.once .code').textContent).toBe(s.response.invite);
   else if(action==='limits') expect(document.querySelector('[data-key="k_7f3a2b"]').textContent).toContain('77 rpm');
   else expect(document.querySelector('.drawer .meta').textContent).toContain(action==='pause'?'paused':action==='resume'?'active':'revoked');
   const lastWrite=s.request.mock.calls.findIndex(([,init])=>init.method);
   expect(s.request.mock.calls.slice(lastWrite+1).some(([url,init])=>url==='/api/keys'&&!init.method)).toBe(true);
  } else {
   expect(s.data.keys).toEqual(before);expect(document.querySelector('.once')).toBeNull();
   expect(document.querySelector('.action-message').textContent).toContain(outcome==='refusal'?'refused <img':'may have completed');
   expect(document.querySelector('img,[onerror]')).toBeNull();
  }
  await vi.advanceTimersByTimeAsync(6000);expect(s.writes).toHaveLength(1);
  expect(localStorage.length).toBe(0);expect(sessionStorage.length).toBe(0);
 });
}
test('revoke requires inline confirmation; Keep sends nothing',async()=>{
 const s=await setup();open();click('confirm');expect(document.querySelector('.confirm').textContent).toContain('Revoking is permanent — alice would need a new invite.');
 expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1);click('keep');expect(document.querySelector('.confirm')).toBeNull();expect(s.writes).toHaveLength(0);
});
test('mint requires name; defaults and model selection survive live refresh and focus',async()=>{
 const s=await setup();click('new');submit();await tick();expect(s.writes).toHaveLength(0);
 edit('#invite-name','erin');edit('#limit-rpm','31');
 const model=document.querySelector('[data-model]');model.checked=false;model.dispatchEvent(new Event('input',{bubbles:true}));
 document.querySelector('#invite-name').focus();document.querySelector('#invite-name').setSelectionRange(1,3);
 await app.refresh();expect(document.activeElement.id).toBe('invite-name');expect(document.activeElement.selectionStart).toBe(1);
 expect(document.querySelector('#limit-rpm').value).toBe('31');expect(document.querySelector('[data-model]').checked).toBe(false);
 submit();await tick();const body=JSON.parse(s.writes[0].body);expect(body.name).toBe('erin');expect(body.limits.rpm).toBe(31);expect(body.limits.daily_tokens).toBe(200000);expect(body.limits).not.toHaveProperty('daily_audio_seconds');
});
test('optional audio fields preserve zero and are omitted on older hosts',async()=>{
 const s=await setup();s.data.keys[0].limits.daily_audio_seconds=0;s.data.keys[0].limits.daily_speech_chars=200000;await app.refresh();open();
 expect(document.querySelector('#limit-daily_audio_seconds').value).toBe('0');edit('#limit-daily_speech_chars','900');submit();await tick();
 expect(JSON.parse(s.writes[0].body)).toMatchObject({daily_audio_seconds:0,daily_speech_chars:900});
});
test('closing a pending mint discards its late secret and prevents duplicate submit',async()=>{
 const s=await setup('pending');await act('mint');submit();expect(s.writes).toHaveLength(1);document.querySelector('button[data-close]').click();s.finish();await tick();
 expect(document.querySelector('.once')).toBeNull();expect(document.body.textContent).not.toContain(s.response.invite);expect(s.writes).toHaveLength(1);
 document.querySelector('[data-key="k_new"]').click();expect(document.querySelector('.once')).toBeNull();
});
test('a once-card disappears on Done, close and a new app instance',async()=>{
 const s=await setup();await act('mint');click('done');expect(document.querySelector('.once')).toBeNull();expect(document.querySelector('.drawer h3').textContent).toBe('erin');
 click('rotate');await tick();expect(document.querySelector('.once')).not.toBeNull();document.querySelector('button[data-close]').click();
 document.querySelector('[data-key="k_new"]').click();expect(document.querySelector('.once')).toBeNull();
 app.stop();app=createConsole(document.querySelector('#app'),'admin-secret',s.request);await tick();expect(document.body.textContent).not.toContain(s.response.invite);
});
test('QR failure preserves committed code and link; read failure also preserves once-card',async()=>{
 const s=await setup('pending',()=>{throw new Error('too long');});await act('mint');s.failReads();s.finish();await tick();
 expect(document.querySelector('.once .code').textContent).toBe(s.response.invite);expect(document.querySelector('.once .link').textContent).toBe(s.response.link);
 expect(document.querySelector('.qrcap').textContent).toContain('QR unavailable');
});
test('copy uses the complete secret and clears Copied after two seconds',async()=>{
 const writeText=vi.fn(async()=>{});Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText}});
 const s=await setup();await act('mint');click('copy-link');await tick();expect(writeText).toHaveBeenLastCalledWith(s.response.link);expect(document.querySelector('[data-action="copy-link"]').textContent).toBe('Copied');
 await vi.advanceTimersByTimeAsync(2000);expect(document.querySelector('[data-action="copy-link"]').textContent).toBe('Copy link');
 writeText.mockRejectedValueOnce(new Error('denied'));click('copy-code');await tick();expect(document.querySelector('.action-message').textContent).toContain('Could not copy');
});
for(const content of ['ic1.host.secret','https://infercat.ai/#ic1.'+'a'.repeat(43)+'.'+'b'.repeat(43),'https://example.com/中文#ic1.host.secret']) {
 test(`QR decodes original bytes with a four-module quiet zone (${content.length} chars)`,()=>{
  document.body.innerHTML=inviteQR(content);const svg=document.querySelector('svg'),size=Number(svg.getAttribute('viewBox').split(' ')[2]),scale=4,width=size*scale;
  const pixels=new Uint8ClampedArray(width*width*4).fill(255);
  for(const rect of svg.querySelectorAll('g rect')) {
   const x=Number(rect.getAttribute('x')),y=Number(rect.getAttribute('y'));
   expect(Math.min(x,y)).toBeGreaterThanOrEqual(4);expect(Math.max(x,y)).toBeLessThan(size-4);
   for(let dy=0;dy<scale;dy++)for(let dx=0;dx<scale;dx++){const at=((y*scale+dy)*width+x*scale+dx)*4;pixels[at]=pixels[at+1]=pixels[at+2]=0;}
  }
  expect(jsQR(pixels,width,width)?.data).toBe(content);
 });
}
test('a host without web URL renders code-only card and encodes the invite',async()=>{
 const encode=vi.fn(inviteQR),s=await setup('pending',encode);await act('mint');delete s.response.link;s.finish();await tick();
 expect(encode).toHaveBeenCalledWith(s.response.invite);expect(document.querySelector('[data-action="copy-link"]')).toBeNull();expect(document.querySelector('.once').textContent).toContain('No web app URL is set');
});
test('keyboard focus stays inside editable drawer and returns on Escape',async()=>{
 await setup();open();const close=document.querySelector('button[data-close]');close.focus();close.dispatchEvent(new KeyboardEvent('keydown',{key:'Tab',shiftKey:true,bubbles:true,cancelable:true}));
 expect(document.activeElement.dataset.action).toBe('confirm');document.activeElement.dispatchEvent(new KeyboardEvent('keydown',{key:'Tab',bubbles:true,cancelable:true}));expect(document.activeElement.dataset.close).toBe('true');
 document.activeElement.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));expect(document.activeElement.dataset.key).toBe('k_7f3a2b');
});
