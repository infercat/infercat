// Documents become text on this device; only these words are persisted or sent.
import { tr } from './i18n/text';
import { estimateTokens } from './stream';
import { compact } from './session';

export interface AttachedFile {
  name: string;
  kind: string;
  text: string;
  pages?: number;
  lines?: number;
  source: 'file' | 'paste';
}
export interface FileOptions {
  signal?: AbortSignal;
  modelContext?: number;
  files?: readonly AttachedFile[];
  draft?: string;
  /** Extracted UTF-8 bytes in other retained turns; excludes `files` above. */
  storedBytes?: number;
}
export type FileReason = 'no_text' | 'cant_read' | 'over_context' | 'count' | 'storage';
export class FileError extends Error {
  constructor(readonly reason: FileReason, readonly fileName: string) {
    super(reason); this.name = 'FileError';
  }
}
const TEXT_EXTENSIONS = 'txt md markdown csv tsv json jsonl yaml yml toml ini cfg log js jsx ts tsx mjs cjs py go rs rb java c cpp h hpp cs swift kt kts sh bash zsh sql css scss html xml vue svelte r tex'.split(' ');
const EXTENSIONS = ['pdf', 'docx', ...TEXT_EXTENSIONS];
export const FILE_ACCEPT = ['text/plain', ...EXTENSIONS.map((ext) => `.${ext}`)].join(',');
export const MAX_FILES = 4;
export const MAX_STORED_TEXT_BYTES = 512 * 1024;
export const PASTE_THRESHOLD = 4000;
const encoder = new TextEncoder();
const extension = (name: string) => name.split('.').length > 1 ? name.split('.').at(-1)!.toLowerCase() : '';

export function acceptsFile(file: Pick<File, 'name' | 'type'>): boolean {
  const ext = extension(file.name);
  return EXTENSIONS.includes(ext) || file.type === 'text/plain';
}
export function storedFileBytes(files: readonly AttachedFile[] = []): number {
  return files.reduce((sum, file) => sum + encoder.encode(file.text).length, 0);
}
/** Line-end normalization only: indentation, blank lines and PDF hyphenation remain evidence. */
function normalized(text: string): string {
  return text.replace(/\r\n?/g, '\n').replace(/[ \t]+(?=\n|$)/g, '');
}
function safeName(name: string): string {
  return [...name].map((c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127 || c === '[' || c === ']' ? ' ' : c).join('');
}
export function fileBlocks(files: readonly AttachedFile[] = []): string {
  return files.map((file) => {
    const extent = file.pages !== undefined ? ` · ${file.pages} ${file.pages === 1 ? 'page' : 'pages'}`
      : file.lines !== undefined ? ` · ${file.lines} ${file.lines === 1 ? 'line' : 'lines'}` : '';
    return `[file: ${safeName(file.name)} · ${file.kind}${extent}]\n${file.text}\n[end of ${safeName(file.name)}]`;
  }).join('\n\n');
}
export function measure(file: AttachedFile): string {
  const extent = file.pages !== undefined
    ? tr(file.pages === 1 ? 'app_file_page' : 'app_file_pages', { count: file.pages })
    : file.lines !== undefined ? tr(file.lines === 1 ? 'app_file_line' : 'app_file_lines', { count: file.lines }) : '';
  return tr('app_file_chip_line', { kind: file.kind, extent: extent ? `${extent} · ` : '', tokens: compact(estimateTokens(file.text)) });
}
export function fileLine(files: readonly AttachedFile[] = []): string {
  return files.length ? tr(files.length === 1 ? 'app_file_single_line' : 'app_files_line', {
    count: files.length, tokens: compact(files.reduce((n, file) => n + estimateTokens(file.text), 0)),
  }) : '';
}
/** Validate a proposed addition; existing attachments and stored turns are never changed. */
export function checkFile(file: AttachedFile, opts: FileOptions = {}): AttachedFile {
  opts.signal?.throwIfAborted();
  const files = opts.files ?? [];
  if (files.length >= MAX_FILES) throw new FileError('count', file.name);
  if (file.text.replace(/\s/g, '').length < 20) throw new FileError('no_text', file.name);
  const context = opts.modelContext ?? 0;
  const text = [fileBlocks([...files, file]), opts.draft ?? ''].filter(Boolean).join('\n\n');
  if (context > 0 && (estimateTokens(file.text) > Math.floor(context / 2) || estimateTokens(text) + 4 >= context)) {
    throw new FileError('over_context', file.name);
  }
  if ((opts.storedBytes ?? 0) + storedFileBytes([...files, file]) > MAX_STORED_TEXT_BYTES) {
    throw new FileError('storage', file.name);
  }
  return file;
}
export function pastedFile(text: string, opts: FileOptions = {}): AttachedFile | null {
  if (text.length < PASTE_THRESHOLD) return null;
  const clean = normalized(text);
  return checkFile({ name: tr('app_file_pasted_text'), kind: 'TXT', text: clean, lines: clean.split('\n').length, source: 'paste' }, opts);
}
export async function extractFile(file: File, opts: FileOptions = {}): Promise<AttachedFile> {
  opts.signal?.throwIfAborted();
  if (!acceptsFile(file)) throw new FileError('cant_read', file.name);
  if ((opts.files?.length ?? 0) >= MAX_FILES) throw new FileError('count', file.name);
  const ext = extension(file.name);
  const kind = (EXTENSIONS.includes(ext) ? ext : 'txt').toUpperCase();
  try {
    const data = await abortable(file.arrayBuffer(), opts.signal);
    let text: string, pages: number | undefined;
    if (kind === 'PDF') {
      ({ text, pages } = await pdfText(data, opts));
    } else if (kind === 'DOCX') {
      const mammoth = await abortable(import('mammoth'), opts.signal);
      text = (await abortable(mammoth.extractRawText({ arrayBuffer: data }), opts.signal)).value;
    } else {
      text = new TextDecoder('utf-8', { fatal: true }).decode(data);
      // A renamed binary may be valid UTF-8; control bytes still make it a binary.
      for (const c of text) {
        if (c.charCodeAt(0) < 32 && c !== '\t' && c !== '\n' && c !== '\r') throw new FileError('no_text', file.name);
      }
    }
    text = normalized(text);
    return checkFile({ name: safeName(file.name), kind, text, source: 'file',
      ...(pages !== undefined ? { pages } : {}),
      ...(kind !== 'PDF' && kind !== 'DOCX' ? { lines: text.split('\n').length } : {}),
    }, opts);
  } catch (error) {
    opts.signal?.throwIfAborted();
    if (error instanceof FileError) throw new FileError(error.reason, file.name);
    // Encrypted/corrupt PDFs and decode failures cannot supply readable text.
    throw new FileError('no_text', file.name);
  }
}
async function pdfText(data: ArrayBuffer, opts: FileOptions): Promise<{ text: string; pages: number }> {
  const pdf = await abortable(import('pdfjs-dist/legacy/build/pdf.mjs'), opts.signal);
  const worker = await abortable(import('pdfjs-dist/legacy/build/pdf.worker.min.mjs?url'), opts.signal);
  pdf.GlobalWorkerOptions.workerSrc = worker.default;
  const task = pdf.getDocument({ data, useWasm: false, disableFontFace: true });
  const stop = () => { void task.destroy().catch(() => {}); };
  opts.signal?.addEventListener('abort', stop, { once: true });
  try {
    const doc = await abortable(task.promise, opts.signal);
    const pages: string[] = [];
    for (let n = 1; n <= doc.numPages; n++) {
      opts.signal?.throwIfAborted();
      const page = await abortable(doc.getPage(n), opts.signal);
      const content = await abortable(page.getTextContent({ disableNormalization: true }), opts.signal);
      pages.push(content.items.map((item) => 'str' in item ? item.str : '').join(' '));
      page.cleanup();
      // Stop before reading more pages once this document cannot be admitted.
      const text = normalized(pages.join('\n\n'));
      if (opts.modelContext && estimateTokens(text) > Math.floor(opts.modelContext / 2)) throw new FileError('over_context', '');
      if ((opts.storedBytes ?? 0) + storedFileBytes(opts.files) + encoder.encode(text).length > MAX_STORED_TEXT_BYTES) throw new FileError('storage', '');
    }
    return { text: pages.join('\n\n'), pages: doc.numPages };
  } finally {
    opts.signal?.removeEventListener('abort', stop);
    await task.destroy();
  }
}
function abortable<T>(promise: Promise<T>, signal?: AbortSignal): Promise<T> {
  if (!signal) return promise;
  if (signal.aborted) { void promise.catch(() => {}); return Promise.reject(signal.reason); }
  return new Promise<T>((resolve, reject) => {
    const stop = () => reject(signal.reason);
    signal.addEventListener('abort', stop, { once: true });
    promise.then(resolve, reject).finally(() => signal.removeEventListener('abort', stop));
  });
}

/** The seam keeps FileError's typed reason so cap refusals reach the shared danger line. */
export function fileErrorMessage(error: FileError, options: { vision?: boolean; model?: string; modelContext?: number }): string {
  if (error.reason === 'count') return tr('app_file_count');
  if (error.reason === 'storage') return tr('app_file_storage');
  if (error.reason === 'over_context') return tr('app_file_over_context', { context: compact(options.modelContext ?? 0), model: options.model ?? '' });
  if (error.reason === 'cant_read') return tr('app_file_cant_read', { name: error.fileName });
  return tr(options.vision ? 'app_file_no_text' : 'app_file_no_text_no_vision', { name: error.fileName, model: options.model ?? '' });
}
