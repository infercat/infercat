import { Fragment, useEffect, useState } from 'react';
import { tr } from '../i18n/text';
import { voiceTime } from '../voice';
import { type AttachedFile } from '../files';
import { FileChip, FileSheet } from './FileChip';
import { Thinking, CopyButton } from './Message';
import Markdown from './Markdown';
import { bytesLabel } from '../images';
import { rateText } from '../stream';
import RunDiff from './RunDiff';
import type { RunOutput } from '../api';

export interface RunStep {
  id: string;
  at: number;
  kind: 'think' | 'search' | 'run' | 'read' | 'write' | 'wait' | 'other';
  name?: string;
  tool?: string;
  result?: string;
  text?: string;
  complete?: boolean;
  output_id?: string;
}
export interface RunView {
  runKind?: 'image';
  cancelRequested?: boolean;
  outputGone?: boolean;
  id: string;
  state: 'queued' | 'running' | 'waiting' | 'done' | 'failed' | 'cancelled';
  elapsed: number;
  position?: number;
  steps: RunStep[];
  ask?: { id: string; text: string };
  outputs: AttachedFile[];
  images?: { name: string; url: string; size: number }[];
  text?: string;
  tokens?: { in: number; out: number };
  tokensPerSecond?: number;
  diffOutputs?: RunOutput[];
  error?: string;
  refused?: boolean;
}
export function stepName(step: RunStep): string {
  if (step.kind === 'wait' || step.kind === 'other') return step.name || step.tool || '';
  const values = { name: step.name ?? '', tool: step.tool ?? '' };
  return tr(({ think: 'app_run_step_think', search: 'app_run_step_search', run: 'app_run_step_run', read: 'app_run_step_read', write: 'app_run_step_write' } as const)[step.kind], values);
}
export function ordinal(n: number): string {
  return `${n}${n % 100 >= 11 && n % 100 <= 13 ? 'th' : ({ 1: 'st', 2: 'nd', 3: 'rd' } as Record<number, string>)[n % 10] ?? 'th'}`;
}
export function runLine(run: RunView): string {
  const elapsed = voiceTime(run.elapsed);
  if (run.runKind === 'image' && run.state === 'cancelled') return tr('app_job_cancelled_unstarted');
  if (run.runKind === 'image' && run.state === 'running' && run.cancelRequested) return tr('app_job_cancelling', { elapsed });
  if (run.state === 'waiting') return tr('app_run_waiting');
  if (run.state === 'cancelled') return tr('app_run_cancelled', { elapsed });
  if (run.state === 'queued') return run.position === 1 ? tr('app_run_queued_next') : run.position !== undefined ? tr('app_run_queued', { n: run.position, ordinal: ordinal(run.position) }) : tr('app_run_queued_unknown');
  const step = run.steps.at(-1);
  return step && stepName(step) ? tr('app_run_step', { step: stepName(step), elapsed }) : tr('app_run_generating', { elapsed });
}
export default function RunRow({ run, host, connected, disabled, pending, onCancel, onAnswer, onRetry, loadText, onSave, stepsOnly = false }: {
  stepsOnly?: boolean; run: RunView; host: string; connected: boolean; disabled: boolean; pending: boolean;
  onCancel: () => void; onAnswer: (id: string, allow: boolean) => void; onRetry: () => void; loadText?: (id: string) => Promise<string>; onSave?: (image: NonNullable<RunView['images']>[number]) => void;
}) {
  const [opened, setOpened] = useState<AttachedFile | null>(null);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [openedImage, setOpenedImage] = useState<NonNullable<RunView['images']>[number] | null>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  const [outputError, setOutputError] = useState('');
  useEffect(() => {
    if (!openedImage) return;
    const previous = document.activeElement as HTMLElement | null;
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') setOpenedImage(null); };
    document.addEventListener('keydown', key);
    return () => { document.removeEventListener('keydown', key); previous?.focus(); };
  }, [openedImage]);
  const terminal = ['done', 'failed', 'cancelled'].includes(run.state);
  const live = connected && !terminal && run.state !== 'waiting';
  const who = host || tr('app_the_host_lowercase');
  const current = run.steps.at(-1)?.id;
  return <div className="row assistant run" data-run-state={run.state}>
    {!stepsOnly && !['done', 'failed'].includes(run.state) && <div className="spoken">
      {live && <i className="live" aria-hidden="true" />}<span>{runLine(run)}</span>
      {!terminal && !(run.runKind === 'image' && run.cancelRequested) && <button className="ghost tiny" disabled={disabled || pending} onClick={onCancel}>{tr('app_cancel')}</button>}
    </div>}
    {!stepsOnly && run.state === 'failed' && <><p className="ended interrupted">{tr(run.runKind === 'image' ? 'app_job_failed' : run.refused ? 'app_run_refused' : 'app_run_failed', { host: who })}</p>
      {run.error && <details className="host-said"><summary>{tr('app_details')}</summary><pre>{run.error}</pre></details>}</>}
    {run.steps.length > 0 && <details className="host-said steps" onToggle={(e) => setDetailsOpen(e.currentTarget.open)}><summary>{tr(terminal ? (run.steps.length === 1 ? 'app_run_steps_one' : 'app_run_steps') : (run.steps.length === 1 ? 'app_run_steps_so_far_one' : 'app_run_steps_so_far'), { count: run.steps.length })}</summary>
      {run.steps.map((step) => <Fragment key={step.id}><div className={`step ${current === step.id && !step.complete && live ? 'now' : ''}`}>
        <span className="at">{voiceTime(step.at)}</span><span>
          {current === step.id && !step.complete && live && <i className="live" aria-hidden="true" />}
          {step.result !== undefined ? <button className="ghost r" onClick={() => { void (step.output_id && loadText ? loadText(step.output_id) : Promise.resolve(step.text ?? step.result!)).then((text) => setOpened({ name: stepName(step), kind: 'TXT', text, source: 'file', lines: text.split('\n').length })).catch((error) => setOutputError(String(error))); }}>{tr('app_run_step_result', { step: stepName(step), result: stepResult(step) })}</button> : stepName(step)}
        </span></div>{step.kind === 'think' && <RunThinking step={step} active={detailsOpen} loadText={loadText} />}{step.kind === 'write' && run.diffOutputs?.filter(o => o.id === step.output_id).map(output => <RunDiff key={`${run.id}:${output.id}`} output={output} active={detailsOpen} loadText={loadText} onOpen={setOpened} />)}</Fragment>)}
    </details>}
    {run.state === 'waiting' && run.ask && <><p className="waiting">{tr('app_run_ask')}</p><p className="ask">{run.ask.text}</p><div className="actions">
      <button className="ghost tiny" disabled={disabled || pending} onClick={() => onAnswer(run.ask!.id, true)}>{tr('app_run_allow')}</button>
      <button className="ghost tiny" disabled={disabled || pending} onClick={() => onAnswer(run.ask!.id, false)}>{tr('app_run_deny')}</button>
    </div></>}
    {run.outputGone && <div className="spoken">{tr('app_job_output_gone', { host: who })}</div>}
    {(run.outputs.length > 0 || (run.images?.length ?? 0) > 0) && <div className="shots">{run.outputs.map((file, n) => <FileChip key={`${n}:${file.name}`} file={file} line={`${file.kind} · ${tr('app_run_result_lines', { count: file.lines ?? file.text.split('\n').length })}`} onOpen={() => setOpened(file)} />)}{run.images?.map((image, n) => <button className="shot" key={image.url} onClick={() => { setSize({ w: 0, h: 0 }); setOpenedImage(image); }}><img src={image.url} alt={tr('app_run_output_n_of', { n: n + 1, count: run.images!.length, name: image.name })} /></button>)}</div>}
    {outputError && <details className="host-said"><summary>{tr('app_details')}</summary><pre>{outputError}</pre></details>}
    {openedImage && <div className="sheet-wrap" onClick={() => setOpenedImage(null)}><figure className="sheet image" role="dialog" aria-modal="true" aria-label={openedImage.name} tabIndex={-1} ref={(el) => el?.focus()} onClick={(e) => e.stopPropagation()}><img src={openedImage.url} alt={openedImage.name} onLoad={(e) => setSize({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })} />{size.w > 0 && <figcaption>{tr('app_run_output_image_caption', { ...size, size: bytesLabel(openedImage.size), host: who })}</figcaption>}{onSave && <div className="sheet-actions"><button className="ghost tiny" onClick={() => onSave(openedImage)}>{tr('app_job_save')}</button></div>}</figure></div>}
    {run.text && <Markdown text={run.text} />}
    {!stepsOnly && terminal && <div className="meta"><span className="meta-text">{run.runKind === 'image' ? tr(run.state === 'done' ? 'app_run_meta_images_one' : 'app_job_meta_failed', { host: who, elapsed: voiceTime(run.elapsed) }) : run.tokens ? tr('app_run_meta_agent', { host: who, elapsed: voiceTime(run.elapsed), input: run.tokens.in.toLocaleString('en-US'), output: run.tokens.out.toLocaleString('en-US') }) : `${voiceTime(run.elapsed)} · ${who}`}{run.runKind !== 'image' && run.tokensPerSecond !== undefined && Number.isFinite(run.tokensPerSecond) && run.tokensPerSecond >= 0 ? ` · ${rateText(run.tokensPerSecond)}` : ''}</span><span className="actions">
      {run.state === 'done' && run.runKind === 'image' && <button className="ghost tiny" disabled={disabled || pending} onClick={onRetry}>{tr('app_regenerate')}</button>}
      {run.state === 'done' && run.text && <CopyButton text={run.text} />}
      {run.state === 'failed' && <button className="ghost tiny" disabled={disabled || pending} onClick={onRetry}>{tr('app_try_again')}</button>}
    </span></div>}
    {opened && <FileSheet file={opened} host={who} raw caption={tr('app_run_output_caption', { kind: opened.kind, extent: tr('app_run_result_lines', { count: opened.lines ?? opened.text.split('\n').length }), host: who })} onClose={() => setOpened(null)} />}
  </div>;
}

/** Translate only the host's known measured preview grammar; diagnostics remain verbatim. */
export function stepResult(step: RunStep): string {
  const result = step.result ?? '';
  const count = /^(\d+) (results?|lines?)$/.exec(result);
  if (count && step.kind === 'search' && count[2]!.startsWith('result')) return tr('app_run_result_search', { count: count[1]! });
  if (count && ['read','write'].includes(step.kind) && count[2]!.startsWith('line')) return tr('app_run_result_lines', { count: count[1]! });
  const exit = /^exit (-?\d+) · (.*)$/.exec(result);
  if (step.kind === 'run' && exit) return tr('app_run_result_run', { code: exit[1]!, tail: exit[2]! });
  return result === 'failed' ? tr('app_run_result_failed') : result;
}

function RunThinking({ step, active, loadText }: { step: RunStep; active: boolean; loadText?: (id: string) => Promise<string> }) {
  const [loaded, setLoaded] = useState({ id: '', text: '', error: '' });
  useEffect(() => {
    if (!active || step.text !== undefined || !step.output_id || !loadText) return;
    let current = true; const id = step.output_id;
    void loadText(id).then((text) => { if (current) setLoaded({ id, text, error: '' }); }).catch((error) => { if (current) setLoaded({ id, text: '', error: String(error) }); });
    return () => { current = false; };
  }, [active, step.text, step.output_id, loadText]);
  const text = step.text ?? (loaded.id === step.output_id ? loaded.text : '');
  return text ? <Thinking text={text} answering /> : loaded.id === step.output_id && loaded.error ? <pre>{loaded.error}</pre> : null;
}
