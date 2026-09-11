import { useEffect, useState } from 'react';
import type { RunOutput } from '../api';
import type { AttachedFile } from '../files';
import { tr } from '../i18n/text';
import { CopyButton } from './Message';

export const DIFF_PREVIEW_LINES = 12;
export function isDiffOutput(output: RunOutput): boolean {
  return output?.kind === 'diff' && typeof output.mime === 'string' && output.mime.split(';')[0]?.trim() === 'text/x-diff';
}
/** Linear scan; only the twelve preview rows become objects/DOM nodes. */
export function diffPreview(text: string) {
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  if (lines.at(-1) === '') lines.pop();
  const preview: { text: string; kind: 'add' | 'remove' | 'context' }[] = [];
  let added = 0, removed = 0, oldLeft = 0, newLeft = 0;
  for (const line of lines) {
    let kind: 'add' | 'remove' | 'context' = 'context';
    const hunk = /^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@/.exec(line);
    if (hunk) { oldLeft = Number(hunk[1] ?? 1); newLeft = Number(hunk[2] ?? 1); }
    else if (oldLeft > 0 || newLeft > 0) {
      if (line.startsWith('+') && newLeft > 0) { kind = 'add'; added++; newLeft--; }
      else if (line.startsWith('-') && oldLeft > 0) { kind = 'remove'; removed++; oldLeft--; }
      else if (line.startsWith(' ')) { oldLeft--; newLeft--; }
    }
    if (preview.length < DIFF_PREVIEW_LINES) preview.push({ text: line, kind });
  }
  return { preview, added, removed, total: lines.length };
}
export default function RunDiff({ output, active, loadText, onOpen }: {
  output: RunOutput; active: boolean; loadText?: (id: string) => Promise<string>; onOpen: (file: AttachedFile) => void;
}) {
  const [loaded, setLoaded] = useState<{ text?: string; error?: string }>({});
  useEffect(() => {
    if (!active || !isDiffOutput(output) || !loadText || loaded.text !== undefined || loaded.error) return;
    let current = true;
    void loadText(output.id).then(text => { if (current) setLoaded({ text }); }).catch(error => { if (current) setLoaded({ error: String(error) }); });
    return () => { current = false; };
  }, [active, output.id, output.kind, output.mime, loadText, loaded.text, loaded.error]);
  if (!isDiffOutput(output)) return null;
  if (loaded.text === undefined) return active ? <p className="run-diff-status">{loaded.error ?? tr('app_file_reading', { kind: 'DIFF' })}</p> : null;
  const diff = diffPreview(loaded.text);
  return <section className="run-diff" aria-label={tr('app_run_diff')}>
    <header><span>{tr('app_run_diff_counts', { added: diff.added, removed: diff.removed })}</span><CopyButton text={loaded.text} /></header>
    <pre>{diff.preview.map((line, n) => <span key={n} className={`diff-line ${line.kind}`}>{line.text || ' '}</span>)}</pre>
    {diff.total > DIFF_PREVIEW_LINES && <button className="ghost tiny" onClick={() => onOpen({ name: output.name, kind: 'DIFF', source: 'file', text: loaded.text!, lines: diff.total })}>{tr('app_run_diff_more', { shown: DIFF_PREVIEW_LINES, total: diff.total })}</button>}
  </section>;
}
