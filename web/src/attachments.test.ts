import { afterEach, expect, it, vi } from 'vitest';
import { retainedFileBytes, rejectionNotice, ACCEPT, admit, attachmentFields, attachedLine, sentContent, sentText, turnAttachments, type DisplayAttachment } from './attachments';
import { prepareImage } from './images';

const image = { id: 'image', w: 1024, h: 512, bytes: 1024 };
afterEach(() => vi.unstubAllGlobals());
it('keeps the file arm empty and the image accept list conditional', async () => {
  expect(ACCEPT(false)).toBe(''); expect(ACCEPT(true)).toBe('image/*');
  expect(await admit([new File(['text'], 'note.txt', { type: 'text/plain' })])).toEqual([]);
  expect(await admit({ files: [] as unknown as FileList, getData: () => 'long paste' })).toEqual([]);
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
