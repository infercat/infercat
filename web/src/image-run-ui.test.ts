import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import RunRow, { runLine, type RunView } from './ui/Run';
const base: RunView = { id: 'image-1', runKind: 'image', state: 'running', elapsed: 31, steps: [], outputs: [] };
const view = (run: RunView) => renderToStaticMarkup(createElement(RunRow, { run, host: 'Owned host', connected: true, disabled: false, pending: false, onCancel() {}, onAnswer() {}, onRetry() {} }));
describe('image states on the shared run row', () => {
  it('keeps generation live after a cancellation request, without another Cancel', () => {
    const html = view({ ...base, cancelRequested: true });
    expect(html).toContain('cancelling · after this image · 0:31');
    expect(html).toContain('class="live"');
    expect(html).not.toContain('>Cancel<');
    expect(html).not.toContain('cancelled ·');
  });
  it('queued cancellation says it never started', () => {
    expect(runLine({ ...base, state: 'cancelled' })).toBe('cancelled · not started');
    expect(view({ ...base, state: 'cancelled' })).not.toContain('class="live"');
  });
  it('a finished image wins over the sticky cancellation request and can be regenerated', () => {
    const html = view({ ...base, state: 'done', cancelRequested: true, images: [{ name: 'made.png', size: 123, url: 'blob:owned-image' }] });
    expect(html).toContain('1 image · 0:31 on Owned host');
    expect(html).toContain('blob:owned-image');
    expect(html).toContain('>Regenerate<');
    expect(html).not.toContain('cancelling');
    expect(html).not.toContain('tokens');
  });
  it('discarded or expired output keeps its accounting and regeneration action', () => {
    const html = view({ ...base, state: 'done', outputGone: true });
    expect(html).toContain('no longer on Owned host');
    expect(html).toContain('1 image · 0:31 on Owned host');
    expect(html).toContain('>Regenerate<');
  });
  it('image failure uses its own copy and escaped engine evidence', () => {
    const html = view({ ...base, state: 'failed', error: '<script>raw engine</script>' });
    expect(html).toContain('could not make this image');
    expect(html).toContain('&lt;script&gt;raw engine&lt;/script&gt;');
    expect(html).toContain('0:31 on Owned host');
    expect(html).toContain('Try again');
    expect(html).not.toContain('1 image ·');
  });
});
