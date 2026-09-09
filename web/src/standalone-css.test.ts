import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import { expect, it } from 'vitest';
const require = createRequire(import.meta.url);
const { parse } = createRequire(require.resolve('vite/package.json'))('postcss');
interface Rule { type: string; name?: string; params?: string; selector?: string; parent?: Rule }
it('applies every safe-area padding only inside the standalone media query', () => {
  const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'styles.css'), 'utf8');
  const found = new Set<string>();
  parse(css).walkDecls((declaration: { value: string; parent: Rule }) => {
    if (!declaration.value.includes('env(safe-area-inset-')) return;
    found.add(declaration.parent.selector!);
    let scoped = false;
    for (let rule: Rule | undefined = declaration.parent; rule; rule = rule.parent) {
      if (rule.type === 'atrule' && rule.name === 'media' && rule.params === '(display-mode: standalone)') scoped = true;
    }
    expect(scoped).toBe(true);
  });
  expect([...found].sort()).toEqual(['.topbar', '.composer', '.sidebar', '.sheet-wrap', '.connect', '.toast, .banner'].sort());
});
