import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { NEW_REPLY, reduceReply } from './stream';
import { tr } from './i18n/text';
import { KEYS } from './storage';
import {
  chatEvents,
  DEFAULT_DEADLINES,
  FIRST_BYTE_TIMEOUT_MS,
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

/** Every block's data payload, with a comment-only block rendered as `:text` so it is visible. */
async function blocks(parts: string[]): Promise<string[]> {
  const out: string[] = [];
  for await (const b of sseData(stream(parts))) out.push('data' in b ? b.data : `:${b.comment}`);
  return out;
}

describe('sseData', () => {
  it('splits events on blank lines and strips one leading space', async () => {
    expect(await blocks(['data: one\n\ndata:two\n\n'])).toEqual(['one', 'two']);
  });

  it('reassembles events split across reads and accepts CRLF', async () => {
    expect(await blocks(['data: {"a"', ':1}\r\n', '\r\ndata: [DONE]\n\n'])).toEqual(['{"a":1}', '[DONE]']);
  });

  it('joins multi-line data and ignores comment and event lines', async () => {
    expect(await blocks([': keep-alive\nevent: x\ndata: a\ndata: b\n\n'])).toEqual(['a\nb']);
  });

  // The bug this exists for: a CRLF split across two reads used to leave a stray \r on the payload,
  // which turned the [DONE] sentinel into [DONE]\r and made every reply look truncated.
  it('keeps [DONE] intact when the CRLF of its blank line is split across reads', async () => {
    expect(await blocks(['data: a\r\n\r\ndata: [DONE]\r', '\n\r\n'])).toEqual(['a', '[DONE]']);
  });

  it('yields a trailing event that never got its blank line', async () => {
    expect(await blocks(['data: last'])).toEqual(['last']);
  });

  // 018: the gateway's keepalive is a comment in a block of its own, so it arrives as one.
  it('surfaces a comment-only block, as it arrives, and never as data', async () => {
    expect(await blocks([': queued\n\n', ': queued\r\n\r\n', 'data: a\n\n'])).toEqual([':queued', ':queued', 'a']);
    expect(await blocks([': queued\n\n'])).toEqual([':queued']);
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
  it('reads reasoning-named deltas', async () => {
    const body = stream([chunk({ reasoning: 'Thinking' }), chunk({ content: 'Answer' }), 'data: [DONE]\n\n']);
    const events = await collect(chatEvents(transportOf(new Response(body)), 'k', { model: 'm', messages: [] }));
    expect(events).toEqual([{ kind: 'reasoning', text: 'Thinking' }, { kind: 'content', text: 'Answer' }, { kind: 'done' }]);
  });

  it('prefers reasoning_content when both names exist, including an empty value', async () => {
    const body = stream([chunk({ reasoning_content: 'Preferred', reasoning: 'Ignored' }), chunk({ reasoning_content: '', reasoning: 'Also ignored' }), 'data: [DONE]\n\n']);
    const events = await collect(chatEvents(transportOf(new Response(body)), 'k', { model: 'm', messages: [] }));
    expect(events).toEqual([{ kind: 'reasoning', text: 'Preferred' }, { kind: 'done' }]);
  });

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

  // 018: with the head already out, Retry-After has no header to ride on; it is in the event.
  it('reads retry_after from an error event inside a 200 stream', async () => {
    const body = stream([
      ': queued\n\n',
      `data: ${JSON.stringify({ error: { message: 'waited 30s for a free slot', type: 'upstream_error', code: 'queue_timeout', retry_after: 7 } })}\n\n`,
      'data: [DONE]\n\n',
    ]);
    const events = await collect(chatEvents(transportOf(new Response(body)), 'k', { model: 'm', messages: [] }, undefined, undefined, 'desk'));
    expect(events.map((e) => e.kind)).toEqual(['queued', 'error']);
    const last = events[1];
    if (last?.kind !== 'error') throw new Error('unreachable');
    expect(last.code).toBe('queue_timeout');
    expect(last.error.retryAfterS).toBe(7);
    expect(last.error.title).toBe('desk is busy');
    expect(last.error.hostSaid).toBe('waited 30s for a free slot');
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
      host: { name: 'desk', upstream: { kind: 'llama.cpp', healthy: true, model_context: 8192 }, models: ['m'], vision: { m: null }, relay: { region: 'sfo' } },
    };
    const seen: { path?: string; init?: RequestInit } = {};
    const got = await getMe(transportOf(new Response(JSON.stringify(me)), seen), 'sekrit');
    expect(got).toEqual(me);
    expect(new Headers(seen.init?.headers).get('authorization')).toBe('Bearer sekrit');
  });
});

describe('describeError', () => {
  it.each([
    ['en', 'The host could not store this image; it did not count.', 'Try again. If it keeps failing, the host’s disk needs attention.'],
    ['zh', '主机无法保存这张图片，没有扣除额度。', '请重试。如果仍然失败，主机的磁盘需要检查。'],
  ])('gives a storage failure its no-charge next step in %s', (language, title, detail) => {
    vi.stubGlobal('localStorage', { getItem: () => JSON.stringify(language) });
    try {
      const error = describeError(new GatewayError(500, 'storage_failed', 'server_error', 'host storage fault'));
      expect(error.title).toBe(title);
      expect(error.detail).toBe(detail);
      expect(error.hostSaid).toBe('host storage fault');
      expect(error.retryAfterS).toBeUndefined(); // A retry is the friend's choice, never an automatic replay.
      expect(error.fatal).toBeUndefined();
    } finally { vi.unstubAllGlobals(); }
  });

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
/** A busy host (018): the head at once, `: queued` every `everyMs` for `forMs`, then either the
 *  engine's reply or the queue's timeout — or nothing more, so the caller decides when to stop. */
function busy(everyMs: number, forMs: number, then: 'reply' | 'timeout' | 'silence'): Transport {
  const enc = new TextEncoder();
  return {
    kind: 'tunnel',
    fetch: (_path, init) => {
      const signal = init?.signal;
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          signal?.addEventListener('abort', () => c.error(new DOMException('aborted', 'AbortError')));
          const say = (s: string): void => {
            if (!signal?.aborted) c.enqueue(enc.encode(s));
          };
          say(': queued\n\n');
          const tick = setInterval(() => say(': queued\n\n'), everyMs);
          setTimeout(() => {
            clearInterval(tick);
            if (then === 'reply') say(`${chunk({ content: 'here' })}data: [DONE]\n\n`);
            if (then === 'timeout') {
              say(`data: ${JSON.stringify({ error: { message: 'waited 30s', type: 'upstream_error', code: 'queue_timeout', retry_after: 5 } })}\n\ndata: [DONE]\n\n`);
            }
            if (then !== 'silence' && !signal?.aborted) c.close();
          }, forMs);
        },
      });
      return Promise.resolve(new Response(body, { status: 200 }));
    },
    ping: () => Promise.resolve(null),
    close: () => {},
  };
}

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
  const fast = { noticeMs: 20, firstByteMs: 60, idleMs: 30 };

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
    setTimeout(() => ac.abort(), 200); // long past firstByteMs
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

  // 018, the three silences. The second: the head is out and the host says it is in line, so
  // however long that takes it is never called asleep — and while the keepalives keep coming,
  // "still waiting" never speaks over "waiting for a free slot".
  it('a host that says it is queued is never called asleep, however long the line', async () => {
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(busy(15, 150, 'reply'), 's', req, undefined, fast, 'desk')) seen.push(ev);
    const kinds = seen.map((e) => e.kind);
    expect(kinds.filter((k) => k === 'queued').length).toBeGreaterThanOrEqual(8);
    expect(kinds).not.toContain('waiting');
    expect(kinds).not.toContain('error');
    expect(kinds.slice(-2)).toEqual(['content', 'done']);
  });

  it('says "still waiting" only once the keepalives have stopped for two notice intervals', async () => {
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 130);
    const seen: { kind: string; at: number }[] = [];
    const t0 = Date.now();
    for await (const ev of chatEvents(busy(15, 40, 'silence'), 's', req, ac.signal, fast, 'desk')) {
      seen.push({ kind: ev.kind, at: Date.now() - t0 });
    }
    const waiting = seen.find((e) => e.kind === 'waiting');
    const lastQueued = [...seen].reverse().find((e) => e.kind === 'queued');
    expect(waiting).toBeDefined();
    expect(lastQueued).toBeDefined();
    expect(waiting!.at - lastQueued!.at).toBeGreaterThanOrEqual(2 * fast.noticeMs - 5);
    expect(seen.map((e) => e.kind)).not.toContain('error');
    expect(seen[seen.length - 1]?.kind).toBe('aborted');
  });

  // The third: the line ran out. It is the host's own error event, with its retry_after, and it
  // is not a sleeping host — nothing here needs a reconnect.
  it('a queue that times out is a queue_timeout with a countdown, not a sleeping host', async () => {
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(busy(15, 100, 'timeout'), 's', req, undefined, fast, 'desk')) seen.push(ev);
    const last = seen[seen.length - 1];
    expect(last?.kind).toBe('error');
    if (last?.kind !== 'error') throw new Error('unreachable');
    expect(last.code).toBe('queue_timeout');
    expect(last.error.retryAfterS).toBe(5);
    expect(last.error.title).toBe('desk is busy');
    expect(needsRedial(last.code)).toBe(false);
    expect(seen.some((e) => e.kind === 'error' && e.code === 'host_asleep')).toBe(false);
  });
});

// 020 promise 2, the fourth silence: the head is out, tokens were flowing, and then nothing.
describe('a host that stops answering mid-reply', () => {
  /** Answers the head, streams two tokens, and then never another byte — a host killed mid-reply. */
  function stalls(): Transport {
    const enc = new TextEncoder();
    return {
      kind: 'tunnel',
      fetch: (_path, init) => {
        const signal = init?.signal;
        const body = new ReadableStream<Uint8Array>({
          start(c) {
            signal?.addEventListener('abort', () => c.error(new DOMException('aborted', 'AbortError')));
            c.enqueue(enc.encode(chunk({ content: 'half' })));
            c.enqueue(enc.encode(chunk({ content: ' way' })));
          },
        });
        return Promise.resolve(new Response(body, { status: 200 }));
      },
      ping: () => Promise.resolve(null),
      close: () => {},
    };
  }
  const req = { model: 'm', messages: [{ role: 'user' as const, content: 'hi' }] };
  const fast = { noticeMs: 20, firstByteMs: 60, idleMs: 30 };

  it('ends the reply as host_stalled, in the host’s name, when /me says the host is gone', async () => {
    let probes = 0;
    const gone = () => {
      probes++;
      return Promise.reject(new Error('dial port 80: context deadline exceeded'));
    };
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(stalls(), 's', req, undefined, fast, 'desk', gone)) seen.push(ev);
    expect(seen.map((e) => e.kind)).toEqual(['content', 'content', 'error']);
    const last = seen[seen.length - 1];
    if (last?.kind !== 'error') throw new Error('unreachable');
    expect(last.code).toBe('host_stalled');
    expect(last.error.title).toBe('desk stopped answering mid-reply');
    expect(last.error.detail).toContain('What arrived is above');
    expect(probes).toBe(1);
    expect(needsRedial(last.code)).toBe(false); // the /me that follows decides whether it is Reconnect
  });

  it('ends it the same way when /me answers that the engine is unhealthy', async () => {
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(stalls(), 's', req, undefined, fast, 'desk', () => Promise.resolve(false))) seen.push(ev);
    expect(seen[seen.length - 1]).toMatchObject({ kind: 'error', code: 'host_stalled' });
    expect(seen.some((e) => e.kind === 'aborted')).toBe(false);
  });

  // A long prompt takes a slow GPU longer than any idle clock to read: silence with a healthy host
  // behind it is patience, not an ending.
  it('keeps waiting, and keeps asking, while /me says the host is healthy', async () => {
    let probes = 0;
    const healthy = () => {
      probes++;
      return Promise.resolve(true);
    };
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 150);
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(stalls(), 's', req, ac.signal, fast, 'desk', healthy)) seen.push(ev);
    expect(seen.map((e) => e.kind)).toEqual(['content', 'content', 'aborted']);
    expect(probes).toBeGreaterThanOrEqual(2);
  });

  it('never fires while the host is sending keepalives, however long the line', async () => {
    let probes = 0;
    const gone = () => {
      probes++;
      return Promise.resolve(false);
    };
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(busy(10, 120, 'reply'), 's', req, undefined, fast, 'desk', gone)) seen.push(ev);
    expect(seen.slice(-2).map((e) => e.kind)).toEqual(['content', 'done']);
    expect(probes).toBe(0);
  });

  it('is silent without a probe: a slow model on its own is never an ending', async () => {
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 100);
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(stalls(), 's', req, ac.signal, fast, 'desk')) seen.push(ev);
    expect(seen.map((e) => e.kind)).toEqual(['content', 'content', 'aborted']);
  });

  it('a reader pressing Stop in the silence is still a stop', async () => {
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 10);
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(stalls(), 's', req, ac.signal, fast, 'desk', () => Promise.resolve(false))) seen.push(ev);
    expect(seen[seen.length - 1]).toEqual({ kind: 'aborted' });
  });

  // 020 promise 6: an abort the app asked for, with a reason, is not the reader's Stop.
  it('an abort with a reason carries it, so the reply never says the reader stopped it', async () => {
    const ac = new AbortController();
    setTimeout(() => ac.abort('Another tab took over this chat'), 10);
    const seen: StreamEvent[] = [];
    for await (const ev of chatEvents(stalls(), 's', req, ac.signal, fast, 'desk')) seen.push(ev);
    expect(seen[seen.length - 1]).toEqual({ kind: 'aborted', why: 'Another tab took over this chat' });
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
    for (const code of ['rate_limited', 'upstream_down', 'key_paused', 'context_too_long', 'queue_timeout']) {
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

it('uses the shared invite cap in HTTP/SSE notices, preserving one-seat copy and retry', async () => {
  try {
    for (const lang of ['en', 'zh']) {
      vi.stubGlobal('navigator', { language: lang });
      for (const limit of [undefined, 1, 30]) for (const inStream of [false, true]) {
        const error = { code: 'concurrency_limited', type: 'rate_limit_error', message: 'diagnostic', retry_after: 7, ...(limit === undefined ? {} : { limit, in_flight: 31 }) };
        const body = JSON.stringify({ error });
        const res = inStream ? new Response(`data: ${body}\n\ndata: [DONE]\n\n`) : new Response(body, { status: 429, headers: { 'retry-after': '7' } });
        const events = await collect(chatEvents(transportOf(res), 'k', { model: 'm', messages: [] }));
        const event = events[0];
        expect(event?.kind).toBe('error');
        if (!event || event.kind !== 'error') throw new Error('missing error');
        expect(event.error.retryAfterS).toBe(7);
        expect(event.error.hostSaid).toBe('diagnostic');
        const note = reduceReply(NEW_REPLY, event).note;
        if (limit === 30) {
          expect(note).toBe(lang === 'en' ? 'This invite is busy — all 30 seats are in use. Try again in a moment.' : '这个邀请码正忙：所有 30 个名额都在使用中。稍后再试。');
        } else {
          expect(event.error.title).toBe(tr('app_one_reply_at_a_time'));
          expect(event.error.detail).toBe(tr('app_this_invite_may_have_one_request_in_flight_wait'));
          expect(note).toBe(tr('app_not_sent_one_reply_at_a_time'));
        }
      }
    }
  } finally { vi.unstubAllGlobals(); }
});

describe('cold-load first-byte patience', () => {
  const req = { model: 'cold-model', messages: [{ role: 'user' as const, content: 'hi' }] };
  it('matches the gateway first-byte budget without changing notice or idle deadlines', () => {
    const go = readFileSync(new URL('../../internal/upstream/client.go', import.meta.url), 'utf8');
    const seconds = /FirstByteTimeout\s*=\s*(\d+)\s*\*\s*time.Second/.exec(go)?.[1];
    expect(seconds).toBeDefined();
    expect(FIRST_BYTE_TIMEOUT_MS).toBe(Number(seconds) * 1000);
    expect(DEFAULT_DEADLINES).toEqual({ noticeMs: 5000, firstByteMs: FIRST_BYTE_TIMEOUT_MS, idleMs: 15000 });
    expect(tr('app_waiting_for_model_load', { host: 'Max', model: 'cold-model' }, 'en')).toBe('Waiting for Max to load cold-model…');
    expect(tr('app_waiting_for_model_load', { host: 'Max', model: 'cold-model' }, 'zh')).toBe('正在等待 Max 加载 cold-model…');
  });

  it.each(['headers', 'stop'] as const)('notifies at five seconds and clears on %s, surviving the old deadline', async (finish) => {
    vi.useFakeTimers();
    const ac = new AbortController();
    try {
      let respond!: (response: Response) => void;
      let requestSignal: AbortSignal | null | undefined;
      const transport: Transport = {
        kind: 'tunnel',
        fetch: (_path, init) => new Promise<Response>((resolve, reject) => {
          respond = resolve; requestSignal = init?.signal;
          requestSignal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }),
        ping: () => Promise.resolve(null), close: () => {},
      };
      const waiting = vi.fn(); const seen: StreamEvent[] = [];
      const done = (async () => { for await (const ev of chatEvents(transport, 's', req, ac.signal, undefined, 'Max', undefined, waiting)) seen.push(ev); })();
      await vi.advanceTimersByTimeAsync(4999);
      expect(waiting).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);
      expect(waiting).toHaveBeenLastCalledWith(true);
      await vi.advanceTimersByTimeAsync(15001);
      expect(requestSignal?.aborted).toBe(false);
      if (finish === 'headers') {
        let body!: ReadableStreamDefaultController<Uint8Array>;
        respond(new Response(new ReadableStream<Uint8Array>({ start(c) { body = c; } }), { status: 200 }));
        await vi.advanceTimersByTimeAsync(0);
        expect(waiting).toHaveBeenLastCalledWith(false);
        expect(seen.some((e) => e.kind === 'content')).toBe(false);
        body.enqueue(new TextEncoder().encode('data: [DONE]\n\n')); body.close();
      } else ac.abort();
      await done;
      expect(waiting).toHaveBeenLastCalledWith(false);
      expect(seen.at(-1)?.kind).toBe(finish === 'headers' ? 'done' : 'aborted');
    } finally { ac.abort(); vi.useRealTimers(); }
  });

  it('still ends an unanswered request at the gateway deadline', async () => {
    vi.useFakeTimers();
    try {
      let signal: AbortSignal | null | undefined;
      const transport: Transport = {
        kind: 'tunnel', fetch: (_path, init) => new Promise<Response>((_resolve, reject) => {
          signal = init?.signal;
          signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }), ping: () => Promise.resolve(null), close: () => {},
      };
      const seen: StreamEvent[] = [];
      const done = (async () => { for await (const ev of chatEvents(transport, 's', req)) seen.push(ev); })();
      await vi.advanceTimersByTimeAsync(FIRST_BYTE_TIMEOUT_MS - 1);
      expect(signal?.aborted).toBe(false);
      await vi.advanceTimersByTimeAsync(1); await done;
      expect(signal?.aborted).toBe(true);
      expect(seen.at(-1)).toMatchObject({ kind: 'error', code: 'host_asleep' });
    } finally { vi.useRealTimers(); }
  });
});

 it('gives agent_unavailable the approved bilingual copy and normal retry affordance', () => {
  const previous=globalThis.localStorage;
  try {
   for(const [lang,copy] of [['en',"The host's agent runtime is not available right now. Try again in a moment."],['zh','主机的智能体运行环境暂时不可用，请稍后再试。']]) {
    vi.stubGlobal('localStorage',{getItem:(key:string)=>key===KEYS.language?JSON.stringify(lang):null});
    const error=describeError(new GatewayError(503,'agent_unavailable','upstream_error','runtime diagnostic'));
    expect(error.title).toBe(copy);expect(error.retryAfterS).toBe(5);expect(error.fatal).toBeUndefined();
    expect(describeError(new GatewayError(503,'agent_unavailable','upstream_error','runtime diagnostic',12)).retryAfterS).toBe(12);
   }
  } finally {vi.stubGlobal('localStorage',previous);}
 });
