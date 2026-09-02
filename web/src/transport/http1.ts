// HTTP/1.1 over a tunnel Conn. One connection per request (pm/DECLINED.md: no keep-alive pooling),
// `Connection: close`, and a response body that is a real ReadableStream so SSE tokens reach the UI
// as they arrive rather than after the response completes.
import type { Conn } from './types';

export class Http1Error extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'Http1Error';
  }
}

export interface Http1Request {
  method: string;
  path: string;
  headers: Headers;
  body?: Uint8Array;
}

const encoder = new TextEncoder();
const MAX_HEAD = 64 * 1024;

export function encodeRequest(req: Http1Request): Uint8Array {
  const lines = [`${req.method.toUpperCase()} ${req.path} HTTP/1.1`];
  for (const [name, value] of req.headers) lines.push(`${name}: ${value}`);
  const head = encoder.encode(`${lines.join('\r\n')}\r\n\r\n`);
  if (!req.body || req.body.length === 0) return head;
  const out = new Uint8Array(head.length + req.body.length);
  out.set(head);
  out.set(req.body, head.length);
  return out;
}

export interface ResponseHead {
  status: number;
  statusText: string;
  headers: Headers;
}

export function parseResponseHead(head: string): ResponseHead {
  const lines = head.split('\r\n');
  const status = /^HTTP\/1\.[01] (\d{3})(?: (.*))?$/.exec(lines[0] ?? '');
  if (!status) {
    throw new Http1Error(
      `the host did not answer with HTTP: ${JSON.stringify((lines[0] ?? '').slice(0, 64))}`,
    );
  }
  const headers = new Headers();
  for (const line of lines.slice(1)) {
    if (line === '') continue;
    const i = line.indexOf(':');
    if (i <= 0) throw new Http1Error(`bad header line ${JSON.stringify(line.slice(0, 64))}`);
    headers.append(line.slice(0, i).trim(), line.slice(i + 1).trim());
  }
  return { status: Number(status[1]), statusText: status[2] ?? '', headers };
}

/**
 * Writes the request and returns a Response whose body streams straight off the conn.
 * The conn is closed when the body ends, errors, is cancelled, or the signal aborts.
 *
 * Note: we deliberately do NOT closeWrite() after the request. Go's net/http server treats a read
 * EOF on the connection as a client disconnect and cancels the request context, which would kill a
 * stream mid-flight. `Connection: close` already tells the gateway how to frame its response.
 */
export async function fetchOverConn(
  conn: Conn,
  req: Http1Request,
  signal?: AbortSignal | null,
): Promise<Response> {
  let closed = false;
  const closeConn = () => {
    if (closed) return;
    closed = true;
    signal?.removeEventListener('abort', closeConn);
    try {
      conn.close();
    } catch {
      /* the conn is already gone; nothing to do */
    }
  };
  if (signal?.aborted) {
    closeConn();
    throw abortError();
  }
  signal?.addEventListener('abort', closeConn, { once: true });

  try {
    // Every await before the head arrives is raced against the signal. A tunnel conn to a peer that
    // has gone away accepts a write and never settles it, and close() does not settle it either —
    // so without this the deadline above (014 promise 1) waits for the relay's own timeout instead.
    await raceAbort(conn.write(encodeRequest(req)), signal);
    const reader = new ConnReader(conn, signal);
    const { status, statusText, headers } = parseResponseHead(await raceAbort(reader.readHead(), signal));
    if (status < 200) throw new Http1Error(`unexpected ${status} response from the host`);

    const body = bodyStream(reader, req.method, status, headers, closeConn, signal);
    if (body === null) closeConn();
    return new Response(body, { status, statusText, headers });
  } catch (err) {
    closeConn();
    throw err;
  }
}

function bodyStream(
  reader: ConnReader,
  method: string,
  status: number,
  headers: Headers,
  done: () => void,
  signal?: AbortSignal | null,
): ReadableStream<Uint8Array> | null {
  if (method.toUpperCase() === 'HEAD' || status === 204 || status === 304) return null;
  const te = headers.get('transfer-encoding');
  if (te && te.toLowerCase().split(',').some((t) => t.trim() === 'chunked')) {
    return streamOf(readChunked(reader), done, signal);
  }
  const cl = headers.get('content-length');
  if (cl !== null) {
    const n = Number(cl);
    if (!Number.isInteger(n) || n < 0) {
      throw new Http1Error(`bad Content-Length ${JSON.stringify(cl.slice(0, 32))}`);
    }
    return n === 0 ? null : streamOf(readFixed(reader, n), done, signal);
  }
  // No framing header: the body runs until the host closes the connection.
  return streamOf(readToEOF(reader), done, signal);
}

/**
 * Abort has to win the race, not wait for it: a promise that never settles must not outlive the
 * signal that cancelled it. Used for every await that talks to a Conn.
 */
export function raceAbort<T>(p: Promise<T>, signal?: AbortSignal | null): Promise<T> {
  if (!signal) return p;
  if (signal.aborted) return Promise.reject(abortError());
  return new Promise<T>((resolve, reject) => {
    const onAbort = () => reject(abortError());
    signal.addEventListener('abort', onAbort, { once: true });
    p.then(resolve, reject).finally(() => signal.removeEventListener('abort', onAbort));
  });
}

/** Buffered pull-reader over a Conn. Handles reads that split heads, lines, and chunks anywhere. */
class ConnReader {
  private buf: Uint8Array = new Uint8Array(0);
  private eof = false;

  constructor(
    private readonly conn: Conn,
    private readonly signal?: AbortSignal | null,
  ) {}

  private async pull(): Promise<boolean> {
    while (!this.eof) {
      if (this.signal?.aborted) throw abortError();
      const chunk = await raceAbort(this.conn.read(), this.signal);
      if (this.signal?.aborted) throw abortError();
      if (chunk === null) {
        this.eof = true;
        return false;
      }
      if (chunk.length > 0) {
        this.buf = concat(this.buf, chunk);
        return true;
      }
    }
    return false;
  }

  async readHead(): Promise<string> {
    for (;;) {
      const i = indexOf(this.buf, CRLF2);
      if (i >= 0) {
        const head = ascii(this.buf.subarray(0, i));
        this.buf = this.buf.subarray(i + 4);
        return head;
      }
      if (this.buf.length > MAX_HEAD) throw new Http1Error('response headers are absurdly large');
      if (!(await this.pull())) {
        throw new Http1Error('the connection closed before the host answered');
      }
    }
  }

  async readLine(): Promise<string> {
    for (;;) {
      const i = indexOf(this.buf, CRLF);
      if (i >= 0) {
        const line = ascii(this.buf.subarray(0, i));
        this.buf = this.buf.subarray(i + 2);
        return line;
      }
      if (this.buf.length > 8192) throw new Http1Error('chunk header is absurdly long');
      if (!(await this.pull())) throw new Http1Error('the connection closed inside the response');
    }
  }

  async readSome(max: number): Promise<Uint8Array | null> {
    if (this.buf.length === 0 && !(await this.pull())) return null;
    const take = Math.min(max, this.buf.length);
    const out = this.buf.subarray(0, take);
    this.buf = this.buf.subarray(take);
    return out;
  }
}

async function* readFixed(r: ConnReader, n: number): AsyncGenerator<Uint8Array, void, void> {
  let left = n;
  while (left > 0) {
    const chunk = await r.readSome(left);
    if (chunk === null) {
      throw new Http1Error(`the connection closed after ${n - left} of ${n} body bytes`);
    }
    left -= chunk.length;
    yield chunk;
  }
}

async function* readChunked(r: ConnReader): AsyncGenerator<Uint8Array, void, void> {
  for (;;) {
    const header = await r.readLine();
    const size = Number.parseInt((header.split(';')[0] ?? '').trim(), 16);
    if (!Number.isInteger(size) || size < 0) {
      throw new Http1Error(`bad chunk size ${JSON.stringify(header.slice(0, 32))}`);
    }
    if (size === 0) {
      while ((await r.readLine()) !== '') {
        /* trailers, ignored */
      }
      return;
    }
    yield* readFixed(r, size);
    const sep = await r.readLine();
    if (sep !== '') throw new Http1Error('missing CRLF after a chunk');
  }
}

async function* readToEOF(r: ConnReader): AsyncGenerator<Uint8Array, void, void> {
  for (;;) {
    const chunk = await r.readSome(1 << 20);
    if (chunk === null) return;
    yield chunk;
  }
}

function streamOf(
  src: AsyncGenerator<Uint8Array, void, void>,
  done: () => void,
  signal?: AbortSignal | null,
): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    async pull(controller) {
      try {
        const next = await src.next();
        if (next.done) {
          done();
          controller.close();
          return;
        }
        controller.enqueue(next.value);
      } catch (err) {
        done();
        try {
          controller.error(signal?.aborted ? abortError() : err);
        } catch {
          /* the reader already cancelled; the error has nowhere to go */
        }
      }
    },
    cancel() {
      done();
      void src.return();
    },
  });
}

const CRLF = new Uint8Array([13, 10]);
const CRLF2 = new Uint8Array([13, 10, 13, 10]);

function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
  if (a.length === 0) return b;
  const out = new Uint8Array(a.length + b.length);
  out.set(a);
  out.set(b, a.length);
  return out;
}

function indexOf(hay: Uint8Array, needle: Uint8Array): number {
  outer: for (let i = 0; i + needle.length <= hay.length; i++) {
    for (let j = 0; j < needle.length; j++) if (hay[i + j] !== needle[j]) continue outer;
    return i;
  }
  return -1;
}

function ascii(bytes: Uint8Array): string {
  let s = '';
  for (const b of bytes) s += String.fromCharCode(b);
  return s;
}

export function abortError(): DOMException {
  return new DOMException('The request was stopped.', 'AbortError');
}
