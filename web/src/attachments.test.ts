import { afterEach, expect, it, vi } from 'vitest';
import { retainedFileBytes, rejectionNotice, ACCEPT, admit, attachmentFields, attachedLine, sentContent, sentText, turnAttachments, type DisplayAttachment } from './attachments';
import { prepareImage } from './images';

const image = { id: 'image', w: 1024, h: 512, bytes: 1024 };
afterEach(() => vi.unstubAllGlobals());
it('admits readable files without vision and keeps only the image accept term conditional', async () => {
  expect(ACCEPT(false)).toContain('.pdf'); expect(ACCEPT(false)).not.toContain('image/*');
  expect(ACCEPT(true)).toContain('image/*');
  const files = await admit([new File(['A readable document with enough text.'], 'note.txt', { type: 'text/plain' })]);
  expect(files).toHaveLength(1); expect(files[0]?.kind).toBe('file');
  expect(await admit({ files: [] as unknown as FileList, getData: () => 'short paste' })).toEqual([]);
});
it('returns a typed no-vision refusal before reading', async () => {
  const read = vi.fn(); vi.stubGlobal('createImageBitmap', read);
  await expect(admit([new File(['bad'], 'x.png', { type: 'image/png' })], undefined, { vision: false, model: 'Text model' })).rejects.toMatchObject({ reason: 'no_vision' });
  expect(read).not.toHaveBeenCalled();
});
it('does not read pre-aborted inputs', async () => {
  const read = vi.fn(); vi.stubGlobal('createImageBitmap', read);
  const ac = new AbortController(); ac.abort();
  await expect(admit([new File(['bad'], 'x.png', { type: 'image/png' })], ac.signal, { vision: true })).rejects.toMatchObject({ name: 'AbortError' });
  expect(read).not.toHaveBeenCalled();
});
it('releases a bitmap that finished decoding after cancellation', async () => {
  const ac = new AbortController(), close = vi.fn();
  vi.stubGlobal('createImageBitmap', vi.fn(async () => { ac.abort(); return { width: 10, height: 10, close }; }));
  await expect(prepareImage(new Blob(['x']), ac.signal)).rejects.toMatchObject({ name: 'AbortError' });
  expect(close).toHaveBeenCalledOnce();
});
it('round trips mixed attach order while keeping JPEG bytes out of the record', () => {
  const items: DisplayAttachment[] = [
    { kind: 'file', id: 'file', file: { name: 'a.txt', kind: 'TXT', text: 'words', source: 'file' } },
    { kind: 'image', id: image.id, image: { ...image, data: 'not stored' } as typeof image },
  ];
  const fields = attachmentFields(items);
  expect(turnAttachments({ ...fields, content: '' }).map((a) => a.kind)).toEqual(['file', 'image']);
  expect(JSON.stringify(fields)).not.toContain('not stored');
  expect(fields.files[0]?.text).toBe('words');
});
it('uses one text part after available images and omits missing bytes', () => {
  const turn = { content: 'question', images: [image] };
  expect(sentText(turn)).toBe('question');
  expect(sentContent(turn)).toBe('question');
  expect(sentContent(turn, { image: 'data:image/jpeg;base64,AA==' })).toEqual([{ type: 'image_url', image_url: { url: 'data:image/jpeg;base64,AA==' } }, { type: 'text', text: 'question' }]);
  expect(attachedLine({})).toBe('');
});

it('counts UTF-8 extracted text only, excluding the edited turn', () => {
  const file = { name: 'a very long name.txt', kind: 'TXT', text: '你好', source: 'file' as const };
  const messages = [{ id: 'old', content: 'ignored words', files: [file] }, { id: 'editing', files: [{ ...file, text: 'omit me' }] }, { id: 'text-only', content: 'not file text' }];
  expect(retainedFileBytes(messages, 'editing')).toBe(6);
  expect(retainedFileBytes(messages)).toBe(13);
  expect(retainedFileBytes([])).toBe(0);
});
it('classifies either arm by typed severity regardless of translated copy', () => {
  for (const reason of ['count', 'storage', 'over_context', 'body_too_large']) {
    expect(rejectionNotice(Object.assign(new Error('任意文字'), { name: 'FileError', reason }))).toEqual({ message: '任意文字', danger: true });
  }
  expect(rejectionNotice({ message: 'custom', danger: true }).danger).toBe(true);
  expect(rejectionNotice({ message: 'Over the 4 MB one message can carry — remove an image.', reason: 'cant_read' }).danger).toBe(false);
});

it('puts file blocks before typed words and counts exactly that text in the context meter', async () => {
  const { contextCarried, estimateTokens } = await import('./stream');
  const parts = await admit([new File(['A readable document with enough text.'], 'note.txt')]);
  const turn = { id: 'turn', role: 'user' as const, content: 'Compare this rule.', ...attachmentFields(parts) };
  const text = sentText(turn);
  expect(text).toBe('[file: note.txt · TXT · 1 line]\nA readable document with enough text.\n[end of note.txt]\n\nCompare this rule.');
  expect(sentContent(turn)).toBe(text);
  expect(sentContent({ ...turn, images: [image] }, { image: 'data:image/jpeg;base64,fixture' })).toEqual([
    { type: 'image_url', image_url: { url: 'data:image/jpeg;base64,fixture' } }, { type: 'text', text },
  ]);
  expect(contextCarried([turn], { systemPrompt: '' }, 8192)).toBe(estimateTokens(text) + 4);
  expect(attachedLine(turn)).toBe('1 file · 10 tokens');
});
it('admits long paste as a file and preserves typed cap reasons with approved translated copy', async () => {
  const clipboard = { files: [] as unknown as FileList, getData: () => 'x'.repeat(4000) };
  expect((await admit(clipboard))[0]).toMatchObject({ kind: 'file', file: { source: 'paste', name: 'Pasted text' } });
  await expect(admit(clipboard, undefined, { modelContext: 1000, model: 'Gemma' })).rejects.toMatchObject({ name: 'FileError', reason: 'over_context', message: 'Over the 1.0k tokens Gemma holds — remove a file.' });
  await expect(admit(clipboard, undefined, { storedBytes: 512 * 1024 })).rejects.toMatchObject({ reason: 'storage', message: 'This chat holds 512 KiB of file text — start a new chat or remove a file.' });
  const ready = await admit([new File(['A readable document with enough text.'], 'note.txt')]);
  await expect(admit(clipboard, undefined, { attachments: Array.from({ length: 4 }, () => ready[0]!) })).rejects.toMatchObject({ reason: 'count', message: 'Up to 4 files per message — remove a file.' });
});
it('no-text hints distinguish models with and without vision', async () => {
  const scan = new File(['short'], 'scan.txt');
  await expect(admit([scan], undefined, { vision: true })).rejects.toMatchObject({ message: expect.stringContaining('Attach it as an image instead.') });
  await expect(admit([scan], undefined, { vision: false, model: 'Gemma' })).rejects.toMatchObject({ message: expect.stringContaining('Gemma can’t see images.') });
});
