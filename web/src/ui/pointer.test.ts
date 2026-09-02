// 014 promise 6: on a touch keyboard Return makes a new line, so Send is the only way to send and
// the "Enter sends" hint is not shown. Everything downstream of that is this one question.
import { afterEach, describe, expect, it } from 'vitest';
import { coarsePointer } from './pointer';

const g = globalThis as { matchMedia?: unknown };
const real = g.matchMedia;
afterEach(() => {
  if (real === undefined) delete g.matchMedia;
  else g.matchMedia = real;
});

describe('coarsePointer', () => {
  it('is true on a touch device and false on a pointer device', () => {
    g.matchMedia = (q: string) => ({ matches: q.includes('coarse') });
    expect(coarsePointer()).toBe(true);
    g.matchMedia = () => ({ matches: false });
    expect(coarsePointer()).toBe(false);
  });

  it('assumes a pointer where the question cannot be asked, rather than hiding the hint', () => {
    delete g.matchMedia;
    expect(coarsePointer()).toBe(false);
    g.matchMedia = () => {
      throw new Error('no');
    };
    expect(coarsePointer()).toBe(false);
  });
});
