import { readFileSync } from 'node:fs';
import { transformWithEsbuild } from 'vite';
import { expect, it, vi } from 'vitest';
const origin = 'https://app.test';
const config = { revision: 'build-b', version: '0.1.2', shell: ['/index.html', '/assets/app-b.js'], runtime: ['/runtime/0.1.2/wasm_exec.js', '/runtime/0.1.2/infercat.wasm.gz'] };
async function harness() {
  const handlers = new Map<string, (event: { request?: Request; waitUntil?: (p: Promise<unknown>) => void; respondWith?: (p: Promise<Response>) => void }) => void>();
  const stores = new Map<string, Map<string, Response>>();
  const key = (input: string | Request) => new URL(typeof input === 'string' ? input : input.url, origin).href;
  const network = vi.fn(async (input: string | Request) => new Response(`network ${key(input)}`));
  const caches = {
    match: async (input: string | Request, { cacheName }: { cacheName: string }) => stores.get(cacheName)?.get(key(input))?.clone(),
    keys: async () => [...stores.keys()],
    delete: async (name: string) => stores.delete(name),
    open: async (name: string) => {
      if (!stores.has(name)) stores.set(name, new Map());
      const store = stores.get(name)!;
      return {
        match: async (input: string | Request) => store.get(key(input))?.clone(),
        put: async (input: string | Request, response: Response) => { store.set(key(input), response.clone()); },
        addAll: async (inputs: Request[]) => {
          const responses = await Promise.all(inputs.map((input) => network(input)));
          if (responses.some((r) => !r.ok)) throw new Error('cache add failed');
          inputs.forEach((input, index) => store.set(key(input), responses[index]!.clone()));
        },
      };
    },
  };
  class LocalRequest extends Request { constructor(input: string, init?: RequestInit) { super(new URL(input, origin), init); } }
  const source = readFileSync(new URL('./sw.ts', import.meta.url), 'utf8');
  const js = await transformWithEsbuild(source, 'sw.ts', { loader: 'ts', define: { __PRECACHE__: JSON.stringify(config) } });
  const worker = { location: { origin }, clients: { claim: vi.fn() }, addEventListener: (name: string, handler: typeof handlers extends Map<string, infer T> ? T : never) => handlers.set(name, handler) };
  new Function('self', 'caches', 'fetch', 'Request', 'Response', js.code)(worker, caches, network, LocalRequest, Response);
  async function lifecycle(type: string) { let work: Promise<unknown> | undefined; handlers.get(type)!({ waitUntil: (p) => { work = p; } }); await work; }
  function fetchEvent(path: string, mode = 'cors', method = 'GET') {
    let work: Promise<Response> | undefined;
    handlers.get('fetch')!({ request: { url: new URL(path, origin).href, mode, method } as Request, respondWith: (p) => { work = p; } });
    return work;
  }
  return { stores, caches, network, lifecycle, fetchEvent, worker };
}
it('precaches the runtime as a pair and reuses it on same-version shell updates', async () => {
  const h = await harness(); await h.lifecycle('install');
  expect(h.network).toHaveBeenCalledTimes(4);
  h.network.mockClear(); await h.lifecycle('install'); expect(h.network).toHaveBeenCalledTimes(2);
  expect(h.stores.get('infercat-runtime-0.1.2')?.size).toBe(2);
});
it('serves an online navigation fresh but keeps the matching old shell for offline fallback', async () => {
  const h = await harness(); await h.lifecycle('install');
  h.network.mockResolvedValue(new Response('newer HTML'));
  expect(await (await h.fetchEvent('/', 'navigate')!).text()).toBe('newer HTML');
  h.network.mockRejectedValue(new TypeError('offline'));
  expect(await (await h.fetchEvent('/?tracking=1', 'navigate')!).text()).toBe(`network ${origin}/index.html`);
  expect(await (await h.fetchEvent('/assets/app-b.js')!).text()).toContain('app-b.js');
});
it('does not intercept API responses, invite queries, mutations or cross-origin requests', async () => {
  const h = await harness(); await h.lifecycle('install');
  for (const [url, method] of [['/me', 'GET'], ['/v1/chat/completions', 'POST'], ['/assets/app-b.js?invite=secret', 'GET'], ['https://relay.test/map', 'GET']]) {
    expect(h.fetchEvent(url!, 'cors', method)).toBeUndefined();
  }
});
it('a failed runtime download leaves older caches intact and never writes half a new pair', async () => {
  const h = await harness(); await (await h.caches.open('infercat-runtime-old')).put('/old', new Response('old'));
  h.network.mockImplementation(async (input) => new Response('bad', { status: String(typeof input === 'string' ? input : input.url).endsWith('.gz') ? 503 : 200 }));
  await expect(h.lifecycle('install')).rejects.toThrow('Runtime precache failed');
  expect(h.stores.get('infercat-runtime-old')?.size).toBe(1); expect(h.stores.get('infercat-runtime-0.1.2')?.size).toBe(0);
});
it('activation retires older owned caches, preserving unrelated and newer staged caches', async () => {
  const h = await harness(); await h.caches.open('infercat-shell-old'); await h.caches.open('unrelated');
  await h.lifecycle('install'); await h.caches.open('infercat-shell-future'); await h.lifecycle('activate');
  expect([...h.stores.keys()]).toEqual(['unrelated', 'infercat-shell-build-b', 'infercat-runtime-0.1.2', 'infercat-shell-future']);
  expect(h.worker.clients.claim).toHaveBeenCalledOnce();
});

it('late fetches cannot recreate a cache retired by a newer worker', async () => {
  const h = await harness(); await h.lifecycle('install');
  h.stores.clear();
  await h.fetchEvent('/assets/app-b.js'); await h.fetchEvent('/runtime/0.1.2/wasm_exec.js');
  h.network.mockRejectedValue(new TypeError('offline'));
  await h.fetchEvent('/', 'navigate');
  expect([...h.stores.keys()]).toEqual([]);
});
