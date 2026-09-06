import { describe, expect, it } from 'vitest';
import { clipboardReader } from './clipboard';

// 039 comfort 2: Paste offers itself only where the clipboard can actually be read. Everywhere else
// the button is absent rather than present-and-broken — a control that throws when pressed is worse
// than no control, because the reader cannot tell it apart from the paste having failed.
describe('clipboardReader', () => {
  it('hands back the reader where readText exists', () => {
    const readText = () => Promise.resolve('ic1.tcAddress.secret');
    const clipboard = { readText };
    expect(clipboardReader({ clipboard })).toBe(clipboard);
  });

  // The fallback, browser by browser: Firefox ships no readText at all, an http origin has no
  // navigator.clipboard, and an old engine has no navigator worth speaking of.
  it('hands back nothing — so the button is never rendered — where it does not', () => {
    expect(clipboardReader({ clipboard: { writeText: () => Promise.resolve() } })).toBeNull();
    expect(clipboardReader({ clipboard: {} })).toBeNull();
    expect(clipboardReader({})).toBeNull();
    expect(clipboardReader(null)).toBeNull();
    expect(clipboardReader(undefined)).toBeNull();
  });

  it('is not fooled by a readText that is not callable', () => {
    expect(clipboardReader({ clipboard: { readText: 'yes' } })).toBeNull();
    expect(clipboardReader({ clipboard: { readText: null } })).toBeNull();
  });

  it('reads through the reader it returns', async () => {
    const reader = clipboardReader({ clipboard: { readText: () => Promise.resolve('  ic1.tcA.b  ') } });
    expect(reader).not.toBeNull();
    expect((await reader!.readText()).trim()).toBe('ic1.tcA.b');
  });
});
