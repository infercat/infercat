/**
 * Reading the clipboard is a permission, not a capability: Firefox has no `readText` at all, Safari
 * only grants it inside a user gesture, and a page served over plain http has no `navigator.clipboard`
 * whatsoever. So Paste is offered only where the read exists — where it does not, the button is not
 * there to be pressed and the field is the one it always was (039 comfort 2, "falling back silently").
 *
 * Shaped like `composing` next door: a pure predicate over the navigator, so the fallback is a test
 * rather than a browser matrix.
 */
export interface ClipboardReader {
  readText(): Promise<string>;
}

export function clipboardReader(nav: unknown): ClipboardReader | null {
  const clip = (nav as { clipboard?: { readText?: unknown } } | null | undefined)?.clipboard;
  return typeof clip?.readText === 'function' ? (clip as ClipboardReader) : null;
}
