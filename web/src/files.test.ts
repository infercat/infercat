import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { acceptsFile, checkFile, extractFile, fileBlocks, fileLine, MAX_STORED_TEXT_BYTES, measure, pastedFile, storedFileBytes, type AttachedFile } from './files';

// Exercise the real parsers in Node; PDF.js uses its real worker as a fake worker here.
vi.mock('pdfjs-dist/legacy/build/pdf.worker.min.mjs?url', () => ({
  default: pathToFileURL(createRequire(import.meta.url).resolve('pdfjs-dist/legacy/build/pdf.worker.min.mjs')).href,
}));
vi.mock('mammoth', async (actual) => {
  const parser = await actual<typeof import('mammoth')>();
  return { extractRawText: ({ arrayBuffer }: { arrayBuffer: ArrayBuffer }) => parser.extractRawText({ buffer: Buffer.from(arrayBuffer) }) };
});
const fixture = (name: string) => new File([readFileSync(new URL(`../dev/fixtures/files/${name}`, import.meta.url))], name);
const text = 'The burst multiplier is two.';
const attached = (more: Partial<AttachedFile> = {}): AttachedFile => ({ name: 'notes.txt', kind: 'TXT', text, lines: 1, source: 'file', ...more });
beforeEach(() => vi.stubGlobal('navigator', { language: 'en' }));
afterEach(() => vi.unstubAllGlobals());

describe('real extraction fixtures', () => {
  it('reads PDF pages in order with a blank line between pages', async () => {
    const f = await extractFile(fixture('rate-limits.pdf'));
    expect(f.pages).toBe(2); expect(f.kind).toBe('PDF');
    expect(f.text).toBe('Rate limits for invited keys. The burst multiplier is two.\n\nA key may spend twice its limit. The source file implements that rule.');
  });
  it.each(['scan.pdf', 'encrypted.pdf', 'binary.txt', 'non-utf8.txt'])('refuses no-text fixture %s', async (name) => {
    await expect(extractFile(fixture(name))).rejects.toMatchObject({ reason: 'no_text', fileName: name });
  });
  it('reads DOCX raw paragraphs with no invented page or line count', async () => {
    const f = await extractFile(fixture('rate-limits.docx'));
    expect(f.text).toContain('The Word document says the burst multiplier is two.');
    expect(f).not.toHaveProperty('pages'); expect(f).not.toHaveProperty('lines');
  });
  it('preserves code and text indentation, normalizing only line ends', async () => {
    expect((await extractFile(fixture('limiter.ts'))).text).toBe(await fixture('limiter.ts').text());
    expect((await extractFile(fixture('notes.txt'))).text).toBe('The burst multiplier is two.\n  Preserve this indentation.\n');
  });
});

it('uses extension plus byte sniffing, never accepting unsupported containers or renamed binaries', async () => {
  expect(acceptsFile({ name: 'main.TSX', type: '' })).toBe(true);
  expect(acceptsFile({ name: 'README', type: 'text/plain' })).toBe(true);
  expect(acceptsFile({ name: 'sheet.xlsx', type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' })).toBe(false);
  // text/plain is advertised; byte validation still refuses a mislabeled binary container.
  await expect(extractFile(new File(['PK\u0003\u0004binary data that is not plain text'], 'sheet.xlsx', { type: 'text/plain' }))).rejects.toMatchObject({ reason: 'no_text' });
  await expect(extractFile(new File(['not a zip'], 'archive.zip'))).rejects.toMatchObject({ reason: 'cant_read' });
});
it('requires at least twenty non-whitespace characters', () => {
  expect(() => checkFile(attached({ text: 'a'.repeat(19) + ' '.repeat(1000) }))).toThrow();
  expect(checkFile(attached({ text: 'a'.repeat(20) })).text.length).toBe(20);
});
it('uses the 4000-character paste threshold without changing existing typed text', () => {
  expect(pastedFile('a'.repeat(3999))).toBeNull();
  const file = pastedFile('a'.repeat(4000));
  expect(file).toMatchObject({ source: 'paste', kind: 'TXT', name: 'Pasted text', lines: 1 });
});
it('enforces four files without mutating the existing list', () => {
  const files = Array.from({ length: 4 }, () => attached());
  expect(() => checkFile(attached(), { files })).toThrowError(expect.objectContaining({ reason: 'count' }));
  expect(files).toHaveLength(4);
});
it('enforces half-context per file and combined framed turn cap with the meter estimate', () => {
  expect(checkFile(attached({ text: 'a'.repeat(2000) }), { modelContext: 1000 }).text.length).toBe(2000);
  expect(() => checkFile(attached({ text: 'a'.repeat(2001) }), { modelContext: 1000 })).toThrowError(expect.objectContaining({ reason: 'over_context' }));
  expect(() => checkFile(attached(), { modelContext: 100, draft: 'd'.repeat(400) })).toThrowError(expect.objectContaining({ reason: 'over_context' }));
  expect(checkFile(attached({ text: 'x'.repeat(8000) }), { modelContext: 0 })).toBeTruthy();
});
it('caps stored UTF-8 text at 512 KiB, counts retained turns, and never trims', () => {
  const file = attached({ text: '字'.repeat(20) });
  expect(storedFileBytes([file])).toBe(60);
  expect(checkFile(file, { storedBytes: MAX_STORED_TEXT_BYTES - 60 })).toBe(file);
  expect(() => checkFile(file, { storedBytes: MAX_STORED_TEXT_BYTES - 59 })).toThrowError(expect.objectContaining({ reason: 'storage' }));
  expect(file.text).toBe('字'.repeat(20));
});
it('frames in attach order with the exact block the sheet shows, retaining whitespace and hyphenation', () => {
  const files = [attached({ name: 'a.pdf', kind: 'PDF', text: 're-\nported as it appeared', pages: 1, lines: undefined }), attached({ name: 'b.docx', kind: 'DOCX', lines: undefined })];
  expect(fileBlocks(files)).toBe('[file: a.pdf · PDF · 1 page]\nre-\nported as it appeared\n[end of a.pdf]\n\n[file: b.docx · DOCX]\nThe burst multiplier is two.\n[end of b.docx]');
  expect(fileBlocks()).toBe('');
  expect(fileBlocks([attached({ name: 'bad]\nname.txt' })])).not.toContain('bad]\nname');
});
it('derives measures in both languages, omitting DOCX extent and choosing singulars', () => {
  expect(measure(attached())).toBe('TXT · 1 line · 7 tokens');
  expect(measure(attached({ kind: 'DOCX', lines: undefined }))).toBe('DOCX · 7 tokens');
  expect(fileLine([attached()])).toBe('1 file · 7 tokens');
  vi.stubGlobal('navigator', { language: 'zh' });
  expect(measure(attached({ pages: 2 }))).toBe('TXT · 2 页 · 7 token');
  expect(fileLine([attached(), attached()])).toBe('2 个文件 · 14 token');
});
it('rejects cancellation before reading and while a read is pending', async () => {
  const ac = new AbortController(); ac.abort();
  await expect(extractFile(fixture('notes.txt'), { signal: ac.signal })).rejects.toMatchObject({ name: 'AbortError' });
  const file = fixture('notes.txt'); const reading = vi.spyOn(file, 'arrayBuffer').mockImplementation(() => new Promise(() => {}));
  const pending = new AbortController(); const result = extractFile(file, { signal: pending.signal }); pending.abort();
  await expect(result).rejects.toMatchObject({ name: 'AbortError' }); expect(reading).toHaveBeenCalledTimes(1);
});
