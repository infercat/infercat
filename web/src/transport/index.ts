// The two Transports and the connect flow that produces one.
import { abortError, fetchOverConn } from './http1';
import { loadBunnyTunnel } from './wasm';
import { tunnelGlobal, type Conn, type PingResult, type Session, type Transport } from './types';

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
    const conn = await dialOrAbort(this.session, this.port, init?.signal);
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

/**
 * Dialling is the one part of a request that cannot be interrupted from inside: `Session.dial` has
 * no signal. So race it. Stop pressed while the dial is pending rejects now rather than when the
 * relay gets round to it, and a conn that arrives after that is closed instead of leaked.
 */
export async function dialOrAbort(
  session: Session,
  port: number,
  signal?: AbortSignal | null,
): Promise<Conn> {
  if (!signal) return session.dial(port);
  if (signal.aborted) throw abortError();
  const dialing = session.dial(port);
  let onAbort = (): void => {};
  const aborted = new Promise<never>((_resolve, reject) => {
    onAbort = () => reject(abortError());
    signal.addEventListener('abort', onAbort, { once: true });
  });
  try {
    return await Promise.race([dialing, aborted]);
  } catch (err) {
    void dialing.then((c) => signal.aborted && c.close()).catch(() => {});
    throw err;
  } finally {
    signal.removeEventListener('abort', onAbort);
  }
}

const IDENTITY_LOCK = 'bn.tunnel-identity';

/**
 * Two tabs must not both connect as the stored tunnel identity: tailcat keys name a client, and two
 * clients under one key make the host's usage ambiguous and the relay's routing worse. The first
 * tab holds a Web Lock for as long as it lives and uses the stored key; a second tab connects with
 * a fresh ephemeral identity and leaves the stored one untouched.
 *
 * Resolves true when this tab may use (and overwrite) the persisted identity.
 */
export function claimTunnelIdentity(): Promise<boolean> {
  const locks = (globalThis.navigator as Navigator | undefined)?.locks;
  if (!locks) return Promise.resolve(true); // no Web Locks here: behave as a single tab
  return new Promise<boolean>((resolve) => {
    void locks
      .request(IDENTITY_LOCK, { ifAvailable: true }, (lock) => {
        resolve(lock !== null);
        // Holding it for the life of the page is the point; the browser releases it with the tab.
        return lock === null ? undefined : new Promise<void>(() => {});
      })
      .catch(() => resolve(true));
  });
}

function toBytes(body: BodyInit | null | undefined): Uint8Array | undefined {
  if (body === null || body === undefined) return undefined;
  if (typeof body === 'string') return new TextEncoder().encode(body);
  if (body instanceof Uint8Array) return body;
  if (body instanceof ArrayBuffer) return new Uint8Array(body);
  throw new TypeError('the tunnel transport only sends string or byte bodies');
}

export interface OpenOptions {
  mode: 'direct' | 'tunnel';
  directURL?: string;
  derpMapURL?: string;
  /** tailcat PrivateKey JSON kept from a previous visit, so the host sees one client identity. */
  privateKey?: string;
  /** Bytes of the wasm module, as a percentage when the total is known. */
  onWasmProgress?: (pct: number | null) => void;
  /** The bridge is up; from here on we are talking to the relay. */
  onWasmLoaded?: () => void;
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

  opts.onWasmProgress?.(tunnelGlobal() ? 100 : null);
  const bridge = await loadBunnyTunnel((p) => opts.onWasmProgress?.(p.pct));

  opts.onWasmLoaded?.();
  const session = await bridge.connect({
    addr,
    ...(opts.derpMapURL ? { derpMapURL: opts.derpMapURL } : {}),
    ...(opts.privateKey ? { privateKey: opts.privateKey } : {}),
    ...(opts.onLog ? { onLog: opts.onLog } : {}),
  });

  // connect() already resolved after its first successful ping, so the handshake is up; this ping
  // is what turns that into a number the header can show.
  const path = await session.ping().catch(() => null);
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
