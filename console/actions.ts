import { text, type CopyKey, type Lang } from './copy';
import { escape } from './html';
import type { Key, Limits, Snapshot } from './types';

export interface Invite { key_id: string; name: string; invite: string; link?: string }
export interface DrawerState {
 mode: 'mint' | 'key'; name: string; limits: Limits; dirty: boolean; duplicateId?: string;
 confirm?: boolean; rotated?: boolean; once?: Invite; qr?: string; qrFailed?: boolean;
 error?: string; message?: CopyKey; copied?: 'link' | 'code';
}
export const defaults: Limits = { rpm: 20, tpm: 20000, max_concurrent: 1, max_output_tokens: 4096, max_context: 0, daily_tokens: 200000, models: [] };
export function draft(key?: Key): DrawerState {
 return { mode: key ? 'key' : 'mint', name: key?.name || '', limits: structuredClone(key?.limits || defaults), dirty: false };
}
const numeric: [CopyKey, Exclude<keyof Limits, 'models'>][] = [['l_rpm','rpm'],['l_tpm','tpm'],['l_conc','max_concurrent'],['l_out','max_output_tokens'],['l_ctx','max_context'],['l_daily','daily_tokens'],['l_audio','daily_audio_seconds'],['l_speech','daily_speech_chars']];
const tr = (lang: Lang) => (key: CopyKey, ...args: (string | number)[]) => escape(text(lang, key, ...args));
function button(lang: Lang, action: string, key: CopyKey, pending: boolean, cls = 'secondary', name = '') {
 return `<button type="button" class="${cls}" data-action="${action}" ${pending ? 'disabled' : ''}>${tr(lang)(key, name)}</button>`;
}
export function message(lang: Lang, ui?: DrawerState, pending = false): string {
 return `<p class="field-hint action-message" role="status">${pending ? tr(lang)('working') : ui?.error ? escape(ui.error) : ui?.message ? tr(lang)(ui.message) : ''}</p>`;
}
export function limitsForm(data: Snapshot, lang: Lang, ui?: DrawerState, key?: Key, pending = false): string {
 const t = tr(lang), limits = ui?.limits || key?.limits || defaults, disabled = pending || key?.status === 'revoked';
 const models = [...new Set([...(data.engine.models || []), ...(limits.models || [])])];
 return `<form id="key-limits" data-form="limits"><div class="dsec"><span class="field-label">${t('d_limits')}</span><div class="lims">${numeric.filter(([,prop]) => limits[prop] !== undefined).map(([label,prop]) => `<label for="limit-${prop}">${t(label)}</label><input id="limit-${prop}" name="${prop}" type="number" min="0" step="1" required value="${escape(limits[prop])}" ${disabled ? 'disabled' : ''}>${prop === 'max_context' ? `<p class="h">${t('context_hint',data.engine.model_context || text(lang,'not_reported'))}</p>` : ''}`).join('')}
 <span>${t('l_models')}</span><span class="dim">${!limits.models?.length ? t('all_models') : ''}</span><div class="models">${models.map(model => `<label><input type="checkbox" data-model="${escape(model)}" ${!limits.models?.length || limits.models.includes(model) ? 'checked' : ''} ${disabled ? 'disabled' : ''}>${escape(model)}</label>`).join('')}</div><p class="h">${t('models_hint')}</p></div>
 <p class="field-hint">${t('limit_hint')}</p><div class="dactions"><button type="submit" class="${ui?.mode === 'mint' ? 'primary' : 'secondary'}" ${disabled ? 'disabled' : ''}>${t(ui?.mode === 'mint' ? 'mint_submit' : 'save_limits')}</button>${key ? `<span class="note">${t('in_force',key.name)}</span>` : ''}</div>${message(lang,ui,pending)}</div></form>`;
}
export function keyActions(lang: Lang, ui: DrawerState | undefined, key: Key, pending: boolean): string {
 const t = tr(lang);
 if (key.status === 'revoked') return '';
 return `<div class="dsec"><span class="field-label">${t('d_key')}</span><div class="dactions">${button(lang,key.status === 'paused' ? 'resume' : 'pause',key.status === 'paused' ? 'resume' : 'pause',pending)}${button(lang,'rotate','rotate',pending)}${button(lang,'confirm','revoke',pending,'ghost danger')}</div><p class="field-hint">${t('pause_h',key.name)}</p>${ui?.confirm ? `<div class="confirm"><p>${t('revoke_q',key.name)}</p><div class="btns">${button(lang,'revoke','revoke_yes',pending,'primary small',key.name)}${button(lang,'keep','keep',pending,'ghost tiny')}</div></div>` : ''}</div>`;
}
export function specialDrawer(data: Snapshot, lang: Lang, ui: DrawerState, pending: boolean): string {
 const t = tr(lang), once = ui.once;
 const returnedKey = data.keys.find(k => k.id === once?.key_id);
 const compact = (n: number) => n ? Intl.NumberFormat('en',{notation:'compact',maximumFractionDigits:0}).format(n).replace('K','k') : '∞';
 const l = returnedKey?.limits;
 const line = l ? `${t('limits',compact(l.rpm),compact(l.tpm),compact(l.max_concurrent),compact(l.max_output_tokens))} · ${t('l_ctx')}: ${l.max_context ? compact(l.max_context) : t('engine_ceiling')} · ${t('l_daily')}: ${compact(l.daily_tokens)} · ${l.models?.length ? escape(l.models.join(', ')) : t('all_models')}${numeric.slice(6).filter(([,prop])=>l[prop] !== undefined).map(([label,prop])=>` · ${t(label)}: ${compact(l[prop]!)}`).join('')}` : '';
 const title = once ? t(ui.rotated ? 'rotate_title' : 'once_title',once.name) : t('mint');
 return `<div class="scrim" data-close="true"></div><aside class="drawer" role="dialog" aria-modal="true" aria-labelledby="friend-title"><div class="drawer-head"><h3 id="friend-title">${title}</h3><button class="ghost tiny x" data-close="true">${t('close')}</button></div>${once ? `
 <p class="meta">${escape(once.key_id)} · ${returnedKey ? `<b>${t(`s_${returnedKey.status}`)}</b> · ` : ''} ${t(ui.rotated ? 'rotated_line' : 'minted_now')}</p><div class="once"><span class="tag">${t('once_tag')}</span><p>${t('once_p')}</p><p class="code">${once.invite.split('.').map((part,i) => `<span class="${i === 2 ? 'sec-part' : ''}">${escape(part)}</span>`).join('<span class="dot">.</span>')}</p><div class="once-grid"><div><div class="btns">${once.link ? button(lang,'copy-link',ui.copied === 'link' ? 'copied' : 'copy_link',false,'primary small') : ''}${button(lang,'copy-code',ui.copied === 'code' ? 'copied' : 'copy_code',false)}</div>${once.link ? `<p>${t('send_p',once.name)}</p><p class="link">${escape(once.link.length > 60 ? once.link.slice(0,40) + '…' + once.link.slice(-8) : once.link)}</p>` : `<p>${t('no_web',once.name)}</p>`}</div><div>${ui.qr || ''}<p class="qrcap">${t(ui.qrFailed ? 'qr_failed' : 'scan')}</p></div></div></div>${message(lang,ui,pending)}${l ? `<div class="dsec"><span class="field-label">${t('d_limits')}</span><p class="nowline">${line}</p><p class="field-hint">${t('minted_limits',once.name)}</p></div>` : ''}<div class="dactions">${button(lang,'done','done',false,'primary')}<span class="note">${t('lost_h')}</span></div>
 ` : `<label class="field"><span class="field-label">${t('new_name')}</span><input id="invite-name" name="name" form="key-limits" required value="${escape(ui.name)}" ${pending ? 'disabled' : ''}><span class="field-hint">${t('name_hint')}</span></label>${limitsForm(data,lang,ui,undefined,pending)}${ui.duplicateId ? `<button type="button" class="ghost" data-key="${escape(ui.duplicateId)}">${t('rotate')}</button>` : ''}`}</aside>`;
}
