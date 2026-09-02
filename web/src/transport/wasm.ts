// Loads the wasm bridge from ticket 001 (web/public/bunny.wasm + wasm_exec.js) with byte progress.
// Nothing here runs until the user presses Connect, so the landing page costs no wasm download.
import { tunnelGlobal, type BunnyTunnel } from './types';

export interface WasmProgress {
  loaded: number;
  total: number;
  /** null while the total is unknown. */
  pct: number | null;
}

interface GoRuntime {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}

let booting: Promise<BunnyTunnel> | null = null;

/** Resolves window.BunnyTunnel, loading and starting the wasm module the first time. */
export function loadBunnyTunnel(
  onProgress?: (p: WasmProgress) => void,
  base = '/',
): Promise<BunnyTunnel> {
  const ready = tunnelGlobal();
  if (ready) return Promise.resolve(ready);
  booting ??= boot(base, onProgress).catch((err: unknown) => {
    booting = null;
    throw err;
  });
  return booting;
}

async function boot(base: string, onProgress?: (p: WasmProgress) => void): Promise<BunnyTunnel> {
  const scope = globalThis as { Go?: new () => GoRuntime };
  if (!scope.Go) await loadScript(`${base}wasm_exec.js`);
  if (!scope.Go) throw new Error('wasm_exec.js loaded but did not define Go');

  const go = new scope.Go();
  const { instance } = await WebAssembly.instantiateStreaming(fetchWasm(base, onProgress), go.importObject);
  // go.run resolves only when the Go program exits; the bridge blocks forever on purpose.
  void go.run(instance);
  return waitForTunnel();
}

async function waitForTunnel(timeoutMs = 15_000): Promise<BunnyTunnel> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const t = tunnelGlobal();
    if (t) return t;
    if (Date.now() > deadline) throw new Error('the tunnel module started but never became ready');
    await new Promise((r) => setTimeout(r, 20));
  }
}

// Static hosts cannot negotiate Content-Encoding for .wasm, so ticket 001 also emits bunny.wasm.gz.
// Prefer it and decompress in the page — unless the host labelled it `Content-Encoding: gzip`, in
// which case the browser has already decoded it and decompressing again destroys the module.
async function fetchWasm(base: string, onProgress?: (p: WasmProgress) => void): Promise<Response> {
  const headers = { 'content-type': 'application/wasm' };
  const gz = await fetch(`${base}bunny.wasm.gz`, { signal: assetTimeout() }).catch(() => null);
  if (gz?.ok && gz.body && !isHTML(gz)) {
    // Header values are case-insensitive: a host that says `GZIP` has decoded it just the same.
    const decoded = (gz.headers.get('content-encoding') ?? '').toLowerCase().includes('gzip');
    // When the browser decoded it, Content-Length describes the wire bytes, not the ones we are
    // counting, so there is no honest total to show a percentage against.
    const total = decoded ? 0 : Number(gz.headers.get('content-length')) || 0;
    const body = counted(gz.body, total, onProgress);
    return new Response(
      decoded
        ? body
        : body.pipeThrough(new DecompressionStream('gzip') as unknown as TransformStream<Uint8Array, Uint8Array>),
      { headers },
    );
  }
  const raw = await fetch(`${base}bunny.wasm`, { signal: assetTimeout() });
  if (!raw.ok || !raw.body) throw new Error(`could not download the tunnel module (${raw.status})`);
  const size = Number(raw.headers.get('content-length')) || 0;
  return new Response(counted(raw.body, size, onProgress), { headers });
}

/**
 * A static host that accepts the connection and then says nothing would otherwise leave the
 * connect screen on "Loading the tunnel …" for ever, with a Try again that joins the same hung
 * promise. Sixty seconds, then a failure the screen can offer a real retry from.
 */
function assetTimeout(): AbortSignal | undefined {
  return typeof AbortSignal?.timeout === 'function' ? AbortSignal.timeout(60_000) : undefined;
}

function isHTML(r: Response): boolean {
  return (r.headers.get('content-type') ?? '').includes('text/html');
}

function counted(
  stream: ReadableStream<Uint8Array>,
  total: number,
  onProgress?: (p: WasmProgress) => void,
): ReadableStream<Uint8Array> {
  let loaded = 0;
  return stream.pipeThrough(
    new TransformStream<Uint8Array, Uint8Array>({
      transform(chunk, controller) {
        loaded += chunk.byteLength;
        onProgress?.({ loaded, total, pct: total > 0 ? Math.min(100, Math.floor((100 * loaded) / total)) : null });
        controller.enqueue(chunk);
      },
    }),
  );
}

function loadScript(src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const el = document.createElement('script');
    el.src = src;
    el.async = false;
    el.onload = () => resolve();
    el.onerror = () => reject(new Error(`could not load ${src}`));
    document.head.append(el);
  });
}
