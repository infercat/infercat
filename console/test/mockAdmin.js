import { vi } from 'vitest';
import { createConsole } from '../app';

export const reads = data => url => {
 const path = url.slice(5);
 return new Response(JSON.stringify(path === 'usage?window=today' ? data.today : path === 'usage?window=week' ? data.week : data[path]));
};
export function mount(data, opts = {}) {
 let root = opts.root;
 if (!root) {
  vi.useFakeTimers();
  document.body.innerHTML = '<div id="app"></div>';
  root = document.querySelector('#app');
 }
 return createConsole(root, opts.token ?? 'test', opts.request ?? reads(data), opts.now ?? Date.now, opts.qr, opts.remote);
}
