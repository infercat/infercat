import { appLanguage, tr } from './i18n/text';

export const IMAGE_ACCEPT = 'image/*';
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
  const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' });
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
