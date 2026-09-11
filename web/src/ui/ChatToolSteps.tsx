import type { RunRecord } from '../api';
import { runView } from './RunItem';
import RunRow from './Run';
/** The streamed reply owns prose, errors and metering; this is only its tool trace. */
export default function ChatToolSteps({ record, host, connected }: { record: RunRecord; host: string; connected: boolean }) {
  const view = runView(record); view.steps = view.steps.filter(s => s.tool === 'make_image'); view.text = undefined;
  return view.steps.length ? <RunRow stepsOnly run={view} host={host} connected={connected} disabled pending={false} onCancel={() => {}} onAnswer={() => {}} onRetry={() => {}} /> : null;
}
