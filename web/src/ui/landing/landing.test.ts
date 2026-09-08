import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { en } from '../../i18n/en';
import { zh } from '../../i18n/zh';

describe('landing contract', () => {
  it('keeps complete language tables', () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort());
    for (const value of [...Object.values(en), ...Object.values(zh)]) expect(value.trim()).not.toBe('');
  });
  it('shows the actual six new-key defaults', () => {
    const go = readFileSync(new URL('../../../../internal/keys/keys.go', import.meta.url), 'utf8');
    const defaults = /func DefaultLimits\(\) Limits \{([\s\S]*?)\n}/.exec(go)?.[1] ?? '';
    const page = readFileSync(new URL('./Landing.tsx', import.meta.url), 'utf8');
    for (const [field, value] of [
      ['RPM', 20],
      ['TPM', 20000],
      ['MaxConcurrent', 1],
      ['DailyTokens', 200000],
      ['MaxContext', 0],
      ['MaxOutputTokens', 4096],
    ] as const) {
      expect(defaults).toMatch(new RegExp(`${field}: ${value}(?:,|})`));
    }
    expect(defaults).not.toContain('Models:'); // nil means all models, not an empty allowlist.
    for (const label of ['20', '20 000', '1', '200 000'])
      expect(page).toMatch(new RegExp(`\\{['"]${label}['"]\\}`));
    expect(page).toContain('t.d5_v');
    expect(page).toContain('t.d6_v');
    expect(en.lim_note).toContain('4 096');
    expect(zh.lim_note).toContain('4 096');
  });
});
