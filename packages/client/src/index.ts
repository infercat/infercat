import { TunnelTransport } from './transport/index';
import { raceAbort } from './transport/http1';
import { loadInfercatTunnel } from './transport/wasm';
import type { PingResult } from './transport/types';
import { decodeInvite } from './invite';
import { gatewayError, type Me } from './gateway';
export { GatewayError, gatewayError } from './gateway';
export type { GatewayErrorCode, Me, Limits } from './gateway';

export interface ConnectOptions {
  /** Exact .wasm or .wasm.gz URL. The matching wasm_exec.js must live beside it. */
  wasmURL?: string;
  derpMapURL?: string;
  /** Reuse a caller-owned identity; never shared concurrently between clients. */
  privateKey?: string;
  onLog?: (line: string) => void;
  onWasmProgress?: (pct: number | null) => void;
}

export interface Session {
  readonly baseURL: string;
  /** Measurement at connect time, not a continuously updated health indicator. */
  readonly status: { readonly kind: 'direct' | 'relayed'; readonly rttMs: number; readonly via: string };
  readonly privateKeyJSON: string;
  readonly fetch: typeof globalThis.fetch;
  me(): Promise<Me>;
  openai(): { baseURL: string; apiKey: string; fetch: typeof globalThis.fetch;
    dangerouslyAllowBrowser: true; maxRetries: 0 };
  close(): void;
}

/** One invite, one tunnel session. Nothing is persisted or retried by this package. */
export async function connect(invite: string, opts: ConnectOptions = {}): Promise<Session> {
  const { addr, secret } = decodeInvite(invite);
  // Go's js/wasm net/http disables fetch under Node (roundtrip_js.go). Do not disguise
  // the runtime or install globals to make a browser artifact appear Node-compatible.
  if (typeof document === 'undefined') {
    throw new Error('@infercat/client v0 requires a browser; the current Go wasm bridge cannot fetch its DERP map in Node.');
  }
  const wasmURL = new URL(opts.wasmURL ?? 'https://infercat.ai/infercat.wasm.gz', document.baseURI);
  const bridge = await loadInfercatTunnel(
    (p) => opts.onWasmProgress?.(p.pct), new URL('.', wasmURL).href, wasmURL.href,
  );
  const tunnel = await bridge.connect({ addr, privateKey: opts.privateKey,
    derpMapURL: opts.derpMapURL, onLog: opts.onLog });
  const transport = new TunnelTransport(tunnel);
  const lifetime = new AbortController();
  const baseURL = 'http://host/v1';
  let path: PingResult;
  try { path = await tunnel.ping(); } catch (error) { transport.close(); throw error; }

  const fetch: typeof globalThis.fetch = async (input, init) => {
    const target = input instanceof Request ? input : new URL(String(input), `${baseURL}/`);
    const request = new Request(target, init);
    const url = new URL(request.url);
    if (url.origin !== 'http://host' || url.username || url.password) {
      throw new TypeError('Session.fetch only accepts paths or URLs under http://host.');
    }
    const signal = AbortSignal.any([request.signal, lifetime.signal]);
    signal.throwIfAborted();
    const headers = new Headers(request.headers);
    headers.delete('content-length');
    headers.delete('transfer-encoding');
    headers.set('authorization', `Bearer ${secret}`);
    // Request normalizes FormData, Blob, strings, bytes and Request+init overrides.
    // Uploads are buffered; responses, including SSE, stream over the shared transport.
    const body = await readBody(request, signal);
    return transport.fetch(url.pathname + url.search, { method: request.method, headers, body, signal });
  };
  const session: Session = {
    baseURL, fetch, privateKeyJSON: tunnel.privateKeyJSON,
    status: Object.freeze({ kind: path.direct ? 'direct' : 'relayed', rttMs: path.rttMs, via: path.via }),
    async me() {
      const response = await fetch('/me', { signal: AbortSignal.timeout(10_000) });
      if (!response.ok) throw await gatewayError(response);
      return response.json() as Promise<Me>;
    },
    openai: () => ({ baseURL, apiKey: secret, fetch, dangerouslyAllowBrowser: true, maxRetries: 0 }),
    close() {
      if (lifetime.signal.aborted) return;
      lifetime.abort();
      transport.close();
    },
  };
  // Do not hand back a session that has never authenticated its invite.
  try { await session.me(); } catch (error) { session.close(); throw error; }
  return session;
}

/** Stop a pending upload producer too, not only the promise waiting for its bytes. */
async function readBody(request: Request, signal: AbortSignal): Promise<Uint8Array<ArrayBuffer> | undefined> {
  if (!request.body) return undefined;
  const reader = request.body.getReader();
  const cancel = () => { void reader.cancel(signal.reason).catch(() => {}); };
  signal.addEventListener('abort', cancel, { once: true });
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    signal.throwIfAborted();
    for (;;) {
      const { done, value } = await raceAbort(reader.read(), signal);
      signal.throwIfAborted();
      if (done) break;
      chunks.push(value); size += value.length;
    }
    const body = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) { body.set(chunk, offset); offset += chunk.length; }
    return body;
  } finally {
    signal.removeEventListener('abort', cancel);
    reader.releaseLock();
  }
}
