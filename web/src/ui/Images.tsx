import { useEffect, useState } from 'react';
import { FileChip, FileSheet } from './FileChip';
import { attachedLine, type DisplayAttachment } from '../attachments';
import { bytesLabel, type ImageData } from '../images';
import { tr } from '../i18n/text';

export interface ReadingAttachment { id: string; name: string; controller: AbortController }
export function AttachedImages({ attachments, data, onRemove, reading = [], onCancel }: { attachments: readonly DisplayAttachment[]; data: ImageData; onRemove: (id: string) => void; reading?: readonly ReadingAttachment[]; onCancel?: (id: string) => void }) {
  if (!attachments.length && !reading.length) return null;
  return <div className="attached">
    {attachments.map((a, n) => a.kind === 'file'
      ? <FileChip key={a.id} file={a.file} onRemove={() => onRemove(a.id)} />
      : <div className="thumb" key={a.id}>
        {data[a.image.id] && <img src={data[a.image.id]} alt={tr('app_image_n_of', { n: attachments.slice(0, n + 1).filter((a) => a.kind === 'image').length, count: attachments.filter((a) => a.kind === 'image').length })} />}
        <button className="thumb-x" aria-label={tr('app_remove_image')} onClick={() => onRemove(a.id)}>×</button>
      </div>)}
    {reading.map((r) => <div className="thumb attachment-reading" key={r.id} aria-busy="true"><span title={r.name}>{r.name}</span><span>{tr('app_attachment_reading')}</span><button className="thumb-x" aria-label={tr('app_cancel')} onClick={() => onCancel?.(r.id)}>×</button></div>)}
    {!reading.length && <span className="attached-line">{attachedLine({ images: attachments.flatMap((a) => a.kind === 'image' && data[a.image.id] ? [a.image] : []), files: attachments.flatMap((a) => a.kind === 'file' ? [a.file] : []) })}</span>}
  </div>;
}

export function ImageShots({ attachments, data, loaded, host }: { attachments: readonly DisplayAttachment[]; data: ImageData; loaded: boolean; host: string }) {
  const [opened, setOpened] = useState<DisplayAttachment | null>(null);
  useEffect(() => {
    if (!opened) return;
    const close = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpened(null); };
    document.addEventListener('keydown', close);
    return () => document.removeEventListener('keydown', close);
  }, [opened]);
  return <>
    <div className="shots">{attachments.map((a, n) => a.kind === 'file'
      ? <FileChip key={a.id} file={a.file} onOpen={() => setOpened(a)} />
      : data[a.image.id]
        ? <button className="shot" key={a.id} onClick={() => setOpened(a)}><img src={data[a.image.id]} alt={tr('app_image_n_of', { n: attachments.slice(0, n + 1).filter((a) => a.kind === 'image').length, count: attachments.filter((a) => a.kind === 'image').length })} /></button>
        : <div className="shot missing-image" key={a.id}><span>{a.image.w} × {a.image.h} · {bytesLabel(a.image.bytes)}</span>{loaded && <span>{tr('app_image_missing')}</span>}</div>)}</div>
    {opened?.kind === 'file' && <FileSheet file={opened.file} host={host} onClose={() => setOpened(null)} />}
    {opened?.kind === 'image' && data[opened.image.id] && <div className="sheet-wrap" onClick={() => setOpened(null)}>
      <figure className="sheet image" role="dialog" aria-modal="true" aria-label={tr('app_image_n_of', { n: attachments.slice(0, attachments.findIndex((a) => a.id === opened.id) + 1).filter((a) => a.kind === 'image').length, count: attachments.filter((a) => a.kind === 'image').length })} onClick={(e) => e.stopPropagation()}>
        <img src={data[opened.image.id]} alt={tr('app_image_n_of', { n: attachments.slice(0, attachments.findIndex((a) => a.id === opened.id) + 1).filter((a) => a.kind === 'image').length, count: attachments.filter((a) => a.kind === 'image').length })} />
        <figcaption>{tr('app_sent_image_caption', { ...opened.image, size: bytesLabel(opened.image.bytes), host })}</figcaption>
      </figure>
    </div>}
  </>;
}
