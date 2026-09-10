import { useEffect, useRef, useState } from 'react';
import { fileBlocks, measure, type AttachedFile } from '../files';
import { tr } from '../i18n/text';

/** Keep the extension and both ends of a long filename visible. */
export function chipName(name: string): string {
  return name.length <= 30 ? name : `${name.slice(0, 17)}…${name.slice(-12)}`;
}
export function FileChip({ file, reading = false, line, onRemove, onOpen }: {
  file: AttachedFile; reading?: boolean; line?: string; onRemove?: () => void; onOpen?: () => void;
}) {
  const label = <><span className="chip-name">{chipName(file.name)}</span><span className="chip-line">{
    reading ? tr('app_file_reading', { kind: file.kind }) : (line ?? measure(file))
  }</span></>;
  return onOpen && !onRemove
    ? <button className="chip" title={file.name} onClick={onOpen}>{label}</button>
    : <div className="chip" title={file.name} aria-busy={reading}>{label}{onRemove &&
      <button className="thumb-x" aria-label={`${tr('app_file_remove')}: ${file.name}`} onClick={onRemove}>×</button>
    }</div>;
}
export function FileSheet({ file, host, caption, raw = false, onClose }: { file: AttachedFile; host: string; caption?: string; raw?: boolean; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const ref = useRef<HTMLElement>(null);
  const block = raw ? file.text : fileBlocks([file]);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    ref.current?.focus();
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
      if (event.key === 'Tab') {
        // Copy is the sheet's only interactive child; keep focus within the dialog.
        event.preventDefault(); ref.current?.querySelector('button')?.focus();
      }
    };
    document.addEventListener('keydown', key);
    return () => { document.removeEventListener('keydown', key); previous?.focus(); };
  }, [onClose]);
  return <div className="sheet-wrap" onClick={onClose}>
    <section ref={ref} className="sheet file" role="dialog" aria-modal="true" aria-label={file.name} tabIndex={-1} onClick={(event) => event.stopPropagation()}>
      <header><strong>{file.name}</strong><span className="actions"><button className="ghost tiny" onClick={() => {
        void navigator.clipboard?.writeText(block).then(() => setCopied(true)).catch(() => setCopied(false));
      }}>{tr(copied ? 'app_copied' : 'app_copy')}</button></span>
        <span className="caption">{caption ?? tr('app_file_sent_caption', { measure: measure(file), host })}</span>
      </header>
      <pre>{block}</pre>
    </section>
  </div>;
}
