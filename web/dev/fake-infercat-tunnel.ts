// An in-page fake of window.InfercatTunnel (docs/ARCHITECTURE.md §wasm bridge), so the whole connect
// flow and TunnelTransport's HTTP/1.1 parsing run for real — in unit tests and in the browser —
// before ticket 001's wasm artifact exists. The bytes on the fake Conn are genuine HTTP/1.1:
// chunked for the SSE stream, content-length for the JSON routes.
import { handleFake, type FakeOptions, type FakeRequest } from './fake-backend.ts';
import type { InfercatTunnel, Conn, PingResult, Session, TunnelConnectOptions } from '../src/transport/types.ts';

export interface FakeTunnelOptions extends FakeOptions {
  /** How long the simulated relay connection takes, in ms. */
  connectMs?: number;
  /** Simulated relay round trip, in ms. */
  rttMs?: number;
  /** ping() rejects, so the degraded-path surface can be seen and screenshotted. */
  pingFails?: boolean;
  /** ping() succeeds this many times and then fails: the "last 84 ms, 31 s ago" surface. */
  pingFailsAfter?: number;
  /** How long dial() takes. Long values are how the abort-during-dial path is exercised. */
  dialMs?: number;
}

/** An address starting with this makes connect() time out, for the "host offline" screen. */
export const OFFLINE_ADDR_PREFIX = 'tcOFFLINE';

export function makeFakeTunnel(opts: FakeTunnelOptions = {}): InfercatTunnel {
  return {
    async connect(o: TunnelConnectOptions): Promise<Session> {
      const total = opts.connectMs ?? 900;
      o.onLog?.('derp: connecting to sfo');
      await sleep(total * 0.55);
      if (o.addr.startsWith(OFFLINE_ADDR_PREFIX)) {
        throw new Error('the host did not answer within 60 s');
      }
      o.onLog?.('wireguard: handshake with peer');
      await sleep(total * 0.45);
      o.onLog?.('ping ok');
      return new FakeSession(o.addr, opts);
    },
  };
}

/** Puts the fake on the global, exactly where the wasm bridge would put the real one. */
export function installFakeTunnel(opts: FakeTunnelOptions = {}): InfercatTunnel {
  const tunnel = makeFakeTunnel(opts);
  (globalThis as unknown as Partial<Window>).InfercatTunnel = tunnel;
  return tunnel;
}

export class FakeSession implements Session {
  readonly privateKeyJSON = JSON.stringify({ fake: true, id: Math.random().toString(36).slice(2) });
  private closed = false;

  constructor(
    readonly addr: string,
    private readonly opts: FakeTunnelOptions,
  ) {}

  /** Conns handed out, so a test can prove an abandoned dial was closed and not leaked. */
  readonly dialled: FakeConn[] = [];

  async dial(port = 80): Promise<Conn> {
    if (this.closed) throw new Error('the tunnel session is closed');
    if (port !== 80) throw new Error(`nothing is listening on tunnel port ${port}`);
    await sleep(this.opts.dialMs ?? 12); // a dial over a live session is cheap, not free
    const conn = new FakeConn(this.opts, this.host);
    this.dialled.push(conn);
    return conn;
  }

  private pings = 0;
  /** The host behind this session, once it has gone to sleep (014 promise 1): nothing on this
   *  session answers again — not a request, not a ping, not /me — exactly as measured against a
   *  real host killed mid-send. A session dialled afresh (Reconnect) starts awake. */
  private readonly host = { asleep: false };

  async ping(): Promise<PingResult> {
    await sleep(6);
    const n = ++this.pings;
    if (this.opts.pingFails) throw new Error('no reply from the relay');
    if (this.host.asleep) throw new Error('no reply from the relay');
    if (this.opts.pingFailsAfter !== undefined && n > this.opts.pingFailsAfter) {
      throw new Error('no reply from the relay');
    }
    const base = this.opts.rttMs ?? 84;
    return { rttMs: base + Math.round((Math.random() - 0.5) * 8), via: 'DERP(sfo)', direct: false };
  }

  close(): void {
    this.closed = true;
  }
}

/** One tunnel TCP connection: request bytes in, HTTP/1.1 response bytes out. */
export class FakeConn implements Conn {
  /** Observable so a test can assert that an abandoned conn was closed. */
  closedByCaller = false;
  private pending: (Uint8Array | null)[] = [];
  private waiter: ((v: Uint8Array | null) => void) | null = null;
  private request: Uint8Array = new Uint8Array(0);
  private serving = false;
  private closed = false;

  constructor(private readonly opts: FakeOptions, private readonly host: { asleep: boolean } = { asleep: false }) {}

  async write(data: Uint8Array): Promise<void> {
    if (this.closed) throw new Error('write on a closed conn');
    this.request = concat(this.request, data);
    if (this.serving) return;
    const parsed = parseRequest(this.request);
    if (parsed) {
      this.serving = true;
      void this.serve(parsed);
    }
  }

  async closeWrite(): Promise<void> {}

  read(): Promise<Uint8Array | null> {
    const next = this.pending.shift();
    if (next !== undefined) return Promise.resolve(next);
    if (this.closed) return Promise.resolve(null);
    return new Promise((resolve) => {
      this.waiter = resolve;
    });
  }

  close(): void {
    this.closedByCaller = true;
    this.closed = true;
    this.emit(null);
  }

  private emit(value: Uint8Array | null): void {
    if (this.waiter) {
      const resolve = this.waiter;
      this.waiter = null;
      resolve(value);
      return;
    }
    this.pending.push(value);
  }

  private async serve(req: FakeRequest): Promise<void> {
    // A sleeping host does not refuse a request, it says nothing at all. That is exactly what makes
    // the raw failure ("the connection closed inside the response") useless to a friend, and why
    // 014 promise 1 exists. The conn simply never writes.
    if (this.host.asleep) return;
    if (this.opts.hostAsleep && req.path.split('?')[0] === '/v1/chat/completions') {
      this.host.asleep = true; // it answered on the way in; it fell asleep as the friend pressed Send
      return;
    }
    if (req.path.startsWith('/v1/audio/') && this.opts.audioDelayMs) await new Promise((resolve) => setTimeout(resolve, this.opts.audioDelayMs));
    if (this.closed) return;
    const res = handleFake(req, this.opts);
    const lines = [`HTTP/1.1 ${res.status} ${STATUS_TEXT[res.status] ?? 'Status'}`];
    for (const [name, value] of Object.entries(res.headers)) lines.push(`${name}: ${value}`);
    const body = res.sse ? undefined : res.bytes ?? encode(res.body ?? '');
    lines.push(res.sse ? 'transfer-encoding: chunked' : `content-length: ${body?.length ?? 0}`);
    lines.push('connection: close');
    this.emit(encode(`${lines.join('\r\n')}\r\n\r\n`));

    if (res.sse) {
      for await (const frame of res.sse) {
        if (this.closed) return;
        const bytes = encode(frame);
        this.emit(concat(encode(`${bytes.length.toString(16)}\r\n`), concat(bytes, CRLF)));
      }
      if (this.closed) return;
      this.emit(encode('0\r\n\r\n'));
    } else if (body && body.length > 0) {
      this.emit(body);
    }
    this.closed = true;
    this.emit(null);
  }
}

function parseRequest(buf: Uint8Array): FakeRequest | null {
  const text = latin1(buf);
  const split = text.indexOf('\r\n\r\n');
  if (split < 0) return null;
  const lines = text.slice(0, split).split('\r\n');
  const [method = 'GET', path = '/'] = (lines[0] ?? '').split(' ');
  const headers: Record<string, string> = {};
  for (const line of lines.slice(1)) {
    const i = line.indexOf(':');
    if (i > 0) headers[line.slice(0, i).trim().toLowerCase()] = line.slice(i + 1).trim();
  }
  const want = Number(headers['content-length'] ?? 0);
  const bodyBytes = buf.subarray(split + 4);
  if (bodyBytes.length < want) return null;
  return { method, path, headers, body: new TextDecoder().decode(bodyBytes.subarray(0, want)) };
}

const STATUS_TEXT: Record<number, string> = {
  200: 'OK',
  204: 'No Content',
  401: 'Unauthorized',
  403: 'Forbidden',
  404: 'Not Found',
  413: 'Payload Too Large',
  422: 'Unprocessable Entity',
  429: 'Too Many Requests',
  500: 'Internal Server Error',
  502: 'Bad Gateway',
  503: 'Service Unavailable',
};

const CRLF = new Uint8Array([13, 10]);

function encode(s: string): Uint8Array {
  return new TextEncoder().encode(s);
}

function latin1(bytes: Uint8Array): string {
  let out = '';
  for (const b of bytes) out += String.fromCharCode(b);
  return out;
}

function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
  const out = new Uint8Array(a.length + b.length);
  out.set(a);
  out.set(b, a.length);
  return out;
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
