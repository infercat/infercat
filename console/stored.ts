import { text, type Lang } from './copy';
import { escape } from './html';
export interface StoredData {
 observed_orphans?:number; cursor:string; reserved:number; retry_cleanup:number; key_id:string; total:number; terminal:number; bytes:number; budget:number; images:number; image_bytes:number; image_budget:number; clear_images:number;
 kinds:Record<string,{stored:number;live:number}>; expires_first?:string; expires_last?:string; pending_cleanup?:number;
 runs:{id:string;kind:string;state:string;bytes:number;expires:string;image?:{bytes:number;expires:string}}[]; truncated:boolean;
}
export interface ClearExpectation { cursor:string; terminal:number; images:number; cleanup:number }
export interface StoredUI { value?:StoredData; error?:string; message?:string; confirm?:ClearExpectation; open?:boolean }
const bytes=(n:number)=>n<1024?`${n} B`:`${(n/(n<1048576?1024:1048576)).toLocaleString('en',{maximumFractionDigits:1})} ${n<1048576?'KiB':'MiB'}`;
export function storedSection(ui:StoredUI|undefined,lang:Lang,pending:boolean,now:number):string {
 const t=(key:Parameters<typeof text>[1],...args:(string|number)[])=>escape(text(lang,key,...args)), s=ui?.value;
 const kindName=(kind:string)=>kind==='image'?t('stored_image_kind'):kind==='agent'?t('stored_agent'):escape(kind);
 const stateName=(state:string)=>['queued','running','waiting','done','failed','cancelled'].includes(state)?t(('stored_'+state) as Parameters<typeof text>[1]):escape(state);
 const expiry=(at?:string)=>at?escape(new Date(at).toLocaleString(lang==='zh'?'zh-CN':'en')):t('not_reported');
 const confirm=ui?.confirm;
 const confirmation=confirm?[confirm.terminal?text(lang,confirm.terminal===1?'stored_confirm_one':'stored_confirm',confirm.terminal,confirm.images?text(lang,confirm.images===1?'stored_confirm_image':'stored_confirm_images',confirm.images):''):'',confirm.cleanup?text(lang,confirm.cleanup===1?'stored_confirm_retry_one':'stored_confirm_retry',confirm.cleanup):'',text(lang,'stored_keep_live')].filter(Boolean).join(' '):'';
 const first=s?.expires_first?Math.max(0,Math.ceil((Date.parse(s.expires_first)-now)/86400000)):null;
 return `<div class="dsec"><span class="field-label">${t('stored')}</span>${!s?`<p class="field-hint">${ui?.error?escape(ui.error):t('stored_loading')}</p>`:`<details class="dis" id="stored-details" ${ui?.open?'open':''}><summary class="nowline">${t('stored_summary',s.total,s.images,bytes(s.bytes+s.image_bytes))}${first===null?'':` · ${t(first===0?'stored_expired':'stored_expires',first)}`}</summary>
 <p class="field-hint">${t('stored_budget',bytes(s.bytes),bytes(s.budget),bytes(s.image_bytes),bytes(s.image_budget))} ${t('stored_reserved',bytes(s.reserved))}${s.bytes+s.reserved>s.budget?` ${t('stored_excess',bytes(s.bytes+s.reserved-s.budget))}`:''}</p>
 <p class="nowline">${Object.entries(s.kinds).map(([kind,count])=>`${kindName(kind)}: ${t('stored_kind',count.stored,count.live)}`).join(' · ')}</p>
 <p class="field-hint">${t('stored_range',expiry(s.expires_first),expiry(s.expires_last))}${s.pending_cleanup===undefined?'':` · ${t(s.pending_cleanup===1?'stored_cleanup_one':'stored_cleanup',s.pending_cleanup)}${s.observed_orphans?` · ${t(s.observed_orphans===1?'stored_orphan':'stored_orphans',s.observed_orphans)}`:''}`}</p>
 <table class="u2"><tbody>${s.runs.map(r=>`<tr><td title="${escape(r.id)}">${kindName(r.kind)} · ${stateName(r.state)}</td><td>${bytes(r.bytes)}<br>${['done','failed','cancelled'].includes(r.state)?expiry(r.expires):''}${r.image?`<br>${t('stored_image',bytes(r.image.bytes),expiry(r.image.expires))}`:''}</td></tr>`).join('')}</tbody></table>${s.truncated?`<p class="field-hint">${t('stored_truncated',s.runs.length,s.total)}</p>`:''}<p class="field-hint">${t('stored_sizes')}</p></details>
 <div class="dactions"><button class="ghost danger" data-action="stored-confirm" ${pending||(!s.terminal&&!s.retry_cleanup)?'disabled':''}>${t('stored_clear')}</button></div>${ui?.confirm?`<div class="confirm"><p>${escape(confirmation)}</p><div class="btns"><button class="primary small" data-action="stored-clear" ${pending?'disabled':''}>${t('stored_clear_yes')}</button><button class="ghost tiny" data-action="stored-keep" ${pending?'disabled':''}>${t('keep')}</button></div></div>`:''}${ui?.message?`<p class="field-hint" role="status">${escape(ui.message)}</p>`:''}${ui?.error?`<p class="field-hint bad" role="status">${escape(ui.error)}</p>`:''}`}</div>`;
}
