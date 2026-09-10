// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest';
import fixture from './test/fixture.json';
import {bridgeAudioFixture} from './test/bridge-audio-fixture';
import {createConsole} from './app';
let app;
afterEach(()=>{app?.stop();vi.useRealTimers();});
async function mount(data,lang='en',remote=false){
 vi.useFakeTimers();history.replaceState(null,'',`/?lang=${lang}`);document.body.innerHTML='<div id="app"></div>';
 const request=vi.fn(async(url,init)=>{expect(init.method).toBeUndefined();const path=url.slice(5);return new Response(JSON.stringify(path==='usage?window=today'?data.today:path==='usage?window=week'?data.week:data[path]));});
 app=createConsole(document.querySelector('#app'),'test',request,Date.now,undefined,remote?{path:'tunnel',leave(){}}:undefined);await vi.advanceTimersByTimeAsync(0);return request;
}
const rows=selector=>[...document.querySelectorAll(selector+' tbody tr')].map(r=>r.textContent);
for(const remote of [false,true])for(const state of ['absent','on','reconnecting','error','off'])test(`public URL ${state}, remote=${remote}`,async()=>{
 const d=bridgeAudioFixture(fixture,state),request=await mount(d,'en',remote),fact=document.querySelector('.overview .wide');
 expect(request).toHaveBeenCalledTimes(6);
 if(state==='absent'){expect(fact).toBeNull();expect(document.querySelectorAll('.overview .fact')).toHaveLength(4);return;}
 expect(fact.querySelector('.v').textContent).toBe('gateway.infercat.ai/h/maxws/v1');expect(fact.querySelector('a,button,input')).toBeNull();expect(fact.querySelector('.s').textContent).toContain('14 requests today');
 expect(fact.querySelector('.s').textContent).toContain(state==='off'?'off':state==='on'?'on · connected since':'on · not connected');
 if(state==='error'){expect(fact.querySelector('.bad').textContent).toBe('on · not connected · closed 1006');expect(fact.querySelector('.bad').textContent).not.toContain('requests');}else expect(fact.querySelector('.bad')).toBeNull();
});
test('public address and host diagnostic stay text',async()=>{
 const d=bridgeAudioFixture(fixture,'error');d.status.bridge.url='https://example/<img src=x>';d.status.bridge.last_error='<svg onload=bad()>closed';await mount(d);expect(document.querySelector('.wide img,.wide svg')).toBeNull();expect(document.querySelector('.wide').textContent).toContain('<svg onload=bad()>');
});
for(const lang of ['en','zh'])test('via rows use report split; missing buckets are zero '+lang,async()=>{
 const d=bridgeAudioFixture(fixture);delete d.today.by_via;delete d.week.by_via.direct;await mount(d,lang);
 const via=[...document.querySelectorAll('.utbl .via-row')];expect(via).toHaveLength(2);expect(via[0].classList.contains('via-first')).toBe(true);expect(via[0].textContent).toContain('via direct');expect(via[1].textContent).toContain('via bridge');expect(via[0].textContent).toContain(lang==='en'?'0 calls · 0 tokens':'0 次调用 · 0 token');expect(via[1].textContent).toContain(lang==='en'?'41 calls · 33k tokens':'41 次调用 · 33k token');
});
for(const absent of [false,true])test('no via rows without weekly public requests '+absent,async()=>{
 const d=bridgeAudioFixture(fixture);if(absent)delete d.week.by_via;else d.week.by_via.bridge.requests=0;await mount(d);expect(document.querySelector('.via-row')).toBeNull();
});
test('audio comes from the selected key; weekly visibility and today units are independent',async()=>{
 const d=bridgeAudioFixture(fixture);await mount(d);expect(rows('.utbl').some(r=>r.includes('Audio seconds2521,860'))).toBe(true);
 document.querySelector(`[data-key="${d.keys[0].id}"]`).click();expect(document.querySelector('.drawer .nowline').textContent).toContain('12 s · 340 chars · Last model call');expect(rows('.u2').some(r=>r.includes('Audio seconds1296'))).toBe(true);expect(document.querySelectorAll('.u2 .via-row')).toHaveLength(0);
 const a=d.today.keys[0],b=d.week.keys[0];a.seconds=0;delete a.characters;b.seconds=0;await app.refresh();expect(rows('.u2').some(r=>r.startsWith('Audio seconds'))).toBe(false);expect(rows('.u2').some(r=>r.startsWith('Speech characters'))).toBe(true);expect(document.querySelector('.drawer .nowline').textContent).not.toContain('chars');
 a.seconds=12.5;await app.refresh();expect(document.querySelector('.drawer .nowline').textContent).toContain('12.5 s · Last model call');
});
test('absent audio fields keep tables and Now text unchanged',async()=>{
 const d=bridgeAudioFixture(fixture);for(const range of [d.today,d.week])for(const v of [range.total,...range.keys]){delete v.seconds;delete v.characters;}await mount(d);document.querySelector(`[data-key="${d.keys[0].id}"]`).click();expect(rows('.utbl').concat(rows('.u2')).some(r=>r.startsWith('Audio seconds')||r.startsWith('Speech characters'))).toBe(false);expect(document.querySelector('.drawer .nowline').textContent).not.toContain('chars');
});
