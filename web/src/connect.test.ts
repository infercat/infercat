// The whole connect flow, end to end, over the in-page fake of the wasm bridge: stages, a real
// HTTP/1.1 conversation on a fake Conn, /me, /v1/models, and a streamed reply with reasoning,
// content and usage. This is the test that would catch a break in the seam ticket 001 owns.
import { afterEach, describe, expect, it } from 'vitest';
import { installFakeTunnel, OFFLINE_ADDR_PREFIX } from '../dev/fake-bunny-tunnel.ts';
import { getMe, getModels, GatewayError, streamChat, type ChatDelta } from './api';
import { decodeInvite } from './invite';
import { describePath, openTransport, TunnelTransport, type ConnectStage } from './transport';

const INVITE = `bn1.tcFAKEaddressFAKEaddressFAKEaddress.${'s'.repeat(43)}`;
const FAST = { connectMs: 10, tokenDelayMs: 0, ttftMs: 0 };

afterEach(() => {
  delete (globalThis as { BunnyTunnel?: unknown }).BunnyTunnel;
});

async function connect() {
  installFakeTunnel(FAST);
  const { addr, secret } = decodeInvite(INVITE);
  const stages: ConnectStage[] = [];
  const logs: string[] = [];
  const opened = await openTransport(addr, {
    mode: 'tunnel',
    onStage: (s) => stages.push(s),
    onLog: (l) => logs.push(l),
  });
  return { ...opened, secret, stages, logs };
}

describe('connect flow', () => {
  it('walks the stages, measures the path, and keeps an identity to persist', async () => {
    const { transport, path, privateKeyJSON, stages, logs } = await connect();

    expect(stages.map((s) => s.name)).toEqual(['wasm', 'relay', 'handshake']);
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

  it('streams a reply through the tunnel: reasoning first, then content, then usage', async () => {
    const { transport, secret } = await connect();
    const deltas: ChatDelta[] = [];
    await streamChat(
      transport,
      secret,
      { model: 'gemma-4-e2b-it', messages: [{ role: 'user', content: 'hello there' }] },
      (d) => deltas.push(d),
    );

    const reasoning = deltas.filter((d) => d.reasoning !== undefined);
    const content = deltas.filter((d) => d.content !== undefined);
    const usage = deltas.filter((d) => d.usage !== undefined);
    expect(reasoning.length).toBeGreaterThan(3);
    expect(content.length).toBeGreaterThan(3);
    expect(usage).toHaveLength(1);
    // Order matters: the Thinking block must fill before the answer starts.
    expect(deltas.indexOf(reasoning.at(-1)!)).toBeLessThan(deltas.indexOf(content[0]!));
    expect(deltas.at(-1)).toBe(usage[0]);
    expect(content.map((d) => d.content).join('')).toContain('hello there');
    expect(usage[0]?.usage?.completion_tokens).toBeGreaterThan(0);
    transport.close();
  });

  it('surfaces a gateway 429 with its code and Retry-After', async () => {
    const { transport, secret } = await connect();
    const err = await streamChat(
      transport,
      secret,
      { model: 'm', messages: [{ role: 'user', content: '/429 please' }] },
      () => {},
    ).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(GatewayError);
    expect(err).toMatchObject({ status: 429, code: 'rate_limited', retryAfterS: 42 });
    transport.close();
  });

  it('stopping mid-stream aborts the request and closes the conn', async () => {
    const { transport, secret } = await connect();
    const ac = new AbortController();
    const deltas: ChatDelta[] = [];
    const err = await streamChat(
      transport,
      secret,
      { model: 'm', messages: [{ role: 'user', content: 'hello' }] },
      (d) => {
        deltas.push(d);
        if (deltas.length === 3) ac.abort();
      },
      ac.signal,
    ).catch((e: unknown) => e);
    expect((err as DOMException).name).toBe('AbortError');
    expect(deltas).toHaveLength(3);
    transport.close();
  });

  it('reports a host that never answers instead of hanging', async () => {
    installFakeTunnel(FAST);
    await expect(
      openTransport(`${OFFLINE_ADDR_PREFIX}xyz`, { mode: 'tunnel' }),
    ).rejects.toThrow(/did not answer/);
  });

  it('refuses ports other than the gateway port (Protection 1)', async () => {
    const { transport } = await connect();
    const session = (transport as TunnelTransport).session;
    await expect(session.dial(81)).rejects.toThrow(/nothing is listening/);
    transport.close();
  });
});
