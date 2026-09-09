import { text, type CopyKey, type Lang } from './copy';
import { emptyStats, type Snapshot, type Key, type Stats } from './types';

export const escape = (v: unknown) => String(v ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
const compact = (n: number) => Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: n >= 10000 ? 0 : 1 }).format(n || 0).replace("K", "k");
const duration = (s: number) => s >= 3600 ? `${Math.floor(s / 3600)}h${Math.floor(s % 3600 / 60)}m` : s >= 60 ? `${Math.floor(s / 60)}m` : `${Math.floor(s)}s`;
const bytes = (n: number) => n >= 1e9 ? `${(n / 1e9).toFixed(1)} GB` : n >= 1e6 ? `${(n / 1e6).toFixed(1)} MB` : `${compact(n)} B`;
const tokens = (s: Stats) => s.prompt_tokens + s.completion_tokens;
const meter = (value: number, max: number) => max > 0 ? `<progress class="meter-track" max="${max}" value="${Math.max(0, Math.min(value, max))}" aria-label="${value} / ${max}"></progress>` : '<span class="meter-track unknown"></span>';
const mark = '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="2" y="2" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2"/><path d="M7 16 12 7l5 9" fill="none" stroke="currentColor" stroke-width="2"/></svg>';

export function render(data: Snapshot | null, lang: Lang, selected: string | null, stale: number | null, now = Date.now(), hasToken = true, refreshedSeconds = 2): string {
 const t = (k: CopyKey, ...args: (string | number)[]) => escape(text(lang, k, ...args));
 const label = (k: CopyKey) => `<span data-t="${k}">${t(k)}</span>`;
 const n = (v: number) => Intl.NumberFormat(lang === 'zh' ? 'zh-CN' : 'en').format(v || 0);
 const limit = (v: number) => v > 0 ? Intl.NumberFormat('en', {notation:'compact',maximumFractionDigits:0}).format(v).replace('K','k') : '∞';
 const since = (v: string) => Math.max(0, (now - new Date(v).getTime()) / 1000) || 0;
 const ago = (v: string) => {
  if (!v || v.startsWith('0001-')) return t('never');
  const seconds = since(v);
  if (seconds < 60) return t('just_now');
  if (seconds >= 172800) return escape(v.slice(0, 10));
  if (lang === 'en') return t('ago', Math.floor(seconds / (seconds < 3600 ? 60 : 3600)) + (seconds < 3600 ? 'm' : 'h'));
  return escape(new Intl.RelativeTimeFormat('zh-CN').format(-Math.floor(seconds / (seconds < 3600 ? 60 : 3600)), seconds < 3600 ? 'minute' : 'hour'));
 };
 const fact = (key: CopyKey, value: string, sub = '', bad = false) => `<div class="fact"><p class="k">${label(key)}</p><p class="v mono${bad ? ' bad' : ''}">${value}</p><p class="s">${sub}</p></div>`;
 const button = (key: CopyKey, cls = 'secondary') => `<button type="button" class="${cls}" disabled>${label(key)}</button>`;
 const header = `<header class="head"><div class="head-in"><div class="brand">${mark}<span class="wordmark">Infercat<span class="sub">${label('console')}</span></span></div>
 <div class="who"><strong>${escape(data?.status.name || '')}</strong><span class="dim">${data ? `${escape(data.engine.kind)} · ${escape(data.engine.models?.join(', '))} · ${duration(data.status.uptime_s)}` : ''}</span></div>
 <div class="head-right"><span class="path">${label('on_machine')}</span><span class="langs"><button data-lang="en" ${lang === 'en' ? 'aria-current="true"' : ''}>EN</button> · <button data-lang="zh" ${lang === 'zh' ? 'aria-current="true"' : ''}>中文</button></span><span class="build">${escape(data?.status.version || import.meta.env.VITE_APP_VERSION)}</span></div></div></header>`;
 if (!data || !hasToken) return `${header}<main class="sec-in" role="status">${!hasToken ? t('need_token') : stale !== null ? t('stale', stale) : t('loading')}</main>`;
 const { status: s, settings: cfg, keys, today, week } = data;
 const e = { ...data.engine, models: data.engine.models || [] };
 const day = today.total, health = e.health.ok ? `${t('healthy')} ${duration(since(e.health.since))}` : t('down', duration(since(e.health.since)));
 const active = keys.filter(k => k.status === 'active'), current = keys.filter(k => k.status !== 'revoked'), revoked = keys.filter(k => k.status === 'revoked');
 const live = (k: Key) => s.keys.find(v => v.id === k.id) || { in_flight: 0, rpm_used: 0, tpm_used: 0, today_tokens: k.today_tokens };
 const modelsLine = (k: Key) => !k.limits.models?.length ? t('all_models') : k.limits.models.length === 1 ? t('one_model', k.limits.models[0]) : t('n_models', k.limits.models.length, e.models.length);
 const limitsLine = (k: Key) => `${t('limits', limit(k.limits.rpm), limit(k.limits.tpm), limit(k.limits.max_concurrent), limit(k.limits.max_output_tokens))} · ${modelsLine(k)}`;
 const nowLine = (k: Key) => k.status === 'paused' ? t('s_paused') : `${live(k).in_flight ? t('in_flight', live(k).in_flight) : t('idle')} · ${t('minute', live(k).rpm_used, limit(k.limits.rpm))}`;
 const keyTable = (list: Key[], isRevoked = false) => `<table class="tbl${isRevoked ? ' revoked' : ''}"><thead><tr>${(['th_friend','th_status','th_now','th_today','th_limits','th_seen'] as CopyKey[]).map(k => `<th>${label(k)}</th>`).join('')}<th></th></tr></thead><tbody>${list.map(k => {
  const l = live(k), status = t(`s_${k.status}`);
  return `<tr class="row ${k.status}" data-key="${escape(k.id)}" role="button" tabindex="0" aria-label="${escape(k.name)} · ${status}">
  <td class="c-name"><div><span class="name">${escape(k.name)}</span><span class="id">${escape(k.id)}</span></div><span class="st-word">${status}</span></td><td class="c-st st">${status}</td>
  <td class="c-now mono"><span class="now ${k.status === 'paused' ? 'paused' : l.in_flight ? 'live' : ''}">${nowLine(k)}</span></td>
  <td class="c-today"><div class="meter"><span class="meter-label">${compact(l.today_tokens)} / ${limit(k.limits.daily_tokens)}</span>${meter(l.today_tokens, isRevoked ? 0 : k.limits.daily_tokens)}</div></td>
  <td class="c-lim lim">${limitsLine(k)}</td><td class="c-ago mono ago">${ago(k.last_seen)}</td><td class="chev">›</td></tr>`;
 }).join('')}</tbody></table>`;
 const ms = (v: number) => v ? v >= 1000 ? `${(v / 1000).toFixed(1)} s` : `${v} ms` : '—';
 const usageTable = (a: Stats, b: Stats, drawer = false, lastCall = '') => {
  const errors = (s: Stats) => `${n(s.errors)}<span class="sub">${escape(Object.entries(s.errors_by_code || {}).map(([key, count]) => `${key} ${count}`).join(' · '))}</span>`;
  if (drawer) {
   const rows: [CopyKey, string, string][] = [
    ['u_calls', n(a.model_calls), n(b.model_calls)],
    ['u_tokens', `${compact(a.prompt_tokens)} → ${compact(a.completion_tokens)}`, `${compact(b.prompt_tokens)} → ${compact(b.completion_tokens)}`],
    ['u_err', errors(a), errors(b)], ['u_ttft50', ms(a.ttft_median_ms), ms(b.ttft_median_ms)],
   ];
   return `<table class="u2"><thead><tr><th></th><th>${label('u_today')}</th><th>${label('u_week')}</th></tr></thead><tbody>${rows.map(([key,a,b])=>`<tr><td>${label(key)}</td><td>${a}</td><td>${b}</td></tr>`).join('')}<tr><td>${label('u_lastcall')}</td><td colspan="2">${ago(lastCall)}</td></tr></tbody></table>`;
  }
  const rows: [CopyKey, string, string][] = [
   ['u_calls', n(a.model_calls), n(b.model_calls)], ['u_polls', n(a.app_polls), n(b.app_polls)], ['u_err', errors(a), errors(b)],
   ['u_prompt', n(a.prompt_tokens), n(b.prompt_tokens)], ['u_compl', n(a.completion_tokens), n(b.completion_tokens)],
   ['u_ttft', `${ms(a.ttft_median_ms)} · ${ms(a.ttft_p95_ms)}`, `${ms(b.ttft_median_ms)} · ${ms(b.ttft_p95_ms)}`],
   ['u_total', `${ms(a.total_median_ms)} · ${ms(a.total_p95_ms)}`, `${ms(b.total_median_ms)} · ${ms(b.total_p95_ms)}`],
  ];
  return `<table class="${drawer ? 'u2' : 'utbl'}"><thead><tr><th></th><th>${label('u_today')}</th><th>${label('u_week')}</th></tr></thead><tbody>${rows.map(([key, a, b]) => `<tr><td>${label(key)}${key === 'u_polls' ? `<span class="sub">${t('u_polls_s')}</span>` : key === 'u_total' ? `<span class="sub">${t('u_total_s')}</span>` : key === 'u_ttft' ? '<span class="sub">p50 · p95</span>' : ''}</td><td>${a}</td><td>${b}</td></tr>`).join('')}</tbody></table>`;
 };
 const modelOpenTo = (k: Key, model: string) => (!s.models_pinned?.length || s.models_pinned.includes(model)) && (!k.limits.models?.length || k.limits.models.includes(model));
 const modelRows = e.models.map(model => `<tr><td class="mono">${escape(model)}${s.models_pinned?.includes(model) ? ` <span class="dim">· ${t('pinned')}</span>` : ''}</td><td class="num">${t('not_reported')}</td><td class="num">${n(day.model_calls_by_model?.[model] || 0)}</td><td class="c-keys">${current.every(k => modelOpenTo(k, model)) ? t('open_all', current.length) : t('open_to', current.filter(k => modelOpenTo(k, model)).length, current.length)}</td></tr>`).join('');
 const daily = week.daily || [], peak = Math.max(1, ...daily.map(d => tokens(d.total)));
 const days = daily.map((d, i) => `<div class="day${i === daily.length - 1 ? ' today' : ''}"><p class="k">${i === daily.length - 1 ? t('u_today') : escape(new Date(d.date + 'T12:00:00Z').toLocaleDateString(lang === 'zh' ? 'zh-CN' : 'en', { weekday: 'short', timeZone: 'UTC' }))}</p><p class="v">${compact(tokens(d.total))}</p><p class="c">${d.total.model_calls ? t('calls', n(d.total.model_calls)) : t('no_calls')}</p>${meter(tokens(d.total), peak)}</div>`).join('');
 const field = (key: CopyKey, value: string | number, hint: CopyKey) => `<label class="field"><span class="field-label">${label(key)}</span><input disabled value="${escape(value)}"><span class="field-hint">${t(hint)}</span></label>`;
 const section = (id: string, key: CopyKey, subtitle: string, body: string) => `<section class="sec" id="${id}"><div class="sec-in"><div class="sec-head"><h2>${label(key)}</h2><span class="count">${subtitle}</span></div>${body}</div></section>`;
 let drawer = '';
 const k = keys.find(k => k.id === selected);
 if (k) {
  const l = live(k), a = today.keys?.find(v => v.key_id === k.id) || emptyStats, b = week.keys?.find(v => v.key_id === k.id) || emptyStats;
  const limits: [CopyKey, keyof Key['limits']][] = [['l_rpm','rpm'],['l_tpm','tpm'],['l_conc','max_concurrent'],['l_out','max_output_tokens'],['l_ctx','max_context'],['l_daily','daily_tokens']];
  drawer = `<div class="scrim" data-close="true"></div><aside class="drawer" role="dialog" aria-modal="true" aria-labelledby="friend-title"><div class="drawer-head"><h3 id="friend-title">${escape(k.name)}</h3><button class="ghost tiny x" data-close="true">${t('close')}</button></div>
  <p class="meta">${escape(k.id)} · <b>${t(`s_${k.status}`)}</b> · ${t('created')} ${escape(k.created_at.slice(0, 10))}</p>
  <div class="dsec"><span class="field-label">${label('d_now')}</span><p class="nowline">${l.in_flight ? '<span class="live" aria-hidden="true">■</span> ' : ''}${nowLine(k)} · ${t('tpm', compact(l.tpm_used), limit(k.limits.tpm))}<br>${t('tokens_today', compact(l.today_tokens), limit(k.limits.daily_tokens))} · ${t('u_lastcall')} ${ago(k.last_seen)}</p></div>
  <div class="dsec"><span class="field-label">${label('d_limits')}</span><div class="lims">${limits.map(([labelKey, prop]) => `<label for="limit-${prop}">${t(labelKey)}</label><input id="limit-${prop}" disabled value="${escape(k.limits[prop])}">${prop === 'max_context' ? `<p class="h">${t('context_hint', e.model_context ? n(e.model_context) : text(lang,'not_reported'))}</p>` : ''}`).join('')}<span>${label('l_models')}</span><span class="dim">${modelsLine(k)}</span><div class="models">${[...new Set([...e.models, ...(k.limits.models || [])])].map(model => `<label><input type="checkbox" disabled ${!k.limits.models?.length || k.limits.models.includes(model) ? 'checked' : ''}>${escape(model)}</label>`).join('')}</div></div><div class="dactions">${button('save_limits')}<span class="note">${t('readonly')}</span></div></div>
  <div class="dsec"><span class="field-label">${label('d_usage')}</span>${usageTable(a,b,true,k.last_seen)}</div><div class="dsec"><span class="field-label">${label('d_key')}</span><div class="dactions">${button(k.status === 'paused' ? 'resume' : 'pause')}${button('rotate')}${button('revoke','ghost danger')}</div></div><p class="dfoot">${t('hash_only')}</p></aside>`;
 }
 return `<div class="${stale !== null ? 'stale' : ''}"><div id="page" ${k ? 'inert' : ''}>${header}
 <div class="strip"><div class="strip-in"><span>${(['friends','engine','usage','settings'] as const).map(id => `<a href="#${id}">${t(`ix_${id}`)}</a>`).join(' · ')}</span><span role="status">${stale !== null ? t('stale',stale) : t('refreshed',refreshedSeconds)}</span></div></div>
 ${!e.health.ok ? `<p class="degraded">${t('degraded',e.kind,e.url)}</p>` : ''}
 <main><section class="sec first overview"><div class="sec-in"><div class="facts">
 ${fact('f_engine',`${e.kind === 'unknown' ? t('unknown') : escape(e.kind)} · ${health}`,t('engine_sub',e.slots,e.model_context ? compact(e.model_context) : text(lang,'not_reported')),!e.health.ok)}
 ${fact('f_tunnel',`${t('relay')} ${escape(s.tunnel.region || '—')}`,t('tunnel_sub',s.tunnel.clients,bytes(s.tunnel.rx_bytes),bytes(s.tunnel.tx_bytes)))}
 ${fact('f_now',t('right_now',s.queue.in_flight,s.queue.waiting),t('throughput',compact(s.engine.tokens_per_s_1m)))}
 ${fact('f_today',t('today_value',n(day.model_calls),compact(tokens(day))),t('today_sub',day.errors,ms(day.ttft_median_ms)))}
 </div></div></section>
 <section class="sec" id="friends"><div class="sec-in"><div class="sec-head"><h2>${label('friends')}</h2><span class="count">${t('friends_count',current.length,active.length,current.filter(k=>k.status==='paused').length)}</span><p class="lead">${t('friends_lead')}</p><div class="act">${button('mint','primary')}</div></div>
 ${current.length ? keyTable(current) : `<p class="empty">${t('no_keys')}</p>`}${revoked.length ? `<details class="dis"><summary>${t('revoked_n',revoked.length)}</summary>${keyTable(revoked,true)}</details>` : ''}<p class="foot-line">${t('counts_line')}</p></div></section>
 ${section('engine','engine',`${escape(e.kind)} · ${escape(e.url)}`,`<div class="facts8">${fact('e_kind',escape(e.kind),t('not_reported'))}${fact('e_health',e.health.ok ? t('healthy') : health,t('since',duration(since(e.health.since)),ago(e.probed_at)),!e.health.ok)}${fact('e_slots',n(e.slots),t(cfg.slots ? 'slots_override' : 'slots_auto'))}${fact('e_ctx',e.model_context ? n(e.model_context) : t('not_reported'),t('e_ctx_s'))}${fact('e_tps',compact(s.engine.tokens_per_s_1m)+' tok/s',t('e_tps_s'))}${fact('e_says',s.engine.metrics ? t('metrics',s.engine.busy,s.engine.waiting) : t('no_metrics'),s.engine.metrics ? t('e_says_s') : '')}${fact('e_mem',s.engine.memory_bytes ? bytes(s.engine.memory_bytes) : t('not_reported'),s.engine.metrics ? t('kv',s.engine.kv_cache_pct.toFixed(1)) : '')}${fact('e_peak',t('in_flight',s.engine.slots_peak_sampled),t('e_peak_s'))}</div>
 <table class="tbl models"><thead><tr>${(['m_model','m_ctx','m_calls','m_keys'] as CopyKey[]).map(key=>`<th${key==='m_keys'?' class="c-keys"':''}>${label(key)}</th>`).join('')}</tr></thead><tbody>${modelRows}</tbody></table><p class="foot-line">${t('engine_foot')}${!e.health.ok ? ' '+escape(e.health.err) : ''}</p>`)}
 ${section('usage','usage',t('usage_src'),`<div class="usage">${usageTable(day,week.total)}<div><p class="days-cap">${t('days_cap')}</p><div class="days">${days}</div><p class="foot-line">${t('usage_foot')}</p></div></div>${!week.total.requests ? `<p class="empty">${t('no_usage')}</p>`:''}${week.malformed_lines ? `<p class="notice">${t('malformed',week.malformed_lines)}</p>`:''}`)}
 ${section('settings','settings',t('settings_src',cfg.data_dir),`<div class="form">${field('st_name',cfg.name,'st_name_h')}${field('st_web',cfg.configured_web_url,'st_web_h')}${field('st_slots',cfg.slots,'slots_hint')}
 <div class="field"><span class="field-label">${label('st_log')}</span><label class="check"><input type="checkbox" disabled ${cfg.log_requests?'checked':''}><span>${t('st_log_h')} (${t('not_remembered')})</span></label></div>
 <div class="form-actions">${button('save')}<span class="saved">${t('restart')}</span></div><p class="field-hint">${t('effective_web',cfg.web_url)}</p><div class="ro">${fact('ro_data',escape(cfg.data_dir),t('ro_data_s'))}${fact('ro_up',escape(e.url),cfg.upstream ? '--upstream' : t('ro_up_s'))}${fact('ro_relay',cfg.derpmap_url || cfg.region ? `${escape(cfg.derpmap_url || '—')} · ${escape(cfg.region || 'auto')}` : t('default_relay'),t('ro_relay_s'))}</div><p class="truth">${t(cfg.log_prompts?'prompts_on':'prompts_off')}</p></div>`)}
 </main><footer class="strip"><div class="strip-in"><span>Infercat ${escape(s.version)} · MIT · <a href="https://github.com/infercat/infercat" target="_blank" rel="noreferrer">${t('source')}</a></span><span>${t('made')}</span></div></footer></div>${drawer}</div>`;
}
