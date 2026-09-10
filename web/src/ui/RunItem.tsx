import { useCallback, useEffect, useState } from 'react';
import { timeoutSignal, runOutput, type RunRecord } from '../api';
import type { Live } from '../session';
import type { RunItem } from '../storage';
import RunRow, { type RunView } from './Run';
import { tr } from '../i18n/text';

export function runView(record: RunRecord, now = Date.now()): RunView {
  const settled = record.attempts?.filter((a) => a.settled && !a.accounting_uncertain) ?? [];
  const measured = settled.length > 0 && settled.length === record.attempts.length && settled.every((a) => typeof a.usage.prompt_tokens === 'number' && typeof a.usage.completion_tokens === 'number');
  return {
    id: record.id, state: record.state, elapsed: Math.max(0, ((['done','failed','cancelled'].includes(record.state) ? Date.parse(record.updated) : now) - Date.parse(record.created)) / 1000), position: record.queue_position,
    steps: (record.steps ?? []).map((s) => ({ ...s, at: Math.max(0, (Date.parse(s.at) - Date.parse(record.created)) / 1000), complete: !['running', 'waiting'].includes(s.status) })),
    outputs: [], text: record.text, ask: record.approval?.status === 'pending' ? { id: record.approval.id, text: record.approval.request } : undefined,
    ...(measured ? { tokens: { in: settled.reduce((n, a) => n + a.usage.prompt_tokens!, 0), out: settled.reduce((n, a) => n + a.usage.completion_tokens!, 0) } } : {}), error: record.reason,
  };
}
export default function RunItemView({ item, live, connected, disabled, pending, onCancel, onAnswer, onRetry }: {
  item: RunItem; live: Live; connected: boolean; disabled: boolean; pending: boolean;
  onCancel: () => void; onAnswer: (id: string, allow: boolean) => void; onRetry: () => void;
}) {
  const [loaded, setLoaded] = useState<{ identity: string; files: RunView['outputs']; images: NonNullable<RunView['images']>; error?: string }>({ identity: '', files: [], images: [] });
  const [now, setNow] = useState(Date.now);
  const record = item.run, identity = JSON.stringify([record?.id, record?.outputs]);
  const recordId = record?.id ?? '', terminal = Boolean(record && ['done','failed','cancelled'].includes(record.state));
  useEffect(() => {
    if (!connected || terminal) return;
    const tick = () => setNow(Date.now()); tick();
    const timer = setInterval(tick, 1000); return () => clearInterval(timer);
  }, [connected, terminal, recordId]);
  useEffect(() => {
    const [runId, outputs] = JSON.parse(identity) as [string | null, RunRecord['outputs']];
    if (!runId || live.offline) return;
    const controller = new AbortController(), urls: string[] = [];
    void (async () => {
      const files: RunView['outputs'] = [], images: NonNullable<RunView['images']> = [];
      try {
        let total = 0;
        for (const output of outputs ?? []) {
          total += output.size; if (total > 4 * 1024 * 1024) throw new Error('Run outputs exceed the display bound');
          const blob = await runOutput(live.transport, live.secret, runId, output.id, controller.signal);
          if (controller.signal.aborted) return;
          if (/^image\/(png|jpeg|webp|gif)$/.test(output.mime)) { const url = URL.createObjectURL(blob); urls.push(url); images.push({ name: output.name, url, size: blob.size }); }
          else files.push({ name: output.name, kind: output.name.split('.').at(-1)?.toUpperCase() ?? 'TXT', source: 'file', text: await blob.text() });
        }
        if (!controller.signal.aborted) setLoaded({ identity, files: files.map((f) => ({ ...f, lines: f.text.replace(/\n$/, '').split('\n').length })), images });
      } catch (error) { if (!controller.signal.aborted) setLoaded({ identity, files, images, error: String(error) }); }
    })();
    return () => { controller.abort(); urls.forEach((url) => URL.revokeObjectURL(url)); };
  }, [identity, live.transport, live.secret, live.offline]);
  const loadText = useCallback((id: string) => runOutput(live.transport, live.secret, recordId, id, timeoutSignal(15000)).then((b) => b.text()), [live.transport, live.secret, recordId]);
  if (!record) return <div className="row assistant run"><p className={item.submission === 'refused' ? 'ended interrupted' : item.submission === 'pending' ? 'spoken' : 'waiting'}>{tr(item.submission === 'refused' ? 'app_run_refused' : item.submission === 'pending' ? 'app_run_submitting' : 'app_run_uncertain', { host: live.me.host.name })}</p>{item.details && <details className="host-said"><summary>{tr('app_details')}</summary><pre>{item.details}</pre></details>}{item.submission === 'refused' && <button className="ghost tiny" disabled={disabled || pending} onClick={onRetry}>{tr('app_try_again')}</button>}</div>;
  const view = runView(record, now);
  if (loaded.identity === identity) { view.outputs = loaded.files; view.images = loaded.images; }
  return <><RunRow run={view} host={live.me.host.name} connected={connected} disabled={disabled} pending={pending || record.cancel_requested} onCancel={onCancel} onAnswer={onAnswer} onRetry={onRetry}
    loadText={loadText} />
    {item.details && <details className="host-said"><summary>{tr('app_details')}</summary><pre>{item.details}</pre></details>}
    {loaded.identity === identity && loaded.error && <details className="host-said"><summary>{tr('app_details')}</summary><pre>{loaded.error}</pre></details>}</>;
}
