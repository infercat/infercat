// @vitest-environment jsdom
import {test,expect,vi,afterEach} from 'vitest';
import fixture from './test/fixture.json';
import {createConsole} from './app';
let app;
afterEach(()=>{app?.stop();vi.useRealTimers();});
const tick=()=>vi.advanceTimersByTimeAsync(0);
async function setup(mode='ok'){
 vi.useFakeTimers();history.replaceState(null,'','/');document.body.innerHTML='<div id="app"></div>';
 const data=structuredClone(fixture);Object.assign(data.settings,{writes_supported:true,default_web_url:'https://infercat.ai',configured_console:'127.0.0.1:9101',console_address:'127.0.0.1:9101',running_slots:2,remote:{enabled:false,in_use:false}});
 const writes=[];let finish;
 const request=async(url,init)=>{
  const path=url.slice(5);
  if(init.method){writes.push({path,...init});if(mode==='network')throw new Error('lost');if(mode==='refusal')return new Response(JSON.stringify({error:'not saved — config.json is not writable'}),{status:400});if(mode==='pending')await new Promise(resolve=>{finish=resolve;});
   if(path==='settings'){const body=JSON.parse(init.body);for(const [key,value]of Object.entries(body))data.settings[key==='web_url'?'configured_web_url':key==='console'?'configured_console':key]=value;return new Response(JSON.stringify(data.settings));}
   data.settings.remote.enabled=path!=='remote/off';data.settings.remote.since='2026-09-09T12:04:00Z';return new Response(JSON.stringify(path==='remote/off'?{ok:true}:{key_id:'admin',name:data.settings.name,invite:'ia1.host.secret',link:'https://infercat.ai/#ia1.host.secret'}));
  }
  return new Response(JSON.stringify(path==='usage?window=today'?data.today:path==='usage?window=week'?data.week:data[path]));
 };
 app=createConsole(document.querySelector('#app'),'token',request);await tick();return {data,writes,finish:()=>finish()};
}
function edit(key,value){const el=document.querySelector(`[data-setting="${key}"]`);if(el.type==='checkbox')el.checked=value;else el.value=value;el.dispatchEvent(new Event('input',{bubbles:true}));}
function submit(){document.querySelector('[data-form="settings"]').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));}
const click=a=>document.querySelector(`[data-action="${a}"]`).click();
for(const mode of ['ok','refusal','network'])test('settings save '+mode+' with no replay',async()=>{
 const s=await setup(mode);edit('name','renamed');edit('slots','4');edit('log_requests',false);submit();await tick();expect(s.writes).toHaveLength(1);expect(JSON.parse(s.writes[0].body)).toEqual({name:'renamed',slots:4,log_requests:false});
 if(mode==='ok'){expect(s.data.settings.name).toBe('renamed');expect(document.querySelector('#settings').textContent).toContain('running with 2');expect(document.querySelector('#settings').textContent).toContain('not remembered');}
 else expect(document.querySelector('#settings').textContent).toContain(mode==='refusal'?'not saved — config.json is not writable':'may have completed');
 await vi.advanceTimersByTimeAsync(4000);expect(s.writes).toHaveLength(1);
});
test('dirty fields and exact URL preview survive polling',async()=>{
 await setup();edit('web_url','http://example.com/');edit('console','off');await app.refresh();expect(document.querySelector('#setting-web_url').value).toBe('http://example.com/');expect(document.querySelector('.preview').textContent).toContain('http://example.com/#ic1.');expect(document.querySelector('#settings').textContent).toContain('not https');expect(document.querySelector('#settings').textContent).toContain('waits for the host');
});
for(const action of ['remote-enable','remote-rotate'])for(const mode of ['ok','refusal','network'])test(action+' '+mode,async()=>{
 const s=await setup(mode);if(action==='remote-rotate'){s.data.settings.remote.enabled=true;await app.refresh();}click(action);await tick();expect(s.writes).toHaveLength(1);
 if(mode==='ok'){expect(document.querySelector('.once .code').textContent).toBe('ia1.host.secret');expect(document.querySelector('.drawer').textContent).toContain('send it to no one');click('done');expect(document.querySelector('.once')).toBeNull();expect(document.querySelector('.rstate').textContent).toContain('not in use');}
 else expect(document.querySelector('.drawer').textContent).toContain(mode==='refusal'?'not saved':'may have completed');
});
test('off confirms inline, Keep sends nothing, then off commits',async()=>{
 const s=await setup();s.data.settings.remote.enabled=true;s.data.settings.remote.in_use=true;await app.refresh();click('remote-confirm');expect(document.querySelector('.remote-confirm').textContent).toContain('ends every remote session');click('remote-keep');expect(s.writes).toHaveLength(0);click('remote-confirm');click('remote-off');await tick();expect(s.writes[0].path).toBe('remote/off');expect(document.querySelector('.rstate')).toBeNull();expect(document.querySelector('.never')).not.toBeNull();
});
test('closed pending admin card cannot resurrect a late code',async()=>{
 const s=await setup('pending');click('remote-enable');await tick();document.querySelector('button[data-close]').click();s.finish();await tick();expect(document.body.textContent).not.toContain('ia1.host.secret');expect(document.querySelector('.once')).toBeNull();expect(s.writes).toHaveLength(1);
});
for(const lang of ['en','zh'])test('damaged admin file diagnostic '+lang,async()=>{
 const s=await setup();s.data.settings.remote.warning_file='/private/host/<admin>/admin.json';await app.refresh();document.querySelector(`[data-lang="${lang}"]`).click();
 const line=document.querySelector('.remote [role="status"]');expect(line.textContent).toContain(s.data.settings.remote.warning_file);expect(line.textContent).toContain(lang==='en'?'Turn remote access on again':'请重新开启远程访问');expect(line.querySelector('admin')).toBeNull();
 delete s.data.settings.remote.warning_file;await app.refresh();expect(document.querySelector('.remote [role="status"]')).toBeNull();
});
