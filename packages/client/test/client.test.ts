import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import OpenAI from 'openai';
import { connect, GatewayError } from '../src/index';
import type { Conn, Session as BridgeSession } from '../src/transport/types';

const encoder = new TextEncoder();
const me = { key: { id: 'k_test', name: 'test', status: 'active' }, host: { models: ['fixture'] } };
const wire = (body: unknown, status = 200, headers = '') => {
  const text = typeof body === 'string' ? body : JSON.stringify(body);
  return `HTTP/1.1 ${status} Test\r\nContent-Type: application/json\r\nContent-Length: ${encoder.encode(text).length}\r\n${headers}\r\n${text}`;
};
let responses: string[];
let conns: Conn[];
let requests: string[];
let bridgeSession: BridgeSession;
let bridge: { connect: ReturnType<typeof vi.fn> };
const invite = 'ic1.tcFixture.test_secret';

beforeEach(() => {
  responses = [wire(me)]; conns = []; requests = [];
  bridgeSession = {
    addr: 'tcFixture', privateKeyJSON: 'private-fixture',
    ping: vi.fn(async () => ({ direct: false, rttMs: 42, via: 'DERP(nyc)' })),
    close: vi.fn(),
    dial: vi.fn(async () => {
      let sent = false;
      const response = responses.shift() ?? wire({ data: [] });
      const conn: Conn = {
        read: async () => { if (sent) return null; sent = true; return encoder.encode(response); },
        write: async (bytes) => { requests.push(new TextDecoder().decode(bytes)); },
        closeWrite: vi.fn(async () => {}), close: vi.fn(),
      };
      conns.push(conn); return conn;
    }),
  };
  bridge = { connect: vi.fn(async () => bridgeSession) };
  vi.stubGlobal('document', { baseURI: 'https://example.test/' });
  vi.stubGlobal('InfercatTunnel', bridge);
});
afterEach(() => vi.unstubAllGlobals());

it('connects once, authenticates /me, exposes measured status and caller-owned identity', async () => {
  const s = await connect(`  ${invite}  `, { privateKey: 'saved', derpMapURL: 'https://relay.test/map' });
  expect(bridge.connect).toHaveBeenCalledWith(expect.objectContaining({ addr: 'tcFixture', privateKey: 'saved', derpMapURL: 'https://relay.test/map' }));
  expect(s.baseURL).toBe('http://host/v1');
  expect(s.status).toEqual({ kind: 'relayed', rttMs: 42, via: 'DERP(nyc)' });
  expect(s.privateKeyJSON).toBe('private-fixture');
  expect(requests[0]).toContain('GET /me HTTP/1.1');
  expect(requests[0]).toContain('authorization: Bearer test_secret');
  responses.push(wire(me)); expect(await s.me()).toEqual(me);
  s.close(); s.close(); expect(bridgeSession.close).toHaveBeenCalledTimes(1);
});

it.each(['', 'ic2.tcA.secret', 'ic1.tcA', 'ic1..secret', 'ic1.tc.secret', 'ic1.bad.secret', 'ic1.tcA.s!', 'ic1.tcA.secret.extra'])('rejects malformed invite %j before connection', async (bad) => {
  await expect(connect(bad)).rejects.toBeInstanceOf(TypeError);
  expect(bridge.connect).not.toHaveBeenCalled();
});

it('refuses Node without installing polyfills or starting the bridge', async () => {
  vi.stubGlobal('document', undefined);
  await expect(connect(invite)).rejects.toThrow('requires a browser');
  expect(bridge.connect).not.toHaveBeenCalled();
});

it('reports direct measurements when the bridge reports them', async () => {
  vi.mocked(bridgeSession.ping).mockResolvedValue({ direct: true, rttMs: 2, via: '127.0.0.1' });
  const s = await connect(invite); expect(s.status.kind).toBe('direct'); s.close();
});

it('closes the session when authentication or the path measurement fails', async () => {
  responses = [wire({ error: { code: 'key_revoked', type: 'permission_error', message: 'revoked' } }, 403)];
  await expect(connect(invite)).rejects.toMatchObject({ name: 'GatewayError', code: 'key_revoked', status: 403 });
  expect(bridgeSession.close).toHaveBeenCalledTimes(1);
  vi.mocked(bridgeSession.ping).mockRejectedValue(new Error('path failed'));
  await expect(connect(invite)).rejects.toThrow('path failed');
  expect(bridgeSession.close).toHaveBeenCalledTimes(2);
});

it('accepts relative paths, URL and Request inputs, preserving init overrides and encoding bodies', async () => {
  const s = await connect(invite);
  await (await s.fetch('models?limit=2')).text();
  expect(requests.at(-1)).toContain('GET /v1/models?limit=2 HTTP/1.1');
  await (await s.fetch(new URL('http://host/v1/models'))).text();
  const req = new Request('http://host/v1/chat/completions', { method: 'POST', body: 'old', headers: { 'x-old': 'yes' } });
  await (await s.fetch(req, { body: new Blob(['new']), headers: { 'x-new': 'yes', authorization: 'wrong' } })).text();
  expect(requests.at(-1)).toContain('x-new: yes');
  expect(requests.at(-1)).not.toContain('x-old:');
  expect(requests.at(-1)).toContain('authorization: Bearer test_secret');
  expect(requests.at(-1)).toContain('content-length: 3');
  expect(requests.at(-1)).toMatch(/\r\n\r\nnew$/);
  const form = new FormData(); form.set('prompt', 'hello');
  await (await s.fetch('/v1/chat/completions', { method: 'POST', body: form })).text();
  expect(requests.at(-1)).toContain('multipart/form-data; boundary=');
  expect(requests.at(-1)).toContain('hello'); s.close();
});

it.each(['https://elsewhere.test/v1/models', '//elsewhere.test/me', 'http://user@host/me'])('refuses foreign or credentialed URLs %s without dialling', async (url) => {
  const s = await connect(invite);
  await expect(s.fetch(url)).rejects.toBeInstanceOf(TypeError);
  expect(requests).toHaveLength(1); s.close();
});

it('keeps native HTTP error semantics and maps typed errors only for me()', async () => {
  const s = await connect(invite);
  const failure = wire({ error: { code: 'concurrency_limited', type: 'rate_limit_error', message: 'busy', limit: 2, in_flight: 2 } }, 429, 'Retry-After: 4\r\n');
  responses.push(failure, failure);
  const r = await s.fetch('/me'); expect(r.status).toBe(429); await r.text();
  await expect(s.me()).rejects.toMatchObject({ name: 'GatewayError', code: 'concurrency_limited', retryAfterS: 4, limit: 2, inFlight: 2 });
  responses.push(wire('not JSON', 502));
  await expect(s.me()).rejects.toMatchObject({ status: 502, code: '', message: 'Host returned HTTP 502' });
  responses.push(wire({ error: { code: 'future_code', message: 'future' } }, 400));
  await expect(s.me()).rejects.toMatchObject({ code: 'future_code' }); s.close();
});

it('pre-aborted requests and calls after close never dial', async () => {
  const s = await connect(invite); const abort = new AbortController(); abort.abort();
  await expect(s.fetch('models', { signal: abort.signal })).rejects.toMatchObject({ name: 'AbortError' });
  s.close(); await expect(s.fetch('models')).rejects.toMatchObject({ name: 'AbortError' });
  expect(requests).toHaveLength(1);
});

it('close interrupts an active read and closes its connection', async () => {
  const s = await connect(invite);
  const conn: Conn = { write: async () => {}, read: () => new Promise(() => {}), closeWrite: async () => {}, close: vi.fn() };
  vi.mocked(bridgeSession.dial).mockResolvedValue(conn);
  const pending = s.fetch('models');
  await new Promise((resolve) => setTimeout(resolve, 5)); s.close();
  await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
  expect(conn.close).toHaveBeenCalledTimes(1);
});

describe('official OpenAI SDK', () => {
  it('lists models, streams completion events, and retains gateway error codes', async () => {
    const s = await connect(invite); const sdk = new OpenAI(s.openai());
    responses.push(wire({ object: 'list', data: [{ id: 'fixture', object: 'model', created: 0, owned_by: 'host' }] }));
    expect((await sdk.models.list()).data[0]?.id).toBe('fixture');
    const chunk = (text: string) => `data: ${JSON.stringify({ id: 'chat', object: 'chat.completion.chunk', created: 0, model: 'fixture', choices: [{ index: 0, delta: { content: text }, finish_reason: null }] })}\n\n`;
    responses.push(wire(chunk('Hello') + chunk(' world') + 'data: [DONE]\n\n', 200, 'Content-Type: text/event-stream\r\n'));
    const stream = await sdk.chat.completions.create({ model: 'fixture', messages: [{ role: 'user', content: 'Hi' }], stream: true });
    let result = ''; for await (const event of stream) result += event.choices[0]?.delta.content ?? '';
    expect(result).toBe('Hello world');
    expect(requests.at(-1)).toContain('POST /v1/chat/completions HTTP/1.1');
    responses.push(wire({ error: { code: 'budget_exhausted', type: 'rate_limit_error', message: 'spent' } }, 429));
    await expect(sdk.models.list()).rejects.toMatchObject({ status: 429, code: 'budget_exhausted' });
    expect(requests).toHaveLength(4); // no automatic replay
    expect(s.openai().apiKey).toBe('test_secret');
    expect(GatewayError.prototype).toBeInstanceOf(Error); s.close();
  });
});

it('close cancels a pending upload producer before any request is dialled', async () => {
  const s = await connect(invite); const cancel = vi.fn();
  const body = new ReadableStream<Uint8Array>({ cancel });
  const request = new Request('http://host/v1/chat/completions', {
    method: 'POST', body, duplex: 'half',
  } as RequestInit);
  const pending = s.fetch(request);
  await new Promise((resolve) => setTimeout(resolve, 5)); s.close();
  await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
  expect(cancel).toHaveBeenCalledTimes(1); expect(requests).toHaveLength(1);
});

it('cancelling a response body closes the request connection without closing its session', async () => {
  const s = await connect(invite);
  const response = await s.fetch('models');
  const conn = conns.at(-1)!;
  await response.body!.cancel();
  expect(conn.close).toHaveBeenCalledTimes(1);
  expect(bridgeSession.close).not.toHaveBeenCalled(); s.close();
});
