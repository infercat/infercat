import { runOutput, timeoutSignal, type RunRecord } from '../api';
import type { Live } from '../session';
import { useCallback } from 'react';
import { runView } from './RunItem';
import RunRow from './Run';
/** The streamed reply owns prose, errors and metering; this is only its tool trace. */
export default function ChatToolSteps({ record, host, connected, live }: { record: RunRecord; live: Live; host: string; connected: boolean }) {
  const loadText = useCallback((id: string) => runOutput(live.transport, live.secret, record.id, id, timeoutSignal(15000)).then(b => b.text()), [live.transport, live.secret, record.id]);
  const view = runView(record); view.steps = view.steps.filter(s => Boolean(s.tool)); view.text = undefined;
  return view.steps.length ? <RunRow loadText={loadText} stepsOnly run={view} host={host} connected={connected} disabled pending={false} onCancel={() => {}} onAnswer={() => {}} onRetry={() => {}} /> : null;
}
