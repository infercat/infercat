// Standalone component harness; the real composer wiring lands after 073.
import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { FileChip, FileSheet } from '../src/ui/FileChip';
import { extractFile, fileLine, type AttachedFile } from '../src/files';
import { KEYS } from '../src/storage';
import '../src/styles.css';
const language = new URLSearchParams(location.search).get('lang') === 'zh' ? 'zh' : 'en';
localStorage.setItem(KEYS.language, JSON.stringify(language));
const pdf: AttachedFile = { name: 'rate-limits.pdf', kind: 'PDF', pages: 6, text: 'Rate limits for invited keys\nRevision 3 · 2026-08-30\n\n1. Scope\nEvery invite carries three limits: messages per minute, tokens per day, and concurrent requests. They are enforced on the host, before a request reaches the engine, and re-\nported on /me so a client can show them.\n\n2. Burst\nA key may spend up to twice its per-minute rate within any ten-second window.', source: 'file' };
const code: AttachedFile = { name: 'limiter.ts', kind: 'TS', lines: 4, text: 'export const BURST_MULTIPLIER = 2;\nexport function canSpend(tokens: number, limit: number) {\n  return tokens <= limit * BURST_MULTIPLIER;\n}', source: 'file' };
function Preview() {
  const [files, setFiles] = useState([pdf, code]);
  const [opened, setOpened] = useState<AttachedFile | null>(null);
  const [reading, setReading] = useState(false);
  const [error, setError] = useState('');
  return <div className={`app ${language}`} style={{ display: 'block', height: 'auto', padding: 20 }}>
    <h1>File chip verification</h1>
    <input aria-label="Extract fixture" type="file" onChange={(event) => {
      const file = event.currentTarget.files?.[0]; if (!file) return;
      setReading(true); setError('');
      void extractFile(file).then((value) => setFiles([value])).catch((err: Error) => setError(err.message)).finally(() => setReading(false));
    }} />
    <p role="status">{reading ? 'reading…' : error}</p>
    <div className="composer"><div className="attached" style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
      {files.map((file, i) => <FileChip key={i} file={file} onRemove={() => setFiles((current) => current.filter((_, at) => at !== i))} />)}
      <span className="attached-line">{fileLine(files)}</span>
    </div></div>
    <div className="row user"><div className="bubble"><div className="shots" style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
      {files.map((file, i) => <FileChip key={i} file={file} onOpen={() => setOpened(file)} />)}
    </div>Does the code do what the document promises?</div></div>
    {opened && <FileSheet file={opened} host="Max’s workstation" onClose={() => setOpened(null)} />}
  </div>;
}
createRoot(document.getElementById('root')!).render(<Preview />);
