// @vitest-environment jsdom
import { readFileSync } from 'node:fs';
import { test, expect, vi } from 'vitest';

for (const token of ['', 'one-time-token']) {
 test(`pending page renders and keeps token only in memory: ${token ? 'with token' : 'without token'}`, async () => {
  vi.resetModules();
  vi.stubEnv('VITE_APP_VERSION', 'test-version');
  document.documentElement.innerHTML = readFileSync('index.html', 'utf8');
  history.replaceState(null, '', token ? '/#token=' + token : '/');
  const { adminHeaders } = await import('./main.ts');
  expect(document.querySelector('h1').textContent).toContain('test-version');
  expect(document.querySelector('main').textContent).toContain('console: slice 1 pending');
  expect(document.querySelector('[lang="zh"]').textContent).toContain('第 1 阶段');
  expect(location.hash).toBe('');
  expect(localStorage.length).toBe(0);
  expect(sessionStorage.length).toBe(0);
  expect(adminHeaders()).toEqual(token ? {Authorization: 'Bearer ' + token} : {});
  vi.unstubAllEnvs();
 });
}
