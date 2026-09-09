import { useEffect, useState } from 'react';
import { bytesLabel, imageLine, type ImageData, type ImageMeta } from '../images';
import { tr } from '../i18n/text';

export function AttachedImages({ images, data, onRemove }: { images: readonly ImageMeta[]; data: ImageData; onRemove: (id: string) => void }) {
  if (!images.length) return null;
  return <div className="attached">
    {images.map((i, n) => <div className="thumb" key={i.id}>
      {data[i.id] && <img src={data[i.id]} alt={tr('app_image_n_of', { n: n + 1, count: images.length })} />}
      <button className="thumb-x" aria-label={tr('app_remove_image')} onClick={() => onRemove(i.id)}>×</button>
    </div>)}
    <span className="attached-line">{imageLine(images.filter((i) => data[i.id]))}</span>
  </div>;
}

export function ImageShots({ images, data, loaded, host }: { images: readonly ImageMeta[]; data: ImageData; loaded: boolean; host: string }) {
  const [opened, setOpened] = useState<ImageMeta | null>(null);
  useEffect(() => {
    if (!opened) return;
    const close = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpened(null); };
    document.addEventListener('keydown', close);
    return () => document.removeEventListener('keydown', close);
  }, [opened]);
  return <>
    <div className="shots">{images.map((i, n) => data[i.id]
      ? <button className="shot" key={i.id} onClick={() => setOpened(i)}><img src={data[i.id]} alt={tr('app_image_n_of', { n: n + 1, count: images.length })} /></button>
      : <div className="shot missing-image" key={i.id}><span>{i.w} × {i.h} · {bytesLabel(i.bytes)}</span>{loaded && <span>{tr('app_image_missing')}</span>}</div>)}</div>
    {opened && data[opened.id] && <div className="sheet-wrap" onClick={() => setOpened(null)}>
      <figure className="sheet image" role="dialog" aria-modal="true" aria-label={tr('app_image_n_of', { n: images.indexOf(opened) + 1, count: images.length })} onClick={(e) => e.stopPropagation()}>
        <img src={data[opened.id]} alt={tr('app_image_n_of', { n: images.indexOf(opened) + 1, count: images.length })} />
        <figcaption>{tr('app_sent_image_caption', { ...opened, size: bytesLabel(opened.bytes), host })}</figcaption>
      </figure>
    </div>}
  </>;
}
