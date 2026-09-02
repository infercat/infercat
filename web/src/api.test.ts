import { describe, expect, it } from 'vitest';
import { describeError, GatewayError, getMe, sseData, streamChat, type ChatDelta } from './api';
import type { Transport } from './transport';

function stream(parts: string[], gapMs = 0): ReadableStream<Uint8Array> {
  const enc = new TextEncoder();
  let i = 0;
  return new ReadableStream({
    async pull(c) {
      if (i >= parts.length) {
        c.close();
        return;
      }
      if (gapMs) await new Promise((r) => setTimeout(r, gapMs));
      c.enqueue(enc.encode(parts[i++] as string));
    },
  });
}

function transportOf(res: Response, seen: { path?: string; init?: RequestInit } = {}): Transport {
  return {
    kind: 'direct',
    fetch: (path, init) => {
      seen.path = path;
      seen.init = init;
      return Promise.resolve(res);
    },
    ping: async () => null,
    close: () => {},
  };
}

describe('sseData', () => {
  it('splits events on blank lines and strips one leading space', async () => {
    const out: string[] = [];
    for await (const d of sseData(stream(['data: one\n\ndata:two\n\n']))) out.push(d);
    expect(out).toEqual(['one', 'two']);
  });

  it('reassembles events split across reads and accepts CRLF', async () => {
    const out: string[] = [];
    for await (const d of sseData(stream(['data: {"a"', ':1}\r\n', '\r\ndata: [DONE]\n\n']))) out.push(d);
    expect(out).toEqual(['{"a":1}', '[DONE]']);
  });

  it('joins multi-line data and ignores comment and event lines', async () => {
    const out: string[] = [];
    for await (const d of sseData(stream([': keep-alive\nevent: x\ndata: a\ndata: b\n\n']))) out.push(d);
    expect(out).toEqual(['a\nb']);
  });

  it('yields a trailing event that never got its blank line', async () => {
    const out: string[] = [];
    for await (const d of sseData(stream(['data: last']))) out.push(d);
    expect(out).toEqual(['last']);
  });
});

const chunk = (delta: Record<string, string>) =>
  `data: ${JSON.stringify({ object: 'chat.completion.chunk', choices: [{ delta }] })}\n\n`;

describe('streamChat', () => {
  it('reports reasoning, then content, then usage, in arrival order', async () => {
    const body = stream([
      chunk({ reasoning_content: 'hmm' }),
      chunk({ reasoning_content: ' ok' }),
      chunk({ content: 'Hello' }),
      chunk({ content: ' world' }),
      `data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 11, completion_tokens: 4 } })}\n\n`,
      'data: [DONE]\n\n',
    ]);
    const seen: { path?: string; init?: RequestInit } = {};
    const t = transportOf(new Response(body, { status: 200 }), seen);
    const deltas: ChatDelta[] = [];
    await streamChat(t, 'sekrit', { model: 'm', messages: [{ role: 'user', content: 'hi' }] }, (d) => deltas.push(d));

    expect(deltas).toEqual([
      { reasoning: 'hmm' },
      { reasoning: ' ok' },
      { content: 'Hello' },
      { content: ' world' },
      { usage: { prompt_tokens: 11, completion_tokens: 4 } },
    ]);
    expect(seen.path).toBe('/v1/chat/completions');
    expect(new Headers(seen.init?.headers).get('authorization')).toBe('Bearer sekrit');
    const sent = JSON.parse(String(seen.init?.body));
    expect(sent).toMatchObject({ model: 'm', stream: true });
    // 005 fix 10e: no max_tokens unless the user set one, so the key's clamp is the only cap.
    expect(sent).not.toHaveProperty('max_tokens');
    expect(sent).not.toHaveProperty('max_completion_tokens');
  });

  it('stops at [DONE] and ignores anything after it', async () => {
    const body = stream([chunk({ content: 'a' }), 'data: [DONE]\n\n', chunk({ content: 'b' })]);
    const deltas: ChatDelta[] = [];
    await streamChat(transportOf(new Response(body)), 'k', { model: 'm', messages: [] }, (d) => deltas.push(d));
    expect(deltas).toEqual([{ content: 'a' }]);
  });

  it('skips an event that is not JSON rather than failing the whole reply', async () => {
    const body = stream(['data: {not json\n\n', chunk({ content: 'ok' })]);
    const deltas: ChatDelta[] = [];
    await streamChat(transportOf(new Response(body)), 'k', { model: 'm', messages: [] }, (d) => deltas.push(d));
    expect(deltas).toEqual([{ content: 'ok' }]);
  });

  it('turns a gateway error body into a GatewayError with its code and Retry-After', async () => {
    const res = new Response(
      JSON.stringify({ error: { message: 'You have used 20 of 20 requests this minute.', type: 'rate_limit_error', code: 'rate_limited' } }),
      { status: 429, headers: { 'retry-after': '42' } },
    );
    await expect(
      streamChat(transportOf(res), 'k', { model: 'm', messages: [] }, () => {}),
    ).rejects.toMatchObject({ status: 429, code: 'rate_limited', retryAfterS: 42 });
  });
});

describe('getMe', () => {
  it('sends the bearer and returns the documented shape', async () => {
    const me = {
      key: { id: 'k_1', name: 'alice', status: 'active' },
      limits: { rpm: 20, tpm: 20000, max_concurrent: 1, max_output_tokens: 2048, max_context: 0, daily_tokens: 200000 },
      usage: { rpm_used: 1, tpm_used: 2, today_tokens: 3, in_flight: 0 },
      host: { name: 'desk', upstream: { kind: 'llama.cpp', healthy: true, model_context: 8192 }, models: ['m'], relay: { region: 'sfo' } },
    };
    const seen: { path?: string; init?: RequestInit } = {};
    const got = await getMe(transportOf(new Response(JSON.stringify(me)), seen), 'sekrit');
    expect(got).toEqual(me);
    expect(new Headers(seen.init?.headers).get('authorization')).toBe('Bearer sekrit');
  });
});

describe('describeError', () => {
  it('gives every documented gateway code a title and a next step', () => {
    const codes = [
      'invalid_key', 'key_paused', 'key_revoked', 'model_not_allowed', 'body_too_large',
      'context_too_long', 'rate_limited', 'concurrency_limited', 'budget_exhausted',
      'queue_timeout', 'upstream_down', 'upstream_error',
    ];
    for (const code of codes) {
      const f = describeError(new GatewayError(429, code, 'x', 'because reasons'));
      expect(f.title.length, code).toBeGreaterThan(4);
      expect(f.detail.length, code).toBeGreaterThan(4);
    }
  });

  it('marks the three codes that invalidate the invite as fatal', () => {
    for (const code of ['invalid_key', 'key_paused', 'key_revoked']) {
      expect(describeError(new GatewayError(403, code, 'permission_error', 'no')).fatal).toBe(true);
    }
    expect(describeError(new GatewayError(429, 'rate_limited', 'rate_limit_error', 'no')).fatal).toBeUndefined();
  });

  it('passes a Retry-After through for the countdown', () => {
    expect(describeError(new GatewayError(429, 'rate_limited', 't', 'no', 42)).retryAfterS).toBe(42);
  });

  it('recognises a stop as a stop, not a failure', () => {
    expect(describeError(new DOMException('x', 'AbortError')).title).toBe('Stopped');
  });

  it('falls back to the status when the host does not use the documented shape', () => {
    expect(describeError(new GatewayError(500, '', '', 'boom')).title).toBe('The host answered 500');
  });
});
