import { createElement } from 'react';
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import RunRow, { ordinal, runLine, type RunView } from './ui/Run';
const base: RunView = { id: 'run-1', state: 'running', elapsed: 31, outputs: [], steps: [{ id: 'step-1', at: 4, kind: 'search', tool: 'exa', result: '4 results', text: '<script>raw</script>', complete: true }, { id: 'step-2', at: 12, kind: 'run', name: 'stats.py' }] };
const view = (run = base, connected = true, pending = false) => renderToStaticMarkup(createElement(RunRow, { run, host: 'Max’s workstation', connected, disabled: false, pending, onCancel() {}, onAnswer() {}, onRetry() {} }));
describe('the run row', () => {
  it('shows elapsed-only state and collapsed step details', () => {
    const html = view();
    expect(html).toContain('running stats.py · 0:31'); expect(html).toContain('2 steps so far');
    expect(html).toContain('searching the web · exa → 4 results'); expect(html).not.toContain('<details open'); expect(html).not.toContain('~');
  });
  it('removes live squares on disconnect, preserving the line', () => {
    expect(view()).toContain('class="live"'); expect(view(base, false)).not.toContain('class="live"'); expect(view(base, false)).toContain(runLine(base));
  });
  it('shows the harness request as text with explicit ghost Allow/Deny', () => {
    const html = view({ ...base, state: 'waiting', ask: { id: 'approval-1', text: '<script>request</script>' } });
    expect(html).toContain('&lt;script&gt;request&lt;/script&gt;'); expect(html).toContain('Allow'); expect(html).toContain('Deny');
    expect(html).not.toContain('class="live"'); expect(html).not.toContain('class="primary');
  });
  it('disables pending approval and cancel actions', () => {
    expect(view({ ...base, state: 'waiting', ask: { id: 'a', text: 'request' } }, true, true).match(/disabled=""/g)).toHaveLength(3);
  });
  it('uses the collapsed Thinking block', () => {
    const html = view({ ...base, steps: [{ id: 't', at: 0, kind: 'think', text: 'One two three' }] });
    expect(html).toContain('thinking  full'); expect(html).toContain('3 words'); expect(html).toContain('aria-expanded="false"');
  });
  it('done keeps outputs, words and host token accounting without a live line', () => {
    const html = view({ ...base, state: 'done', text: 'Finished.', tokens: { in: 4120, out: 910 }, outputs: [{ name: 'notes.md', kind: 'MD', text: 'one\ntwo', lines: 2, source: 'file' }] });
    expect(html).not.toContain('class="spoken"'); expect(html).toContain('2 steps'); expect(html).toContain('notes.md');
    expect(html).toContain('4,120 tokens in · 910 out'); expect(html).toContain('Copy');
  });
  it('failed exposes escaped evidence and Try again', () => {
    const html = view({ ...base, state: 'failed', error: '<img onerror=alert(1)>' });
    expect(html).toContain('ended interrupted'); expect(html).toContain('Try again'); expect(html).toContain('&lt;img onerror=alert(1)&gt;');
  });
  it('cancelled keeps partial output without retry or Cancel', () => {
    const html = view({ ...base, state: 'cancelled', text: 'partial' });
    expect(html).toContain('cancelled · 0:31'); expect(html).toContain('partial'); expect(html).not.toContain('>Cancel<'); expect(html).not.toContain('Try again');
  });
  it('handles queue ordinals and first place', () => {
    expect([1, 2, 3, 4, 11, 12, 13, 21, 112].map(ordinal)).toEqual(['1st','2nd','3rd','4th','11th','12th','13th','21st','112th']);
    expect(runLine({ ...base, state: 'queued', position: 1 })).toBe('queued · next in line');
    expect(runLine({ ...base, state: 'queued', position: 2 })).toBe('queued · 2nd in line');
  });
});

import { runView } from './ui/RunItem';
import { scriptedRun } from '../dev/fake-runs';
it('uses the unranked queue fallback and run age including waits', () => {
  expect(runLine({ ...base, state: 'queued', position: undefined })).toBe('queued');
  const record = scriptedRun('r', 'waiting');
  expect(runView(record, Date.parse(record.created) + 90000).elapsed).toBe(90);
  expect(runView({ ...record, state: 'done', updated: new Date(Date.parse(record.created) + 48000).toISOString() }, Date.parse(record.created) + 90000).elapsed).toBe(48);
  expect(runView({ ...record, attempts: [] }).tokens).toBeUndefined();
});
