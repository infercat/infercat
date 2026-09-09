import { gzipSync } from 'node:zlib';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const bytes = new Uint8Array([0, 97, 115, 109]);
let scripts: string[];
let observed: Uint8Array[];
beforeEach(() => {
  vi.resetModules(); scripts = []; observed = [];
  vi.stubGlobal('InfercatTunnel', undefined); vi.stubGlobal('Go', undefined);
  vi.stubGlobal('document', {
    createElement: () => ({ src: '', onload: () => {} }),
    head: { append: (el: { src: string; onload: () => void }) => {
      scripts.push(el.src);
      vi.stubGlobal('Go', class {
        importObject = {};
        run() { vi.stubGlobal('InfercatTunnel', { connect: () => {} }); return new Promise(() => {}); }
      }); el.onload();
    } },
  });
  vi.spyOn(WebAssembly, 'instantiateStreaming').mockImplementation(async (response) => {
    observed.push(new Uint8Array(await (await response).arrayBuffer()));
    return { instance: {} as WebAssembly.Instance, module: {} as WebAssembly.Module };
  });
});
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it.each(['raw', 'gzip', 'decoded'])('loads the exact requested %s artifact with its sibling Go runtime', async (kind) => {
  const compressed = kind !== 'raw';
  const body = kind === 'gzip' ? new Uint8Array(gzipSync(bytes)) : bytes;
  const headers: Record<string, string> = { 'content-type': 'application/wasm', 'content-length': String(body.length) };
  if (kind === 'decoded') headers['content-encoding'] = 'GZIP';
  const network = vi.fn<typeof fetch>(async () => new Response(body, { headers })); vi.stubGlobal('fetch', network);
  const { loadInfercatTunnel } = await import('../src/transport/wasm');
  const progress = vi.fn();
  const url = `https://assets.test/v1/custom.wasm${compressed ? '.gz' : ''}?version=1`;
  await loadInfercatTunnel(progress, 'https://assets.test/v1/', url);
  expect(network).toHaveBeenCalledTimes(1); expect(network.mock.calls[0]?.[0]).toBe(url);
  expect(scripts).toEqual(['https://assets.test/v1/wasm_exec.js']); expect(observed).toEqual([bytes]);
  expect(progress).toHaveBeenLastCalledWith(expect.objectContaining({ pct: kind === 'decoded' ? null : 100 }));
});

it('does not replace an explicitly requested missing artifact, and permits retry', async () => {
  const network = vi.fn(async () => new Response('missing', { status: 404 })); vi.stubGlobal('fetch', network);
  const { loadInfercatTunnel } = await import('../src/transport/wasm');
  await expect(loadInfercatTunnel(undefined, 'https://assets.test/', 'https://assets.test/version.wasm.gz')).rejects.toThrow('requested tunnel module');
  expect(network).toHaveBeenCalledTimes(1);
  network.mockImplementation(async () => new Response(new Uint8Array(gzipSync(bytes))));
  await loadInfercatTunnel(undefined, 'https://assets.test/', 'https://assets.test/version.wasm.gz');
  expect(observed).toEqual([bytes]);
});

it('retains the app default gzip-to-raw fallback', async () => {
  const network = vi.fn().mockResolvedValueOnce(new Response('missing', { status: 404 })).mockResolvedValueOnce(new Response(bytes));
  vi.stubGlobal('fetch', network);
  const { loadInfercatTunnel } = await import('../src/transport/wasm'); await loadInfercatTunnel();
  expect(network.mock.calls.map((args) => args[0])).toEqual(['/infercat.wasm.gz', '/infercat.wasm']);
});
