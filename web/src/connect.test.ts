// The whole connect flow, end to end, over the in-page fake of the wasm bridge: stages, a real
// HTTP/1.1 conversation on a fake Conn, /me, /v1/models, and a streamed reply with reasoning,
// content and usage. This is the test that would catch a break in the seam ticket 001 owns.
import { afterEach, describe, expect, it } from 'vitest';
import { installFakeTunnel, OFFLINE_ADDR_PREFIX } from '../dev/fake-bunny-tunnel.ts';
import { chatEvents, getMe, getModels, logsPrompts, type StreamEvent } from './api';
import { decodeInvite } from './invite';
import { describePath, openTransport, TunnelTransport, type Transport } from './transport';
import { NEW_REPLY, reduceReply } from './stream';

const INVITE = `bn1.tcFAKEaddressFAKEaddressFAKEaddress.${'s'.repeat(43)}`;
const FAST = { connectMs: 10, tokenDelayMs: 0, ttftMs: 0 };

afterEach(() => {
  delete (globalThis as { BunnyTunnel?: unknown }).BunnyTunnel;
});

async function connect(opts: Record<string, unknown> = {}) {
  installFakeTunnel({ ...FAST, ...opts });
  const { addr, secret } = decodeInvite(INVITE);
  const stages: string[] = [];
  const logs: string[] = [];
  const opened = await openTransport(addr, {
    mode: 'tunnel',
    onWasmProgress: () => stages.push('wasm'),
    onWasmLoaded: () => stages.push('relay'),
    onLog: (l) => logs.push(l),
  });
  return { ...opened, secret, stages, logs };
}

/** What the chat screen does with a reply: play the events through the message reducer. */
async function reply(t: Transport, secret: string, prompt: string, signal?: AbortSignal) {
  const events: StreamEvent[] = [];
  let r = NEW_REPLY;
  for await (const e of chatEvents(t, secret, { model: 'm', messages: [{ role: 'user', content: prompt }] }, signal)) {
    events.push(e);
    r = reduceReply(r, e);
  }
  return { events, reply: r };
}

describe('connect flow', () => {
  it('walks the stages, measures the path, and keeps an identity to persist', async () => {
    const { transport, path, privateKeyJSON, stages, logs } = await connect();

    expect(stages).toEqual(['wasm', 'relay']);
    expect(transport).toBeInstanceOf(TunnelTransport);
    expect(path?.direct).toBe(false);
    expect(path?.via).toBe('DERP(sfo)');
    expect(describePath(path)).toMatch(/^relayed via sfo · \d+ ms$/);
    expect(privateKeyJSON).toBeTruthy();
    expect(logs.length).toBeGreaterThan(0);
    transport.close();
  });

  it('verifies the invite with /me and reads the model list', async () => {
    const { transport, secret } = await connect();
    const me = await getMe(transport, secret);
    expect(me.key.status).toBe('active');
    expect(me.host.relay.region).toBe('sfo');
    expect(me.limits.rpm).toBeGreaterThan(0);
    expect(await getModels(transport, secret)).toEqual(me.host.models);
    transport.close();
  });

  // Promise 12: absent field means the host is not logging; the client only says so when told.
  it('reads host.log_prompts, treating an absent field as false', async () => {
    const quiet = await connect();
    expect(logsPrompts(await getMe(quiet.transport, quiet.secret))).toBe(false);
    quiet.transport.close();

    const loud = await connect({ logPrompts: true });
    expect(logsPrompts(await getMe(loud.transport, loud.secret))).toBe(true);
    loud.transport.close();
  });

  it('streams a reply through the tunnel: reasoning first, then content, then usage, then done', async () => {
    const { transport, secret } = await connect();
    const { events, reply: r } = await reply(transport, secret, 'hello there');

    const reasoning = events.filter((e) => e.kind === 'reasoning');
    const content = events.filter((e) => e.kind === 'content');
    expect(reasoning.length).toBeGreaterThan(3);
    expect(content.length).toBeGreaterThan(3);
    // Order matters: the Thinking block must fill before the answer starts.
    expect(events.indexOf(reasoning.at(-1)!)).toBeLessThan(events.indexOf(content[0]!));
    expect(events.at(-2)).toMatchObject({ kind: 'usage' });
    expect(events.at(-1)).toEqual({ kind: 'done' });
    expect(r.status).toBe('complete');
    expect(r.content).toContain('hello there');
    expect(r.tokens?.out).toBeGreaterThan(0);
    transport.close();
  });

  // Promise 1, through the real HTTP/1.1 path rather than a hand-built stream.
  it('a stream that ends without [DONE] leaves the message interrupted, not complete', async () => {
    const { transport, secret } = await connect();
    const { events, reply: r } = await reply(transport, secret, '/cut this one short');
    expect(events.at(-1)).toEqual({ kind: 'eof' });
    expect(r.status).toBe('interrupted');
    expect(r.content.length).toBeGreaterThan(0); // the partial answer is kept
    expect(r.note).toMatch(/only part of it/);
    transport.close();
  });

  it('an error inside a flushed 200 stream interrupts with the host’s code', async () => {
    const { transport, secret } = await connect();
    const { events, reply: r } = await reply(transport, secret, '/mid stream failure');
    expect(events.at(-1)).toMatchObject({ kind: 'error', code: 'upstream_error' });
    expect(r.status).toBe('interrupted');
    expect(r.note).toMatch(/engine/);
    transport.close();
  });

  // Promise 2.
  it('a reply that is all thinking is no_answer, never an empty bubble', async () => {
    const { transport, secret } = await connect();
    const { reply: r } = await reply(transport, secret, '/think about it');
    expect(r.status).toBe('no_answer');
    expect(r.content).toBe('');
    expect(r.reasoning?.length ?? 0).toBeGreaterThan(0);
    expect(r.note).toMatch(/never got to an answer/);
    transport.close();
  });

  it('surfaces a gateway 429 with its code and Retry-After', async () => {
    const { transport, secret } = await connect();
    const { events } = await reply(transport, secret, '/429 please');
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({ kind: 'error', code: 'rate_limited' });
    expect((events[0] as { error: { retryAfterS?: number } }).error.retryAfterS).toBe(42);
    transport.close();
  });

  it('stopping mid-stream aborts the request and marks the message stopped', async () => {
    const { transport, secret } = await connect();
    const ac = new AbortController();
    const events: StreamEvent[] = [];
    let r = NEW_REPLY;
    for await (const e of chatEvents(
      transport,
      secret,
      { model: 'm', messages: [{ role: 'user', content: 'hello' }] },
      ac.signal,
    )) {
      events.push(e);
      r = reduceReply(r, e);
      if (events.length === 3) ac.abort();
    }
    expect(events.at(-1)).toEqual({ kind: 'aborted' });
    expect(r.status).toBe('stopped');
    transport.close();
  });

  it('reports a host that never answers instead of hanging', async () => {
    installFakeTunnel(FAST);
    await expect(
      openTransport(`${OFFLINE_ADDR_PREFIX}xyz`, { mode: 'tunnel' }),
    ).rejects.toThrow(/did not answer/);
  });

  it('leaves the path unmeasured, rather than guessed, when the ping fails', async () => {
    const { transport, path } = await connect({ pingFails: true });
    expect(path).toBeNull();
    expect(describePath(path, 'sfo')).toBe('relayed via sfo');
    transport.close();
  });

  it('refuses ports other than the gateway port (Protection 1)', async () => {
    const { transport } = await connect();
    const session = (transport as TunnelTransport).session;
    await expect(session.dial(81)).rejects.toThrow(/nothing is listening/);
    transport.close();
  });
});
