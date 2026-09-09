import { FILE_ACCEPT, extractFile, pastedFile, fileBlocks, fileLine, FileError, fileErrorMessage } from './files';
import { IMAGE_ACCEPT, ImageError, imageLine, prepareImage, MAX_IMAGES, type ImageData, type ImageMeta, type PreparedImage } from './images';
import type { ChatMessage } from './api';
import { tr } from './i18n/text';

/** Structural file contract; L3 supplies extraction and the file UI. */
export interface AttachedFile { name: string; kind: string; text: string; pages?: number; lines?: number; source: 'file' | 'paste' }
export type Attachment = { kind: 'image'; id: string; image: PreparedImage } | { kind: 'file'; id: string; file: AttachedFile };
export type AttachmentRef = { kind: 'image' | 'file'; index: number };
export type ClipboardData = Pick<DataTransfer, 'files' | 'getData'>;
export interface AdmissionOptions { vision?: boolean; model?: string; modelContext?: number; attachments?: readonly Attachment[]; draft?: string; storedBytes?: number }
export interface AttachmentTurn { content: string; images?: readonly ImageMeta[]; files?: readonly AttachedFile[] }

export function ACCEPT(vision: boolean): string { return [vision ? IMAGE_ACCEPT : '', FILE_ACCEPT].filter(Boolean).join(','); }
/** Returns only ready attachments; callers own cancellable reading rows, in input order. */
export async function admit(input: File[] | DataTransfer | ClipboardData, signal?: AbortSignal, options: AdmissionOptions = {}): Promise<Attachment[]> {
  const files = Array.isArray(input) ? input : Array.from(input.files);
  const attachments = [...(options.attachments ?? [])];
  const result: Attachment[] = [];
  const fileOptions = () => ({ signal, modelContext: options.modelContext, draft: options.draft,
    storedBytes: options.storedBytes, files: attachments.flatMap((a) => a.kind === 'file' ? [a.file] : []) });
  const addFile = async (read: () => AttachedFile | null | Promise<AttachedFile>) => {
    try {
      const file = await read();
      if (file) { const next: Attachment = { kind: 'file', id: crypto.randomUUID(), file }; attachments.push(next); result.push(next); }
    } catch (error) {
      if (error instanceof FileError) error.message = fileErrorMessage(error, options);
      throw error;
    }
  };
  if (!files.length && !Array.isArray(input)) await addFile(() => pastedFile(input.getData('text/plain'), fileOptions()));
  for (const file of files) {
    signal?.throwIfAborted();
    if (file.type.startsWith('image/')) {
      if (!options.vision) throw new ImageError('no_vision', tr('app_model_cant_see_images', { model: options.model ?? '' }));
      if (attachments.filter((a) => a.kind === 'image').length >= MAX_IMAGES) throw new ImageError('count', tr('app_image_limit'));
      try {
        const image = await prepareImage(file, signal);
        const next: Attachment = { kind: 'image', id: image.id, image };
        attachments.push(next); result.push(next);
      } catch (error) {
        if (signal?.aborted) throw signal.reason;
        throw new ImageError('cant_read', tr('app_could_not_read_that_image'), { cause: error });
      }
    } else {
      await addFile(() => extractFile(file, fileOptions()));
    }
  }
  return result;
}
export function sentText(m: AttachmentTurn): string { return [fileBlocks(m.files), m.content].filter(Boolean).join('\n\n'); }
export function sentContent(m: AttachmentTurn, images: ImageData = {}, text = sentText(m)): ChatMessage['content'] {
  const ready = (m.images ?? []).filter((i) => images[i.id]);
  return ready.length ? [...ready.map((i) => ({ type: 'image_url' as const, image_url: { url: images[i.id]! } })), { type: 'text', text }] : text;
}
export function attachedLine(m: Pick<AttachmentTurn, 'images' | 'files'>): string {
  return [m.images?.length ? imageLine(m.images) : '', fileLine(m.files)].filter(Boolean).join(' · ');
}
export type DisplayAttachment = { kind: 'image'; id: string; image: ImageMeta } | { kind: 'file'; id: string; file: AttachedFile };
export function turnAttachments(m: AttachmentTurn & { attachmentOrder?: readonly AttachmentRef[] }): DisplayAttachment[] {
  const order = m.attachmentOrder ?? [...(m.images ?? []).map((_, index) => ({ kind: 'image' as const, index })), ...(m.files ?? []).map((_, index) => ({ kind: 'file' as const, index }))];
  return order.flatMap((ref): DisplayAttachment[] => {
    const image = m.images?.[ref.index], file = m.files?.[ref.index];
    return ref.kind === 'image' ? (image ? [{ kind: 'image', id: image.id, image }] : []) : (file ? [{ kind: 'file', id: `file-${ref.index}`, file }] : []);
  });
}
export function attachmentFields(attachments: readonly DisplayAttachment[]): { images: ImageMeta[]; files: AttachedFile[]; attachmentOrder: AttachmentRef[] } {
  const images: ImageMeta[] = [], files: AttachedFile[] = [], attachmentOrder: AttachmentRef[] = [];
  for (const a of attachments) {
    const index = a.kind === 'image' ? images.length : files.length;
    attachmentOrder.push({ kind: a.kind, index });
    if (a.kind === 'image') { const { id, w, h, bytes } = a.image; images.push({ id, w, h, bytes }); } else files.push(a.file);
  }
  return { images, files, attachmentOrder };
}
/** Retained extracted text only; the edited turn and unsent attachments are outside this budget. */
export function retainedFileBytes(messages: readonly { id: string; files?: readonly AttachedFile[] }[], editingId?: string | null): number {
  return messages.reduce((sum, m) => sum + (m.id === editingId ? 0 : (m.files ?? []).reduce((n, f) => n + new TextEncoder().encode(f.text).length, 0)), 0);
}
export interface AttachmentNotice { message: string; danger: boolean }
export function rejectionNotice(error: unknown): AttachmentNotice {
  const e = error as { message?: unknown; reason?: unknown; danger?: unknown } | null;
  return { message: typeof e?.message === 'string' ? e.message : tr('app_could_not_read_that_image'), danger: e?.danger === true || ['count', 'storage', 'over_context', 'body_too_large'].includes(String(e?.reason)) };
}
