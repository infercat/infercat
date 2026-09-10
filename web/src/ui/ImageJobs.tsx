import { hostImages } from '../api';
import { useEffect, useState } from 'react';
import { GatewayError, timeoutSignal } from '../api';
import { imageArtifact, imageElapsed, imageGone, type ImageJob } from '../imageJobs';
import { appLanguage, tr } from '../i18n/text';
import { bytesLabel } from '../images';
import type { Live } from '../session';
import type { RunItem } from '../storage';
import { voiceTime } from '../voice';
import RunRow, { runLine, type RunView } from './Run';
function useOutput(job: ImageJob | undefined, live: Live) {
  const [loaded, setLoaded] = useState({ id: '', url: '', gone: false, error: '' });
  const available = Boolean(job?.output && !imageGone(job));
  useEffect(() => {
    if (!job || !available || live.offline) return;
    const ac = new AbortController(); let url = '';
    void imageArtifact(live.transport, live.secret, job.id, timeoutSignal(30000, ac.signal)).then((blob) => {
      if (ac.signal.aborted) return;
      url = URL.createObjectURL(blob); setLoaded({ id: job.id, url, gone: false, error: '' });
    }).catch((error) => { if (!ac.signal.aborted) setLoaded({ id: job.id, url: '', gone: error instanceof GatewayError && error.status === 404, error: String(error) }); });
    return () => { ac.abort(); if (url) URL.revokeObjectURL(url); };
  }, [job?.id, available, live.transport, live.secret, live.offline]);
  return loaded.id === job?.id && available ? loaded : { url: '', gone: false, error: '' };
}
export async function saveImage(job: ImageJob, live: Live) {
  const blob = await imageArtifact(live.transport, live.secret, job.id, timeoutSignal(30000), true), url = URL.createObjectURL(blob);
  const a = document.createElement('a'); a.href = url; a.download = `${job.id}.${blob.type === 'image/jpeg' ? 'jpg' : 'png'}`; a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export function imageView(job: ImageJob, now = Date.now()): RunView { return { id: job.id, runKind: 'image', state: job.state, elapsed: imageElapsed(job, now), position: (job.position ?? 0) > 0 ? job.position : undefined, steps: [], outputs: [], cancelRequested: job.cancel_requested, outputGone: imageGone(job, now), error: job.reason }; }
export function ImageJobRow({ item, live, connected, disabled, pending, onCancel, onRetry }: { item: RunItem; live: Live; connected: boolean; disabled: boolean; pending: boolean; onCancel: () => void; onRetry: () => void }) {
  const output = useOutput(item.job, live), [error, setError] = useState('');
  if (!item.job) return <div className="row assistant run"><p className="spoken">{tr(item.submission === 'pending' ? 'app_run_submitting' : 'app_job_uncertain', { host: live.me.host.name })}</p>{item.details && <details className="host-said"><summary>{tr('app_details')}</summary><pre>{item.details}</pre></details>}</div>;
  const job = item.job, view = imageView(job);
  view.outputGone ||= output.gone;
  if (output.url) view.images = [{ name: job.input.prompt, url: output.url, size: job.output!.bytes }];
  return <><RunRow run={view} host={live.me.host.name} connected={connected} disabled={disabled} pending={pending} onCancel={onCancel} onAnswer={() => {}} onRetry={onRetry} onSave={() => { void saveImage(job, live).catch((e) => setError(String(e))); }} />{(error || output.error && !output.gone) && <details className="host-said"><summary>{tr('app_details')}</summary><pre>{error || output.error}</pre></details>}</>;
}
export function ImagesSheet({ jobs, live, connected, disabled, pending, onClose, onEdit, onConversation, conversationTitle, onCancel, onDiscard }: { jobs: ImageJob[]; live: Live; connected: boolean; disabled: boolean; pending: Set<string>; onClose: () => void; onEdit: (text: string) => void; onConversation: (job: ImageJob) => void; conversationTitle: (job: ImageJob) => string; onCancel: (job: ImageJob) => void; onDiscard: (job: ImageJob) => void }) {
  useEffect(() => { const previous = document.activeElement as HTMLElement | null; const key = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); }; document.addEventListener('keydown', key); return () => { document.removeEventListener('keydown', key); previous?.focus(); }; }, [onClose]);
  const groups = new Map<string, ImageJob[]>();
  for (const job of jobs) { const group = groups.get(job.batch.id) ?? []; group.push(job); groups.set(job.batch.id, group); }
  return <div className="sheet-wrap" onClick={onClose}><section className="sheet harvest" role="dialog" aria-modal="true" aria-label={tr('app_job_list')} tabIndex={-1} ref={(el) => el?.focus()} onClick={(e) => e.stopPropagation()}><header><strong>{tr('app_job_list')}</strong><div className="actions"><button className="ghost tiny" onClick={onClose}>{tr('app_close')}</button></div><div className="caption">{tr('app_job_list_caption', { count: jobs.filter((j) => j.output && !imageGone(j)).length, host: live.me.host.name, days: hostImages(live.me)!.retention_days })}</div></header><div className="body">
    {!jobs.length && <p className="empty-line">{tr('app_job_list_empty')}</p>}
    {[...groups.values()].map((group) => <section className="asking" key={group[0]!.batch.id}><div className="asking-head">{tr('app_job_group', { when: askingDate(group[0]!.created), count: group.length })}<button className="ghost tiny" onClick={() => onConversation(group[0]!)}>{conversationTitle(group[0]!)}</button></div>{group.sort((a,b) => a.batch.index - b.batch.index).map((job) => <ImageEntry key={job.id} {...{ job, live, connected, disabled, onEdit, onCancel, onDiscard }} pending={pending.has(job.id)} />)}</section>)}
  </div></section></div>;
}
function ImageEntry({ job, live, connected, disabled, pending, onEdit, onCancel, onDiscard }: { job: ImageJob; live: Live; connected: boolean; disabled: boolean; pending: boolean; onEdit: (text: string) => void; onCancel: (job: ImageJob) => void; onDiscard: (job: ImageJob) => void }) {
  const output = useOutput(job, live), [opened, setOpened] = useState(false), [error, setError] = useState('');
  const terminal = ['done','failed','cancelled'].includes(job.state), gone = imageGone(job) || output.gone;
  const save = () => { void saveImage(job, live).catch((e) => setError(String(e))); };
  return <div data-image-job={job.id} className={`output ${!output.url ? 'pending' : ''}`}>
    {output.url && <button className="shot" onClick={() => setOpened(true)}><img src={output.url} alt={job.input.prompt} /></button>}
    <p className="prompt">{job.input.prompt}</p>
    {job.output ? <div className="line">{gone ? tr('app_job_output_gone', { host: live.me.host.name }) : tr('app_job_output_line', { w: job.output.w, h: job.output.h, size: bytesLabel(job.output.bytes), elapsed: voiceTime(imageElapsed(job)), days: Math.max(0, Math.ceil((Date.parse(job.output.expiresAt) - Date.now()) / 86400000)) })}</div> : <p className="spoken">{!terminal && connected && <i className="live" />}{job.state === 'failed' ? tr('app_job_failed', { host: live.me.host.name }) : runLine(imageView(job))}{!terminal && !job.cancel_requested && <button className="ghost tiny" disabled={disabled || pending} onClick={() => onCancel(job)}>{tr('app_cancel')}</button>}</p>}
    {job.output && <div className="actions">{!gone && <button className="ghost tiny" onClick={save}>{tr('app_job_save')}</button>}<button className="ghost tiny" disabled={disabled} onClick={() => onEdit(job.input.prompt)}>{tr('app_job_edit_prompt')}</button>{!gone && <button className="ghost tiny" disabled={disabled || pending} onClick={() => onDiscard(job)}>{tr('app_job_discard')}</button>}</div>}
    {(error || output.error && !gone || job.reason) && <details className="host-said"><summary>{tr('app_details')}</summary><pre>{error || output.error || job.reason}</pre></details>}
    {opened && output.url && <div className="sheet-wrap" onClick={() => setOpened(false)}><figure className="sheet image" role="dialog" aria-modal="true" aria-label={job.input.prompt} onClick={(e) => e.stopPropagation()}><img src={output.url} alt={job.input.prompt} /><figcaption>{tr('app_run_output_image_caption', { w: job.output!.w, h: job.output!.h, size: bytesLabel(job.output!.bytes), host: live.me.host.name })}</figcaption><div className="sheet-actions"><button className="ghost tiny" onClick={save}>{tr('app_job_save')}</button><button className="ghost tiny" onClick={() => setOpened(false)}>{tr('app_close')}</button></div></figure></div>}
  </div>;
}

function askingDate(created: string): string {
  const date = new Date(created), today = new Date(), yesterday = new Date(); yesterday.setDate(today.getDate() - 1);
  const day = date.toDateString() === today.toDateString() ? 0 : date.toDateString() === yesterday.toDateString() ? -1 : null;
  return day === null ? date.toLocaleDateString(appLanguage(), { month: 'short', day: 'numeric' }) : `${new Intl.RelativeTimeFormat(appLanguage(), { numeric: 'auto' }).format(day, 'day')} ${date.toLocaleTimeString(appLanguage(), { hour: '2-digit', minute: '2-digit', hour12: false })}`;
}
