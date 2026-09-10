import { afterEach, describe, expect, it, vi } from 'vitest';
import { fitsRequest, imageLine, imageMeta, imageSize, MAX_REQUEST_BYTES, prepareImage } from './images';
import { carried, carriedImageCount, contextCarried } from './stream';
import { evictedTurns, IMAGE_STORE_BYTES } from './image-store';
import type { Message } from './storage';

const meta = { id: 'photo', w: 1024, h: 512, bytes: 120000 };
afterEach(() => vi.unstubAllGlobals());
describe('image conversion', () => {
  it('fits landscape/portrait without upscaling or zero-sized edges', () => {
    expect(imageSize(4000, 2000)).toEqual({ w: 1024, h: 512 });
    expect(imageSize(2000, 4000)).toEqual({ w: 512, h: 1024 });
    expect(imageSize(64, 80)).toEqual({ w: 64, h: 80 });
    expect(imageSize(1, 10000)).toEqual({ w: 1, h: 1024 });
  });
  it('uses EXIF-aware decoding, flattens alpha, measures encoded JPEG, and closes the bitmap', async () => {
    const close = vi.fn(); const decode = vi.fn().mockResolvedValue({ width: 2000, height: 4000, close });
    const fillRect = vi.fn(); const drawImage = vi.fn(); const ctx = { fillStyle: '', fillRect, drawImage };
    const jpeg = new Blob(['jpeg'], { type: 'image/jpeg' });
    const canvas = { width: 0, height: 0, getContext: () => ctx, toBlob: vi.fn((fn: (b: Blob) => void) => fn(jpeg)) };
    vi.stubGlobal('createImageBitmap', decode);
    vi.stubGlobal('document', { createElement: () => canvas });
    vi.stubGlobal('FileReader', class { result = 'data:image/jpeg;base64,anBlZw=='; onload = () => {}; readAsDataURL() { this.onload(); } });
    const input = new Blob(['original']);
    const image = await prepareImage(input);
    expect(decode).toHaveBeenCalledWith(input, { imageOrientation: 'from-image' });
    expect(ctx.fillStyle).toBe('#fff');
    expect(fillRect).toHaveBeenCalledBefore(drawImage);
    expect(canvas.toBlob).toHaveBeenCalledWith(expect.any(Function), 'image/jpeg', 0.85);
    expect(image).toMatchObject({ w: 512, h: 1024, bytes: 4, blob: jpeg });
    expect(imageMeta(image)).toEqual({ id: image.id, w: 512, h: 1024, bytes: 4 });
    expect(close).toHaveBeenCalledOnce();
  });
  it('rejects undecodable files without holding originals', async () => {
    vi.stubGlobal('createImageBitmap', vi.fn().mockRejectedValue(new Error('decode')));
    await expect(prepareImage(new Blob(['bad']))).rejects.toThrow('decode');
  });
});
it('keeps request parts, missing images, whole-turn omission and counts aligned', () => {
  const turn: Message = { id: 'u', role: 'user', content: 'What is this?', images: [meta, { ...meta, id: 'missing' }] };
  const history: Message[] = [turn, { id: 'a', role: 'assistant', content: 'A photo', status: 'complete' }];
  const data = { photo: 'data:image/jpeg;base64,AA==' };
  const built = carried(history, { systemPrompt: '' }, 1000, undefined, data);
  expect(built.messages[0]?.content).toEqual([{ type: 'image_url', image_url: { url: data.photo } }, { type: 'text', text: turn.content }]);
  expect(carriedImageCount(built.messages)).toBe(1);
  expect(carried(history, { systemPrompt: '' }, 1000).messages[0]?.content).toBe(turn.content);
  expect(contextCarried(history, { systemPrompt: '' }, 1000)).toBe(contextCarried(history.map((m) => ({ ...m, images: undefined })), { systemPrompt: '' }, 1000));
  expect(carriedImageCount(carried(history, { systemPrompt: '' }, 2, undefined, data).messages)).toBe(0);
});
it('bounds the UTF-8 serialized request including carried image data', () => {
  expect(fitsRequest({ messages: [{ content: '好'.repeat(MAX_REQUEST_BYTES / 3) }] })).toBe(false);
  expect(fitsRequest({ messages: [{ content: 'hello' }] })).toBe(true);
});
it('measures only prepared bytes and renders singular English', () => {
  vi.stubGlobal('localStorage', { getItem: () => '"en"' });
  expect(imageLine([meta])).toBe('1 image · 1024 px · 117 KB');
});
it('evicts oldest complete turns only until the 20 MB host cap fits', () => {
  expect(evictedTurns([{ key: 'new', at: 3, bytes: 8 * 1024 * 1024 }, { key: 'old', at: 1, bytes: 8 * 1024 * 1024 }, { key: 'middle', at: 2, bytes: 8 * 1024 * 1024 }])).toEqual(['old']);
  expect(evictedTurns([{ key: 'exact', at: 1, bytes: IMAGE_STORE_BYTES }])).toEqual([]);
});

it('bounds SVG source bytes before parsing or decoding', async () => {
  const { MAX_SVG_BYTES } = await import('./images');
  const parser = vi.fn(); vi.stubGlobal('DOMParser', parser);
  await expect(prepareImage(new Blob([' '.repeat(MAX_SVG_BYTES + 1)], { type: 'image/svg+xml' }))).rejects.toThrow('SVG exceeds 4 MiB');
  expect(parser).not.toHaveBeenCalled();
});
