// @vitest-environment jsdom
import {afterEach,expect,test,vi} from 'vitest';
import fixture from './test/fixture.json';
import extension from './test/bridge-audio.json';
import {emptyStats} from './types';
import {createConsole} from './app';
import {render} from './render';
vi.mock('./render',async original=>({...await original(),render:vi.fn(()=>'<div></div>')}));
let app;
afterEach(()=>{app?.stop();vi.useRealTimers();vi.clearAllMocks();});
for(const legacy of [false,true])test('polls preserve optional bridge, via and audio data '+(legacy?'legacy':'current'),async()=>{
 vi.useFakeTimers();document.body.innerHTML='<div id="app"></div>';
 const data=structuredClone(fixture);
 if(!legacy){
  data.status.bridge=extension.bridge;Object.assign(data.keys[0].limits,extension.limits);
  const byVia=Object.fromEntries(Object.entries(extension.by_via).map(([name,value])=>[name,{...emptyStats,...value}]));
  const total={...emptyStats,...extension.total};
  for(const range of ['today','week'])data[range]={total,by_via:byVia,keys:[{...total,key_id:data.keys[0].id,by_via:byVia}],malformed_lines:0,daily:[{date:'2026-09-10',total,by_via:byVia,keys:[]}]};
 }
 const request=vi.fn(async url=>{const path=url.slice(5),value=path==='usage?window=today'?data.today:path==='usage?window=week'?data.week:data[path];return new Response(JSON.stringify(value));});
 app=createConsole(document.querySelector('#app'),'local-token',request);await vi.advanceTimersByTimeAsync(0);
 const snapshot=render.mock.calls.at(-1)[0];expect(snapshot).toEqual(data);expect(request).toHaveBeenCalledTimes(6);
 expect(request.mock.calls.every(([,init])=>!init.method)).toBe(true);
 if(legacy){expect(snapshot.status.bridge).toBeUndefined();expect(snapshot.today.by_via).toBeUndefined();}
 else{expect(snapshot.today.by_via.bridge.ttft_median_ms).toBe(37);expect(snapshot.today.total.ttft_median_ms).toBe(29);expect(snapshot.today.keys[0].seconds).toBe(12.5);expect(snapshot.today.keys[0].characters).toBe(340);}
});
