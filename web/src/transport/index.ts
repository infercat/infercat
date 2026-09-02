// The two Transports and the connect flow that produces one.
import { fetchOverConn } from './http1';
import { loadBunnyTunnel } from './wasm';
import { tunnelGlobal, type PingResult, type Session, type Transport } from './types';

export type { Conn, PingResult, Session, Transport, BunnyTunnel } from './types';
export { Http1Error, fetchOverConn, encodeRequest, parseResponseHead } from './http1';

/** Real fetch against `bunny-network serve --dev-listen` (or the fake gateway). */
export class DirectTransport implements Transport {
  readonly kind = 'direct' as const;
  constructor(private readonly base: string) {}

  fetch(input: string, init?: RequestInit): Promise<Response> {
    return fetch(new URL(input, this.base).toString(), init);
  }

  async ping(): Promise<PingResult> {
    const started = performance.now();
    const res = await this.fetch('/healthz');
    await res.arrayBuffer();
    return { rttMs: Math.round(performance.now() - started), via: 'localhost', direct: true };
  }

  close(): void {}
}

/** HTTP/1.1 over the tunnel: one Session, one fresh conn per request. */
export class TunnelTransport implements Transport {
  readonly kind = 'tunnel' as const;
  constructor(
    readonly session: Session,
    private readonly port = 80,
  ) {}

  async fetch(input: string, init?: RequestInit): Promise<Response> {
    const headers = new Headers(init?.headers);
    const body = toBytes(init?.body);
    headers.set('host', 'bunny');
    headers.set('connection', 'close');
    if (body) headers.set('content-length', String(body.length));
    const conn = await this.session.dial(this.port);
    return fetchOverConn(
      conn,
      { method: init?.method ?? 'GET', path: input, headers, ...(body ? { body } : {}) },
      init?.signal,
    );
  }

  ping(): Promise<PingResult> {
    return this.session.ping();
  }

  close(): void {
    this.session.close();
  }
}

function toBytes(body: BodyInit | null | undefined): Uint8Array | undefined {
  if (body === null || body === undefined) return undefined;
  if (typeof body === 'string') return new TextEncoder().encode(body);
  if (body instanceof Uint8Array) return body;
  if (body instanceof ArrayBuffer) return new Uint8Array(body);
  throw new TypeError('the tunnel transport only sends string or byte bodies');
}

/** Where the connect flow is right now; the connect screen renders these verbatim. */
export type ConnectStage =
  | { name: 'wasm'; pct: number | null }
  | { name: 'relay' }
  | { name: 'handshake'; path?: PingResult }
  | { name: 'verify' }
  | { name: 'connected' };

export interface OpenOptions {
  mode: 'direct' | 'tunnel';
  directURL?: string;
  derpMapURL?: string;
  /** tailcat PrivateKey JSON kept from a previous visit, so the host sees one client identity. */
  privateKey?: string;
  onStage?: (stage: ConnectStage) => void;
  onLog?: (line: string) => void;
}

export interface OpenResult {
  transport: Transport;
  path: PingResult | null;
  /** Present in tunnel mode; worth persisting so the next visit reuses the identity. */
  privateKeyJSON?: string;
}

/**
 * Brings a transport up to the point where the gateway is reachable. Verifying the invite
 * (GET /me) is the caller's next step so the stages stay honest about what failed.
 */
export async function openTransport(addr: string, opts: OpenOptions): Promise<OpenResult> {
  if (opts.mode === 'direct') {
    const base = opts.directURL ?? '/';
    const transport = new DirectTransport(base);
    const path = await transport.ping().catch(() => null);
    return { transport, path };
  }

  opts.onStage?.({ name: 'wasm', pct: tunnelGlobal() ? 100 : null });
  const bridge = await loadBunnyTunnel((p) => opts.onStage?.({ name: 'wasm', pct: p.pct }));

  opts.onStage?.({ name: 'relay' });
  const session = await bridge.connect({
    addr,
    ...(opts.derpMapURL ? { derpMapURL: opts.derpMapURL } : {}),
    ...(opts.privateKey ? { privateKey: opts.privateKey } : {}),
    ...(opts.onLog ? { onLog: opts.onLog } : {}),
  });

  // connect() already resolved after its first successful ping, so the handshake is up; this ping
  // is what turns that into a number the header can show.
  const path = await session.ping().catch(() => null);
  opts.onStage?.({ name: 'handshake', ...(path ? { path } : {}) });
  return { transport: new TunnelTransport(session), path, privateKeyJSON: session.privateKeyJSON };
}

/** "relayed via sfo · 84 ms" — the truth about the path, never a green dot (pm/BELIEFS.md). */
export function describePath(path: PingResult | null, fallbackRegion?: string): string {
  if (!path) return fallbackRegion ? `relayed via ${fallbackRegion}` : 'path unknown';
  const rtt = `${Math.round(path.rttMs)} ms`;
  if (path.direct) return `direct · ${rtt}`;
  const derp = /^DERP\(([^)]+)\)$/.exec(path.via);
  if (derp) return `relayed via ${derp[1]} · ${rtt}`;
  return `via ${path.via} · ${rtt}`;
}
