import { describe, expect, it } from 'vitest';
import { Http1Error, encodeRequest, fetchOverConn } from './http1';
import type { Conn } from './types';

const enc = (s: string) => new TextEncoder().encode(s);

/**
 * A Conn that hands back exactly the byte runs it was scripted with, in order. With `blockAtEnd`
 * it then leaves reads pending forever (until closed), the way a live stream that has not produced
 * its next event does — which is how "did it buffer?" becomes a testable question.
 */
class ScriptedConn implements Conn {
  readonly written: Uint8Array[] = [];
  closed = false;
  private readonly queue: (string | Uint8Array)[];
  private waiting: ((v: Uint8Array | null) => void) | null = null;

  constructor(
    chunks: (string | Uint8Array)[],
    private readonly blockAtEnd = false,
  ) {
    this.queue = [...chunks];
  }

  async write(data: Uint8Array): Promise<void> {
    this.written.push(data);
  }
  async closeWrite(): Promise<void> {}
  read(): Promise<Uint8Array | null> {
    const next = this.queue.shift();
    if (next !== undefined) {
      return Promise.resolve(typeof next === 'string' ? enc(next) : next);
    }
    if (this.blockAtEnd && !this.closed) {
      return new Promise((r) => (this.waiting = r));
    }
    return Promise.resolve(null);
  }
  close(): void {
    this.closed = true;
    this.waiting?.(null);
    this.waiting = null;
  }
  get request(): string {
    return this.written.map((b) => new TextDecoder().decode(b)).join('');
  }
}

const GET = { method: 'GET', path: '/me', headers: new Headers({ host: 'bunny' }) };

describe('encodeRequest', () => {
  it('writes a request line, headers, and the body', () => {
    const bytes = encodeRequest({
      method: 'post',
      path: '/v1/chat/completions',
      headers: new Headers({ host: 'bunny', 'content-type': 'application/json' }),
      body: enc('{"a":1}'),
    });
    const text = new TextDecoder().decode(bytes);
    expect(text.startsWith('POST /v1/chat/completions HTTP/1.1\r\n')).toBe(true);
    expect(text).toContain('host: bunny\r\n');
    expect(text).toContain('content-type: application/json\r\n');
    expect(text.endsWith('\r\n\r\n{"a":1}')).toBe(true);
  });
});

describe('fetchOverConn', () => {
  it('parses a content-length response whose body is split across reads', async () => {
    const conn = new ScriptedConn([
      'HTTP/1.1 200 OK\r\ncontent-type: application/json\r\ncontent-length: 11\r\n\r\n{"ok"',
      ':tr',
      'ue}',
    ]);
    const res = await fetchOverConn(conn, GET);
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ ok: true });
    expect(conn.closed).toBe(true);
    expect(conn.request.startsWith('GET /me HTTP/1.1\r\n')).toBe(true);
  });

  it('parses headers that arrive split mid-line', async () => {
    const conn = new ScriptedConn([
      'HTTP/1.1 429 Too Many Re',
      'quests\r\nRetry-Af',
      'ter: 42\r\ncontent-length: 2\r',
      '\n\r\n{}',
    ]);
    const res = await fetchOverConn(conn, GET);
    expect(res.status).toBe(429);
    // Header lookup is case-insensitive whatever case the host used.
    expect(res.headers.get('retry-after')).toBe('42');
    expect(res.headers.get('RETRY-AFTER')).toBe('42');
    expect(await res.text()).toBe('{}');
  });

  it('decodes a chunked body, including chunks split across reads and trailers', async () => {
    const conn = new ScriptedConn([
      'HTTP/1.1 200 OK\r\ntransfer-encoding: chunked\r\n\r\n',
      '5\r\nhel',
      'lo\r\n6\r\n world\r',
      '\n0\r\nx-trailer: 1\r\n\r\n',
    ]);
    const res = await fetchOverConn(conn, GET);
    expect(await res.text()).toBe('hello world');
    expect(conn.closed).toBe(true);
  });

  it('reads a close-delimited body when the host frames nothing', async () => {
    const conn = new ScriptedConn(['HTTP/1.1 200 OK\r\ncontent-type: text/plain\r\n\r\nab', 'cd']);
    const res = await fetchOverConn(conn, GET);
    expect(await res.text()).toBe('abcd');
  });

  it('yields body bytes as they arrive rather than buffering to completion', async () => {
    // The conn never ends: after the first SSE-sized chunk it leaves reads pending. A reader that
    // still gets "first" back has provably not waited for the response to finish.
    const conn = new ScriptedConn(
      ['HTTP/1.1 200 OK\r\ntransfer-encoding: chunked\r\n\r\n', '5\r\nfirst\r\n'],
      true,
    );
    const res = await fetchOverConn(conn, GET);
    const reader = res.body!.getReader();
    const first = await reader.read();
    expect(new TextDecoder().decode(first.value)).toBe('first');
    await reader.cancel();
    expect(conn.closed).toBe(true);
  });

  it('errors the body stream when the connection dies mid-body', async () => {
    const conn = new ScriptedConn(['HTTP/1.1 200 OK\r\ncontent-length: 10\r\n\r\nabc']);
    const res = await fetchOverConn(conn, GET);
    await expect(res.text()).rejects.toThrow(/closed after 3 of 10/);
    expect(conn.closed).toBe(true);
  });

  it('rejects when the connection closes before any response', async () => {
    await expect(fetchOverConn(new ScriptedConn([]), GET)).rejects.toBeInstanceOf(Http1Error);
  });

  it('rejects a reply that is not HTTP', async () => {
    const conn = new ScriptedConn(['not http at all\r\n\r\n']);
    await expect(fetchOverConn(conn, GET)).rejects.toThrow(/did not answer with HTTP/);
  });

  it('gives 204 a null body and closes the conn', async () => {
    const conn = new ScriptedConn(['HTTP/1.1 204 No Content\r\n\r\n']);
    const res = await fetchOverConn(conn, GET);
    expect(res.status).toBe(204);
    expect(res.body).toBeNull();
    expect(conn.closed).toBe(true);
  });

  it('aborting closes the conn and errors the stream', async () => {
    const ac = new AbortController();
    const conn = new ScriptedConn(['HTTP/1.1 200 OK\r\ntransfer-encoding: chunked\r\n\r\n'], true);
    const res = await fetchOverConn(conn, GET, ac.signal);
    const read = res.body!.getReader().read();
    ac.abort();
    await expect(read).rejects.toMatchObject({ name: 'AbortError' });
    expect(conn.closed).toBe(true);
  });

  it('refuses to start on an already-aborted signal', async () => {
    const ac = new AbortController();
    ac.abort();
    const conn = new ScriptedConn([], true);
    await expect(fetchOverConn(conn, GET, ac.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(conn.closed).toBe(true);
  });

  it('aborting while waiting for the response head rejects', async () => {
    const ac = new AbortController();
    const conn = new ScriptedConn([], true);
    const p = fetchOverConn(conn, GET, ac.signal);
    ac.abort();
    await expect(p).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('rejects a bad chunk size instead of hanging', async () => {
    const conn = new ScriptedConn(['HTTP/1.1 200 OK\r\ntransfer-encoding: chunked\r\n\r\nzz\r\n']);
    const res = await fetchOverConn(conn, GET);
    await expect(res.text()).rejects.toThrow(/bad chunk size/);
  });

  it('rejects a bad Content-Length', async () => {
    const conn = new ScriptedConn(['HTTP/1.1 200 OK\r\ncontent-length: eight\r\n\r\n']);
    await expect(fetchOverConn(conn, GET)).rejects.toThrow(/bad Content-Length/);
  });
});

// 014 promise 1: the deadline above this reader is only as good as the reader's own abort. A
// tunnel conn whose close() does not settle a pending read used to hold the request open until the
// relay gave up — which is how a 15 s deadline landed at 47 s against a real, killed host.
describe('aborting a read that never settles', () => {
  /** A conn that answers the head and then goes quiet for ever, however hard it is closed. */
  function silentAfterHead(): Conn {
    let served = false;
    return {
      write: () => Promise.resolve(),
      closeWrite: () => Promise.resolve(),
      close: () => {},
      read: () => {
        if (served) return new Promise<Uint8Array | null>(() => {});
        served = true;
        return Promise.resolve(
          new TextEncoder().encode(
            'HTTP/1.1 200 OK\r\ncontent-type: text/event-stream\r\ntransfer-encoding: chunked\r\n\r\n',
          ),
        );
      },
    };
  }

  /** A conn to a peer that has gone away: it takes the write and never settles it. */
  function silentFromTheStart(): Conn {
    return {
      write: () => new Promise<void>(() => {}),
      closeWrite: () => Promise.resolve(),
      close: () => {},
      read: () => new Promise<Uint8Array | null>(() => {}),
    };
  }

  it('rejects a request whose write never settles, at the signal, not at the relay timeout', async () => {
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 10);
    const started = Date.now();
    await expect(
      fetchOverConn(silentFromTheStart(), { method: 'GET', path: '/x', headers: new Headers() }, ac.signal),
    ).rejects.toMatchObject({ name: 'AbortError' });
    expect(Date.now() - started).toBeLessThan(1000);
  });

  it('rejects as soon as the signal fires, not when the conn eventually gives up', async () => {
    const ac = new AbortController();
    const res = await fetchOverConn(silentAfterHead(), { method: 'GET', path: '/x', headers: new Headers() }, ac.signal);
    const reader = (res.body as ReadableStream<Uint8Array>).getReader();
    const read = reader.read();
    setTimeout(() => ac.abort(), 10);
    const started = Date.now();
    await expect(read).rejects.toMatchObject({ name: 'AbortError' });
    expect(Date.now() - started).toBeLessThan(1000);
  });
});
