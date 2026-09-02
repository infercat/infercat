import { describe, expect, it } from 'vitest';
import { chatEvents, describeError, GatewayError, getMe, sseData, type StreamEvent } from './api';
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

  // The bug this exists for: a CRLF split across two reads used to leave a stray \r on the payload,
  // which turned the [DONE] sentinel into [DONE]\r and made every reply look truncated.
  it('keeps [DONE] intact when the CRLF of its blank line is split across reads', async () => {
    const out: string[] = [];
    for await (const d of sseData(stream(['data: a\r\n\r\ndata: [DONE]\r', '\n\r\n']))) out.push(d);
    expect(out).toEqual(['a', '[DONE]']);
  });

  it('yields a trailing event that never got its blank line', async () => {
    const out: string[] = [];
    for await (const d of sseData(stream(['data: last']))) out.push(d);
    expect(out).toEqual(['last']);
  });
});

async function collect(gen: AsyncGenerator<StreamEvent, void, void>): Promise<StreamEvent[]> {
  const out: StreamEvent[] = [];
  for await (const e of gen) out.push(e);
  return out;
}

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
    const events = await collect(chatEvents(t, 'sekrit', { model: 'm', messages: [{ role: 'user', content: 'hi' }] }));

    expect(events).toEqual([
      { kind: 'reasoning', text: 'hmm' },
      { kind: 'reasoning', text: ' ok' },
      { kind: 'content', text: 'Hello' },
      { kind: 'content', text: ' world' },
      { kind: 'usage', in: 11, out: 4 },
      { kind: 'done' },
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
    const events = await collect(chatEvents(transportOf(new Response(body)), 'k', { model: 'm', messages: [] }));
    expect(events).toEqual([{ kind: 'content', text: 'a' }, { kind: 'done' }]);
  });

  it('skips an event that is not JSON rather than failing the whole reply', async () => {
    const body = stream(['data: {not json\n\n', chunk({ content: 'ok' }), 'data: [DONE]\n\n']);
    const events = await collect(chatEvents(transportOf(new Response(body)), 'k', { model: 'm', messages: [] }));
    expect(events).toEqual([{ kind: 'content', text: 'ok' }, { kind: 'done' }]);
  });

  // Promise 1: the three endings the old parser could not tell apart.
  it('reports an error member inside an already-flushed 200 stream, with its code', async () => {
    const body = stream([
      chunk({ content: 'half an ans' }),
      `data: ${JSON.stringify({ error: { message: 'The engine dropped the request.', type: 'upstream_error', code: 'upstream_error' } })}\n\n`,
    ]);
    const events = await collect(chatEvents(transportOf(new Response(body)), 'k', { model: 'm', messages: [] }));
    expect(events[0]).toEqual({ kind: 'content', text: 'half an ans' });
    expect(events[1]).toMatchObject({ kind: 'error', code: 'upstream_error' });
    expect((events[1] as { error: { title: string } }).error.title).toBe("The host's engine returned an error");
  });

  it('ends with eof when the stream stops without [DONE] and without a usage chunk', async () => {
    const body = stream([chunk({ content: 'half an ans' })]);
    const events = await collect(chatEvents(transportOf(new Response(body)), 'k', { model: 'm', messages: [] }));
    expect(events).toEqual([{ kind: 'content', text: 'half an ans' }, { kind: 'eof' }]);
  });

  it('accepts the usage-bearing final chunk as the host saying it finished', async () => {
    const body = stream([
      chunk({ content: 'done' }),
      `data: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 1, completion_tokens: 2 } })}\n\n`,
    ]);
    const events = await collect(chatEvents(transportOf(new Response(body)), 'k', { model: 'm', messages: [] }));
    expect(events.at(-1)).toEqual({ kind: 'done' });
  });

  it('turns a gateway error head into an error event with its code and Retry-After', async () => {
    const res = new Response(
      JSON.stringify({ error: { message: 'You have used 20 of 20 requests this minute.', type: 'rate_limit_error', code: 'rate_limited' } }),
      { status: 429, headers: { 'retry-after': '42' } },
    );
    const events = await collect(chatEvents(transportOf(res), 'k', { model: 'm', messages: [] }));
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({ kind: 'error', code: 'rate_limited' });
    expect((events[0] as { error: { retryAfterS?: number } }).error.retryAfterS).toBe(42);
  });

  it('turns an abort into an aborted event rather than a rejection', async () => {
    const ac = new AbortController();
    const t: Transport = {
      kind: 'direct',
      fetch: () => Promise.reject(new DOMException('stopped', 'AbortError')),
      ping: async () => null,
      close: () => {},
    };
    ac.abort();
    const events = await collect(chatEvents(t, 'k', { model: 'm', messages: [] }, ac.signal));
    expect(events).toEqual([{ kind: 'aborted' }]);
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
  it('shows our copy and the host diagnostic, never one instead of the other', () => {
    const f = describeError(new GatewayError(429, 'rate_limited', 'rate_limit_error', 'You have used 20 of 20 requests this minute.'));
    expect(f.title).toBe('You are sending faster than the host allows');
    expect(f.detail).toBe('The limit is per minute and clears on its own.');
    expect(f.hostSaid).toBe('You have used 20 of 20 requests this minute.');
  });

  it('has copy for invalid_request and not_found too', () => {
    for (const code of ['invalid_request', 'not_found']) {
      const f = describeError(new GatewayError(400, code, 'invalid_request_error', 'nope'));
      expect(f.title, code).not.toMatch(/^The host answered/);
      expect(f.detail.length, code).toBeGreaterThan(4);
    }
  });

  it('defaults a missing Retry-After to 5 s on the codes that mean "wait"', () => {
    expect(describeError(new GatewayError(429, 'rate_limited', 't', 'no')).retryAfterS).toBe(5);
    expect(describeError(new GatewayError(503, 'queue_timeout', 't', 'no')).retryAfterS).toBe(5);
    expect(describeError(new GatewayError(403, 'key_revoked', 't', 'no')).retryAfterS).toBeUndefined();
  });

  it('names an error that arrived inside a 200 stream', () => {
    expect(describeError(new GatewayError(0, '', '', 'the engine went away')).title).toBe('The host stopped the reply');
  });

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
