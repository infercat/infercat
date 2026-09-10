import { appLanguage, tr } from './i18n/text';

/** One declaration table for the picker and every admission path; decoding is still required. */
export const IMAGE_KINDS = [
  { name: 'JPEG', mime: 'image/jpeg', extensions: ['jpg', 'jpeg', 'jpe'] },
  { name: 'PNG', mime: 'image/png', extensions: ['png'] },
  { name: 'WebP', mime: 'image/webp', extensions: ['webp'] },
  { name: 'GIF', mime: 'image/gif', extensions: ['gif'] },
  { name: 'AVIF', mime: 'image/avif', extensions: ['avif'] },
  { name: 'BMP', mime: 'image/bmp', extensions: ['bmp'] },
  { name: 'ICO', mime: 'image/x-icon', extensions: ['ico'], aliases: ['image/vnd.microsoft.icon'] },
  { name: 'SVG', mime: 'image/svg+xml', extensions: ['svg'] },
] as const;
export const IMAGE_ACCEPT = IMAGE_KINDS.flatMap((kind) => [kind.mime, ...('aliases' in kind ? kind.aliases : []), ...kind.extensions.map((ext) => `.${ext}`)]).join(',');
export function imageKind(file: { name?: string; type: string }) {
  const ext = file.name?.includes('.') ? file.name.split('.').at(-1)?.toLowerCase() : undefined;
  return IMAGE_KINDS.find((kind) => kind.extensions.some((value) => value === ext))
    ?? IMAGE_KINDS.find((kind) => kind.mime === file.type || ('aliases' in kind && kind.aliases.some((value) => value === file.type)));
}
export function imageKindLabel(file: { name?: string; type: string }): string {
  return imageKind(file)?.name ?? file.type.replace(/^image\//, '').toUpperCase();
}
export const MAX_SVG_BYTES = 4 * 1024 * 1024;
export class ImageError extends Error {
  constructor(readonly reason: 'no_vision' | 'count' | 'cant_read', message: string, options?: ErrorOptions) { super(message, options); this.name = 'ImageError'; }
}
export const MAX_IMAGES = 4;
export const MAX_REQUEST_BYTES = 4 * 1024 * 1024;
export interface ImageMeta { id: string; w: number; h: number; bytes: number }
export interface PreparedImage extends ImageMeta { blob: Blob; data: string }
export type ImageData = Readonly<Record<string, string>>;

export function imageSize(w: number, h: number): { w: number; h: number } {
  const scale = Math.min(1, 1024 / Math.max(w, h));
  return { w: Math.max(1, Math.round(w * scale)), h: Math.max(1, Math.round(h * scale)) };
}
export function dataURL(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
}
/** Browser decoding applies EXIF exactly once; canvas flattens alpha before JPEG encoding. */
export async function prepareImage(file: Blob, signal?: AbortSignal): Promise<PreparedImage> {
  signal?.throwIfAborted();
  const kind = imageKind(file);
  if (kind && kind.name !== 'SVG') await checkRasterHeader(file, kind.name);
  signal?.throwIfAborted();
  const bitmap = kind?.name === 'SVG' ? await svgImage(file, signal) : await createImageBitmap(file, { imageOrientation: 'from-image' });
  try {
    signal?.throwIfAborted();
    const { w, h } = imageSize(bitmap.width, bitmap.height);
    const canvas = document.createElement('canvas');
    canvas.width = w; canvas.height = h;
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('Canvas unavailable');
    ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, w, h);
    ctx.drawImage(bitmap, 0, 0, w, h);
    const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob((b) => b ? resolve(b) : reject(new Error('JPEG encoding failed')), 'image/jpeg', 0.85));
    signal?.throwIfAborted();
    return { id: crypto.randomUUID(), w, h, bytes: blob.size, blob, data: await dataURL(blob) };
  } finally { bitmap.close(); }
}
/** Do not let browser sniffing admit an unadvertised format under a supported filename. */
async function checkRasterHeader(file: Blob, kind: string): Promise<void> {
  const bytes = new Uint8Array(await file.slice(0, 32).arrayBuffer());
  const starts = (...values: number[]) => values.every((n, i) => bytes[i] === n);
  const ascii = String.fromCharCode(...bytes);
  const valid = kind === 'JPEG' ? starts(255, 216, 255)
    : kind === 'PNG' ? starts(137, 80, 78, 71, 13, 10, 26, 10)
    : kind === 'GIF' ? /^GIF8[79]a/.test(ascii)
    : kind === 'WebP' ? ascii.startsWith('RIFF') && ascii.slice(8, 12) === 'WEBP'
    : kind === 'BMP' ? ascii.startsWith('BM')
    : kind === 'ICO' ? starts(0, 0, 1, 0)
    : kind === 'AVIF' && ascii.slice(4, 8) === 'ftyp' && /avif|avis/.test(ascii.slice(8));
  if (!valid) throw new Error('Image bytes do not match the declared kind');
}
/** SVG stays in image mode: no document insertion, scripts or external resource loads.
 * Animation is flattened at load into one static JPEG; no animation is retained. */
async function svgImage(file: Blob, signal?: AbortSignal): Promise<HTMLImageElement & { close(): void }> {
  if (file.size > MAX_SVG_BYTES) throw new Error('SVG exceeds 4 MiB');
  const doc = new DOMParser().parseFromString(await file.text(), 'image/svg+xml');
  signal?.throwIfAborted();
  const root = doc.documentElement;
  if (doc.querySelector('parsererror') || root.localName !== 'svg' || root.namespaceURI !== 'http://www.w3.org/2000/svg') throw new Error('Invalid SVG');
  const box = (root.getAttribute('viewBox') ?? '').trim().split(/[\s,]+/).map(Number);
  const hasBox = box.length === 4 && box.every(Number.isFinite) && box[2]! > 0 && box[3]! > 0;
  // Give dimensionless SVGs an intrinsic viewport; viewBox preserves their aspect ratio.
  if (!root.hasAttribute('width') && !root.hasAttribute('height')) {
    root.setAttribute('width', String(hasBox ? box[2] : 300));
    root.setAttribute('height', String(hasBox ? box[3] : 150));
  }
  const url = URL.createObjectURL(new Blob([new XMLSerializer().serializeToString(root)], { type: 'image/svg+xml' }));
  const img = new Image();
  const close = () => { img.src = ''; URL.revokeObjectURL(url); };
  try {
    await new Promise<void>((resolve, reject) => {
      const stop = () => { cleanup(); reject(signal?.reason); };
      const cleanup = () => { img.onload = null; img.onerror = null; signal?.removeEventListener('abort', stop); };
      img.onload = () => { cleanup(); resolve(); };
      img.onerror = () => { cleanup(); reject(new Error('SVG decoding failed')); };
      signal?.addEventListener('abort', stop, { once: true });
      img.src = url;
    });
    signal?.throwIfAborted();
    if (!img.naturalWidth || !img.naturalHeight) throw new Error('SVG has no drawable size');
    return Object.assign(img, { close });
  } catch (error) { close(); throw error; }
}
export function imageMeta({ id, w, h, bytes }: ImageMeta): ImageMeta { return { id, w, h, bytes }; }
export function bytesLabel(bytes: number): string { return `${Math.max(1, Math.round(bytes / 1024))} KB`; }
export function imageLine(images: readonly ImageMeta[]): string {
  return imageCopy('app_images_line', images.length, { px: Math.max(0, ...images.map((i) => Math.max(i.w, i.h))), size: bytesLabel(images.reduce((n, i) => n + i.bytes, 0)) });
}
export function imageCopy(key: Parameters<typeof tr>[0], count: number, values: Record<string, string | number> = {}): string {
  const text = tr(key, { ...values, count });
  return count === 1 && appLanguage() === 'en' ? text.replace(/\b1 images\b/g, '1 image') : text;
}
export function fitsRequest(request: unknown): boolean {
  // chatEvents adds stream before serializing the request.
  return new TextEncoder().encode(JSON.stringify({ ...(request as object), stream: true })).length <= MAX_REQUEST_BYTES;
}
