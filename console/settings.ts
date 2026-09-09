import { text, type CopyKey, type Lang } from './copy';
import { escape } from './html';
import type { Settings, Snapshot } from './types';
export type SettingsPatch = Partial<{name:string;web_url:string;slots:number|string;console:string;log_requests:boolean}>;
export interface SettingsUI { draft:SettingsPatch; error?:string; confirm?:boolean; saved?:boolean }
export function settingValues(s:Settings):SettingsPatch{return {name:s.name,web_url:s.configured_web_url,slots:s.slots,console:s.configured_console||'off',log_requests:s.log_requests};}
export function urlError(value:string):boolean {
 if(!value)return false;
 try {const u=new URL(value),h=u.hostname.replace(/^\[|\]$/g,'');const local=h==='localhost'||h==='::1'||/^127\./.test(h)||/^10\./.test(h)||/^192\.168\./.test(h)||/^172\.(1[6-9]|2\d|3[01])\./.test(h);return !!(u.username||u.password||u.search||u.hash)||(u.protocol!=='https:'&&!(u.protocol==='http:'&&local));}catch{return true;}
}
export function settingsSection(data:Snapshot,lang:Lang,ui:SettingsUI,pending:boolean,isRemote=false):string {
 const t=(k:CopyKey,...args:(string|number)[])=>escape(text(lang,k,...args)),s=data.settings,v={...settingValues(s),...ui.draft},count=Object.keys(ui.draft).length;
 const time=(value?:string)=>value?new Date(value).toLocaleTimeString(lang==='zh'?'zh-CN':'en-GB',{hour:'2-digit',minute:'2-digit'}):'';
 const line=(key:keyof SettingsPatch)=>{
  const dirty=key in ui.draft,next=key==='slots'||key==='console',bad=key==='web_url'&&urlError(String(v.web_url));
  let value=dirty?t(next?'changed_next':'changed_live'):key==='name'?t('name_live'):key==='web_url'?t('web_live'):key==='log_requests'?t('log_live'):key==='slots'?t('slots_next',s.running_slots||data.engine.slots):t('console_next',s.console_address||'off');
  if(s.saved_at?.[key]&&!dirty)value=t('saved_time',time(s.saved_at[key]))+' · '+value;
  if(bad)value=t('url_refusal');
  return `<span class="apply ${bad||dirty&&ui.error?'bad':dirty?'dirty':next?'next':'force'}">${value}</span>`;
 };
 const field=(key:keyof SettingsPatch,label:CopyKey,hint:CopyKey,type='text')=>`<label class="field"><span class="field-label">${t(label)}</span><input id="setting-${key}" data-setting="${key}" type="${type}" ${type==='number'?'min="0" step="1"':''} ${key==='name'?'required':''} value="${escape(v[key])}" ${pending?'disabled':''}><span class="field-hint">${t(hint)}</span>${key==='web_url'?`<p class="preview">${t('url_preview',String(v.web_url||s.default_web_url||s.web_url)+'#ic1.…')}</p>`:''}${line(key)}</label>`;
 const remote=s.remote,enabled=!!remote?.enabled;
 return `<section class="sec" id="settings"><div class="sec-in"><div class="sec-head"><h2>${t('settings')}</h2><span class="count">${t('settings_src',s.data_dir)}</span><p class="lead">${t('settings_lead')}</p></div><form class="form" data-form="settings">
 ${field('name','st_name','st_name_h')}${field('web_url','st_web','web_hint')}${field('slots','st_slots','slots_hint','number')}
 <div class="field"><span class="field-label">${t('st_log')}</span><label class="check"><input id="setting-log_requests" data-setting="log_requests" type="checkbox" ${v.log_requests?'checked':''} ${pending?'disabled':''}><span>${t('st_log_h')}</span></label>${line('log_requests')}</div>
 ${isRemote?`<div class="field"><span class="field-label">${t('console_address')}</span><p>${escape(s.console_address||'off')}</p><span class="apply ro">${t('remote_address')}</span></div>`:field('console','console_address','console_hint')}<div class="form-actions"><button type="submit" class="secondary" ${pending||!count?'disabled':''}>${t('save')}</button><span class="saved" role="status">${escape(ui.error||'')||t(pending?'working':ui.saved?'settings_saved':count===1?'one_change':'changes',count)}</span></div>
 <div class="remote"><span class="field-label">${t('remote_access')}</span><label class="check"><input type="checkbox" data-action="${enabled?'remote-confirm':'remote-enable'}" ${enabled?'checked':''} ${pending||!s.console_address?'disabled':''}><span><b>${t('remote_switch')}</b><span class="dim">${t('remote_hint')}</span></span></label><p class="trust">${t('remote_trust')}</p>
 ${remote?.warning_file?`<p class="notice" role="status">${t('remote_recovery',remote.warning_file)}</p>`:''}
 ${enabled?`<div class="rstate"><p class="nowline"><span class="live">■</span> ${t('remote_since',time(remote?.since))} · ${t(remote?.in_use?'in_use':'not_in_use')}</p><button type="button" class="secondary" data-action="remote-rotate" ${pending?'disabled':''}>${t('rotate_code')}</button><button type="button" class="ghost danger" data-action="remote-confirm" ${pending?'disabled':''}>${t('turn_off')}</button></div>`:''}
 ${isRemote?`<p class="here">■ ${t('remote_here')}</p>`:''}${ui.confirm?`<div class="confirm remote-confirm"><p>${t('off_confirm')}</p><div class="btns"><button type="button" class="primary small" data-action="remote-off" ${pending?'disabled':''}>${t('turn_off')}</button><button type="button" class="ghost tiny" data-action="remote-keep" ${pending?'disabled':''}>${t('keep_on')}</button></div></div>`:''}
 <div class="never"><span class="field-label">${t('never_heading')}</span><div class="never-grid">${(['prompts','engine','address'] as const).map((key,i)=>`<div class="fact"><p class="k">0${i+1}</p><p class="v">${t(`never_${key}`)}</p><p class="s">${t(`never_${key}_why`)}</p></div>`).join('')}</div></div></div>
 <div class="ro">${[['ro_data',s.data_dir],['ro_up',data.engine.url],['ro_relay',s.derpmap_url||s.region||text(lang,'default_relay')]].map(([key,value])=>`<div class="fact"><p class="k">${t(key as CopyKey)}</p><p class="v mono">${escape(value)}</p></div>`).join('')}</div><p class="truth">${t(s.log_prompts?'prompts_on':'prompts_off')}</p></form></div></section>`;
}
