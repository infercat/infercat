import { useEffect, useRef, useState } from 'react';
import { mountConsole } from '@infercat/console';
import { decodeCode,isAdminCode } from '../admin-route';
import { openTransport,cityFor,type Transport,type PingResult } from '../transport';
import { tr,appLanguage } from '../i18n/text';
import { VERSION } from '../product';
declare const __DEFAULT_DIRECT_URL__:string;
export default function RemoteConsole({code,onLeave}:{code:string;onLeave:()=>void}){
 const host=useRef<HTMLDivElement>(null),[input,setInput]=useState(code),[attempt,setAttempt]=useState({code,run:0}),[state,setState]=useState(''),[error,setError]=useState('');
 useEffect(()=>{
  if(!attempt.code)return;
  let ended=false,transport:Transport|undefined,consoleApp:ReturnType<typeof mountConsole>|undefined,pingTimer:ReturnType<typeof setInterval>|undefined;
  const oldTitle=document.title;setState('opening');setError('');
  const timeout=setTimeout(()=>{ended=true;transport?.close();setError(tr('app_admin_timeout'));setState('');},20000);
  void(async()=>{
   try{
    const parsed=decodeCode(attempt.code);if(!isAdminCode(attempt.code))throw new Error(tr('app_admin_expected'));
    const direct=import.meta.env.DEV&&new URLSearchParams(location.search).has('direct');
    const opened=await openTransport(parsed.addr,{assetBase:import.meta.env.PROD?`/runtime/${encodeURIComponent(VERSION)}/`:undefined,mode:direct?'direct':'tunnel',...(direct?{directURL:__DEFAULT_DIRECT_URL__}:{})});
    if(ended){opened.transport.close();return;}transport=opened.transport;clearTimeout(timeout);
    let path:PingResult|null=opened.path;
    const view={language:appLanguage(),path:(lang:'en'|'zh')=>path?tr(path.direct?'app_admin_direct':'app_admin_relay',{place:cityFor(/^DERP\((.*)\)$/.exec(path.via)?.[1]||path.via),ms:Math.round(path.rttMs)},lang):tr('app_admin_path_unknown',{},lang),leave:onLeave};
    const request:typeof fetch=(url,init)=>{
     if(typeof url!=='string'||!url.startsWith('/api/'))return Promise.reject(new Error('invalid console path'));
     return opened.transport.fetch('/console'+url,init);
    };
    if(!host.current){opened.transport.close();return;}
    consoleApp=mountConsole(host.current,parsed.secret,request,view);setState('connected');
    pingTimer=setInterval(()=>{void opened.transport.ping().then(p=>{path=p;}).catch(()=>{path=null;});},30000);
   }catch(e){if(!ended){setError(e instanceof Error?e.message:tr('app_admin_timeout'));setState('');}transport?.close();clearTimeout(timeout);}
  })();
  return()=>{ended=true;clearTimeout(timeout);clearInterval(pingTimer);consoleApp?.stop();transport?.close();document.title=oldTitle;};
 },[attempt,onLeave]);
 return <>{state!=='connected'&&<main className="admin-entry"><h1>{tr('app_admin_title')}</h1><p>{tr('app_admin_trust')}</p><form onSubmit={e=>{e.preventDefault();setAttempt({code:input.trim(),run:attempt.run+1});}}><label>{tr('app_admin_code')}<textarea value={input} onChange={e=>setInput(e.target.value)} autoComplete="off" spellCheck={false} required/></label><button className="primary" disabled={state==='opening'}>{tr('app_open_console')}</button></form><p role="status">{error|| (state==='opening'?tr('app_opening'):'')}</p><button className="ghost" onClick={onLeave}>{tr('app_forget_console')}</button></main>}<div ref={host} data-remote-console/></>;
}
