import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, expect, it, vi } from 'vitest';
import { FileChip, FileSheet, chipName } from './FileChip';
import { fileBlocks, type AttachedFile } from '../files';
const file: AttachedFile = { name: 'rates.pdf', kind: 'PDF', pages: 1, text: 'The burst multiplier is two.', source: 'file' };
afterEach(() => vi.unstubAllGlobals());
it('renders the composer measure and an accessible remove action; reading preserves remove', () => {
  vi.stubGlobal('navigator', { language: 'en' });
  const render = (reading: boolean) => renderToStaticMarkup(createElement(FileChip, { file, reading, onRemove: () => {} }));
  expect(render(false)).toContain('PDF · 1 page · 7 tokens');
  expect(render(true)).toContain('PDF · reading…');
  expect(render(true)).toContain('aria-label="Remove file: rates.pdf"');
  expect(render(true)).toContain('aria-busy="true"');
});
it('makes a sent chip pressable and keeps long filename extensions visible', () => {
  const html = renderToStaticMarkup(createElement(FileChip, { file, onOpen: () => {} }));
  expect(html).toContain('<button class="chip"');
  const name = chipName('quarterly-rate-limits-report-version-three.pdf');
  expect(name).toContain('…'); expect(name).toMatch(/\.pdf$/); expect(name.length).toBe(30);
});
it('shows the exact outgoing block in the sheet as escaped text, with a dialog name and copy action', () => {
  vi.stubGlobal('navigator', { language: 'en' });
  const marked = { ...file, text: '<script>never executed</script>\nKeep this line.' };
  const html = renderToStaticMarkup(createElement(FileSheet, { file: marked, host: 'Max', onClose: () => {} }));
  expect(html).toContain('role="dialog"'); expect(html).toContain('aria-label="rates.pdf"');
  expect(html).toContain('as sent to Max'); expect(html).toContain('>Copy</button>');
  expect(html).toContain(fileBlocks([marked]).replaceAll('<', '&lt;').replaceAll('>', '&gt;'));
});
