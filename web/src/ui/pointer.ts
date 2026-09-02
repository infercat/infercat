/**
 * Is the reader pointing with a finger? On a touch keyboard Return is how you start a new line, so
 * Enter must not send and the "Enter sends" hint is a lie worth not telling (014 promise 6).
 * Read through a function rather than a constant so a test — and a Playwright run with a touch
 * viewport — sees the device it is actually on.
 */
export function coarsePointer(): boolean {
  const mm = (globalThis as { matchMedia?: (q: string) => { matches: boolean } }).matchMedia;
  try {
    return mm ? mm('(pointer: coarse)').matches : false;
  } catch {
    return false;
  }
}
