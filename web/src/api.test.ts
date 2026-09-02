import { describe, expect, it } from 'vitest';
import {
  chatEvents,
  describeError,
  GatewayError,
  getMe,
  modelLabel,
  needsRedial,
  sseData,
  type StreamEvent,
} from './api';
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
    const f = describeError(
      new GatewayError(429, 'rate_limited', 'rate_limit_error', 'You have used 20 of 20 requests this minute.'),
      "Max's laptop",
    );
    expect(f.title).toBe('Too fast for this invite');
    // 014 promise 1: the copy names the machine that let the reader down.
    expect(f.detail).toContain("Max's laptop");
    expect(f.hostSaid).toBe('You have used 20 of 20 requests this minute.');
  });

  // 014 promise 1: a nameless host still makes a sentence, and never a hole in one.
  it('says "your host" when the host has no name', () => {
    expect(describeError(new GatewayError(429, 'rate_limited', 't', 'no')).detail).toContain('your host');
    expect(describeError(new GatewayError(429, 'rate_limited', 't', 'no'), '  ').detail).not.toContain('{host}');
  });

  // 014 promise 1: a raw transport string is evidence behind a disclosure, never primary copy.
  it('keeps a raw transport string out of the primary copy', () => {
    const f = describeError(new Error('dial port 80: context deadline exceeded'), 'desk');
    expect(f.title).not.toContain('deadline');
    expect(f.detail).not.toContain('deadline');
    expect(f.hostSaid).toBe('dial port 80: context deadline exceeded');
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
    expect(describeError(new GatewayError(0, '', '', 'the engine went away'), 'desk').title).toBe('desk stopped the reply');
  });

  it('gives every documented gateway code a title and a next step', () => {
    const codes = [
      'invalid_key', 'key_paused', 'key_revoked', 'model_not_allowed', 'body_too_large',
      'context_too_long', 'rate_limited', 'concurrency_limited', 'budget_exhausted',
      'queue_timeout', 'upstream_down', 'upstream_error', 'host_asleep',
    ];
    for (const code of codes) {
      const f = describeError(new GatewayError(429, code, 'x', 'because reasons'));
      expect(f.title.length, code).toBeGreaterThan(4);
      expect(f.detail.length, code).toBeGreaterThan(4);
    }
  });

  // 014 promise 2: only the two codes no waiting can fix eject the reader. A pause is a pause.
  it('marks only revoked and unrecognised invites as fatal; paused is recoverable', () => {
    for (const code of ['invalid_key', 'key_revoked']) {
      expect(describeError(new GatewayError(403, code, 'permission_error', 'no')).fatal).toBe(true);
    }
    const paused = describeError(new GatewayError(403, 'key_paused', 'permission_error', 'no'));
    expect(paused.fatal).toBeUndefined();
    expect(paused.paused).toBe(true);
    expect(describeError(new GatewayError(429, 'rate_limited', 'rate_limit_error', 'no')).fatal).toBeUndefined();
  });

  it('passes a Retry-After through for the countdown', () => {
    expect(describeError(new GatewayError(429, 'rate_limited', 't', 'no', 42)).retryAfterS).toBe(42);
  });

  it('recognises a stop as a stop, not a failure', () => {
    expect(describeError(new DOMException('x', 'AbortError')).title).toBe('Stopped');
  });

  it('falls back to the status when the host does not use the documented shape', () => {
    expect(describeError(new GatewayError(500, '', '', 'boom'), 'desk').title).toBe('desk answered 500');
  });
});

// 014 promise 1, the blocker. A host that is asleep must be named as asleep — after saying so once
// while we wait — and a host that is merely busy must never be called asleep.
describe('a host that does not answer', () => {
  /** A transport whose request never answers. It honours abort, as both real transports do. */
  function silent(): Transport {
    return {
      kind: 'tunnel',
      fetch: (_path, init) =>
        new Promise<Response>((_resolve, reject) => {
          const signal = init?.signal;
          if (signal?.aborted) reject(new DOMException('aborted', 'AbortError'));
          signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }),
      ping: () => Promise.resolve({ rttMs: 40, via: 'DERP(nyc)', direct: false }),
      close: () => {},
    };
  }

  /** A transport that answers the head at once and then streams nothing: a slow model, not a dead
   *  host. This is the case the deadline must never touch. */
  function slow(): Transport {
    return {
      kind: 'tunnel',
      fetch: (_path, init) => {
        const signal = init?.signal;
        const body = new ReadableStream<Uint8Array>({
          start(c) {
            signal?.addEventListener('abort', () => c.error(new DOMException('aborted', 'AbortError')));
          },
        });
        return Promise.resolve(new Response(body, { status: 200 }));
      },
      ping: () => Promise.resolve(null),
      close: () => {},
    };
  }
  const req = { model: 'm', messages: [{ role: 'user' as const, content: 'hi' }] };
  const fast = { noticeMs: 20, answerMs: 60 };

  it('says it is still waiting before it gives up', async () => {
    const kinds: StreamEvent['kind'][] = [];
    for await (const ev of chatEvents(silent(), 's', req, undefined, fast, 'desk')) {
      kinds.push(ev.kind);
    }
    expect(kinds[0]).toBe('waiting');
  });

  it('gives up as host_asleep, in the host\u2019s name, not as a stop or a dial string', async () => {
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(silent(), 's', req, undefined, fast, 'desk')) seen.push(ev);
    const last = seen[seen.length - 1];
    expect(last?.kind).toBe('error');
    if (last?.kind !== 'error') throw new Error('unreachable');
    expect(last.code).toBe('host_asleep');
    expect(last.error.title).toContain('desk');
    expect(last.error.detail).toContain('asleep or offline');
    expect(seen.some((e) => e.kind === 'aborted')).toBe(false);
  });

  // The deadline is on the *host*, never on the model: once a response head has arrived the host
  // is demonstrably awake, and a GPU thinking for two minutes must never be called offline.
  it('never touches a host that answered, however slow the tokens are', async () => {
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 200); // long past answerMs
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(slow(), 's', req, ac.signal, fast, 'desk')) seen.push(ev);
    expect(seen.map((e) => e.kind)).toEqual(['waiting', 'aborted']);
    expect(seen.some((e) => e.kind === 'error')).toBe(false);
  });

  it('a reader pressing Stop is still a stop, not a sleeping host', async () => {
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 20);
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(silent(), 's', req, ac.signal, fast, 'desk')) seen.push(ev);
    expect(seen[seen.length - 1]?.kind).toBe('aborted');
  });
});

// 014 promise 11: a model id is a filename; a reader wants the model.
describe('modelLabel', () => {
  it('reads a build artefact as the model it is', () => {
    expect(modelLabel('gemma-4-E2B-it-Q4_K_M.gguf')).toBe('Gemma 4 E2B');
    expect(modelLabel('gemma-4-e2b-it')).toBe('Gemma 4 E2B');
    expect(modelLabel('deepseek-v4-flash')).toBe('Deepseek V4 Flash');
    expect(modelLabel('llama-3-8b-instruct')).toBe('Llama 3 8B');
    expect(modelLabel('models/qwen3-30b-a3b')).toBe('Qwen3 30B A3B');
  });

  it('never returns nothing, whatever the id looks like', () => {
    expect(modelLabel('')).toBe('');
    expect(modelLabel('Q4_K_M')).toBe('Q4_K_M');
    expect(modelLabel('mystery')).toBe('Mystery');
  });
});

// 014 promise 13: which failures mean "this connection is finished", not "try that again".
describe('needsRedial', () => {
  it('is true for a host that never answered and for a broken transport', () => {
    expect(needsRedial('host_asleep')).toBe(true);
    expect(needsRedial('')).toBe(true);
  });

  it('is false for the failures a working connection reports', () => {
    for (const code of ['rate_limited', 'upstream_down', 'key_paused', 'context_too_long']) {
      expect(needsRedial(code), code).toBe(false);
    }
  });
});

// 014 promise 14: finish_reason "length" is the engine saying it ran out of allowance.
describe('a capped reply on the wire', () => {
  it('turns finish_reason: length into a capped event', async () => {
    const body = [
      'data: {"choices":[{"delta":{"content":"half"}}]}\n\n',
      'data: {"choices":[{"delta":{},"finish_reason":"length"}]}\n\n',
      'data: [DONE]\n\n',
    ];
    const kinds = [];
    const t = transportOf(new Response(stream(body), { status: 200 }));
    for await (const ev of chatEvents(t, 's', { model: 'm', messages: [] })) kinds.push(ev.kind);
    expect(kinds).toEqual(['content', 'capped', 'done']);
  });

  it('leaves a normal stop alone', async () => {
    const body = [
      'data: {"choices":[{"delta":{"content":"all"},"finish_reason":"stop"}]}\n\n',
      'data: [DONE]\n\n',
    ];
    const kinds = [];
    const t = transportOf(new Response(stream(body), { status: 200 }));
    for await (const ev of chatEvents(t, 's', { model: 'm', messages: [] })) kinds.push(ev.kind);
    expect(kinds).toEqual(['content', 'done']);
  });
});
