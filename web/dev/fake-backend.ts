// A stand-in for the real gateway (ticket 002), shaped exactly by docs/ARCHITECTURE.md §Gateway
// HTTP API. One implementation feeds two adapters: dev/fake-gateway.ts (a Node http server, for
// Direct mode) and dev/fake-bunny-tunnel.ts (an in-page fake of window.BunnyTunnel, for Tunnel
// mode). Nothing here ships in the bundle.

export interface FakeRequest {
  method: string;
  path: string;
  headers: Record<string, string>;
  body: string;
}

export interface FakeResponse {
  status: number;
  headers: Record<string, string>;
  body?: string;
  /** When set, the body is streamed as these SSE frames, in order, as they are produced. */
  sse?: AsyncIterable<string>;
}

export interface FakeOptions {
  hostName?: string;
  models?: string[];
  region?: string;
  /** Milliseconds between streamed tokens. */
  tokenDelayMs?: number;
  /** Milliseconds before the first token, i.e. simulated TTFT. */
  ttftMs?: number;
}

/** Counters so the usage bar in the header actually moves while you use the demo. */
const counters = { rpm_used: 0, tpm_used: 0, today_tokens: 0, in_flight: 0 };

const LIMITS = {
  rpm: 20,
  tpm: 20000,
  max_concurrent: 1,
  max_output_tokens: 2048,
  max_context: 0,
  daily_tokens: 200000,
};

/**
 * Type `/429`, `/503`, `/403` or `/502` as the first word of a message to make the fake host answer
 * with that failure; anything else streams a reply. This is how the error screenshots are produced.
 */
const FAILURES: Record<string, { status: number; type: string; code: string; message: string; retryAfter?: number }> = {
  '/429': {
    status: 429,
    type: 'rate_limit_error',
    code: 'rate_limited',
    message: 'You have used 20 of 20 requests this minute.',
    retryAfter: 42,
  },
  '/503': {
    status: 503,
    type: 'upstream_error',
    code: 'upstream_down',
    message: 'The engine on the host machine is not answering.',
    retryAfter: 10,
  },
  '/403': {
    status: 403,
    type: 'permission_error',
    code: 'key_revoked',
    message: 'This invite was revoked by the host.',
  },
  '/502': {
    status: 502,
    type: 'upstream_error',
    code: 'upstream_error',
    message: 'The engine returned 500 Internal Server Error.',
  },
};

export function handleFake(req: FakeRequest, opts: FakeOptions = {}): FakeResponse {
  const models = opts.models ?? ['gemma-4-e2b-it', 'deepseek-v4-flash'];
  const path = req.path.split('?')[0] ?? '/';

  if (path === '/healthz') return json(200, { ok: true });

  const auth = req.headers['authorization'] ?? '';
  if (!auth.startsWith('Bearer ') || auth.length < 8) {
    return error(401, 'authentication_error', 'invalid_key', 'No key was presented with this request.');
  }

  if (path === '/me') {
    return json(200, {
      key: { id: 'k_7f3a2b', name: 'alice', status: 'active' },
      limits: LIMITS,
      usage: { ...counters },
      host: {
        name: opts.hostName ?? "Max's workstation",
        upstream: { kind: 'llama.cpp', healthy: true, model_context: 8192 },
        models,
        relay: { region: opts.region ?? 'sfo' },
      },
    });
  }

  if (path === '/v1/models') {
    return json(200, {
      object: 'list',
      data: models.map((id) => ({ id, object: 'model', owned_by: 'host' })),
    });
  }

  if (path === '/v1/chat/completions' && req.method === 'POST') {
    const parsed = JSON.parse(req.body || '{}') as {
      model?: string;
      messages?: { role: string; content: string }[];
    };
    const last = [...(parsed.messages ?? [])].reverse().find((m) => m.role === 'user');
    const text = (last?.content ?? '').trim();
    const failure = FAILURES[text.split(/\s/)[0] ?? ''];
    if (failure) {
      counters.rpm_used = Math.min(LIMITS.rpm, counters.rpm_used + 1);
      const res = error(failure.status, failure.type, failure.code, failure.message);
      if (failure.retryAfter) res.headers['retry-after'] = String(failure.retryAfter);
      return res;
    }
    counters.rpm_used = Math.min(LIMITS.rpm, counters.rpm_used + 1);
    return {
      status: 200,
      headers: {
        'content-type': 'text/event-stream',
        'cache-control': 'no-cache',
        connection: 'close',
      },
      sse: chatStream(parsed.model ?? models[0] ?? 'model', text, opts),
    };
  }

  return error(404, 'invalid_request_error', '', `No route ${req.method} ${path}.`);
}

function json(status: number, value: unknown): FakeResponse {
  return { status, headers: { 'content-type': 'application/json' }, body: JSON.stringify(value) };
}

function error(status: number, type: string, code: string, message: string): FakeResponse {
  return {
    status,
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ error: { message, type, ...(code ? { code } : {}) } }),
  };
}

const REASONING =
  'The friend is talking to a GPU on somebody else’s desk. ' +
  'I should answer plainly and show a little markdown so the renderer is exercised. ';

function reply(prompt: string): string {
  return (
    `You are talking to a model running on someone else’s machine. ` +
    `Your message travelled over an encrypted tunnel; the host sees that a request happened, not what it said.\n\n` +
    `You asked: **${prompt.slice(0, 80) || 'nothing yet'}**\n\n` +
    `Here is what the round trip looks like:\n\n` +
    '```python\n' +
    'import httpx\n\n' +
    'r = httpx.post(\n' +
    '    "https://bunny.local/v1/chat/completions",\n' +
    '    headers={"Authorization": f"Bearer {invite_secret}"},\n' +
    '    json={"model": "gemma-4-e2b-it", "messages": messages, "stream": True},\n' +
    ')\n' +
    'print(r.status_code)\n' +
    '```\n\n' +
    '| hop | what it is | encrypted |\n' +
    '| --- | --- | --- |\n' +
    '| browser → relay | WebSocket to a DERP node | yes |\n' +
    '| relay → host | WireGuard over the same relay | yes |\n' +
    '| host → engine | loopback on the host machine | n/a |\n\n' +
    'Nothing in the middle can read the text — the relay only sees ciphertext.'
  );
}

async function* chatStream(
  model: string,
  prompt: string,
  opts: FakeOptions,
): AsyncGenerator<string, void, void> {
  const delay = opts.tokenDelayMs ?? 18;
  const id = `chatcmpl-${Math.random().toString(36).slice(2, 10)}`;
  const frame = (delta: Record<string, string>) =>
    `data: ${JSON.stringify({
      id,
      object: 'chat.completion.chunk',
      model,
      choices: [{ index: 0, delta, finish_reason: null }],
    })}\n\n`;

  await sleep(opts.ttftMs ?? 220);
  for (const token of tokenize(REASONING)) {
    yield frame({ reasoning_content: token });
    await sleep(delay);
  }
  const body = reply(prompt);
  for (const token of tokenize(body)) {
    yield frame({ content: token });
    await sleep(delay);
  }
  const promptTokens = Math.ceil(prompt.length / 4) + 12;
  const completionTokens = Math.ceil((REASONING.length + body.length) / 4);
  counters.tpm_used += promptTokens + completionTokens;
  counters.today_tokens += promptTokens + completionTokens;
  yield `data: ${JSON.stringify({
    id,
    object: 'chat.completion.chunk',
    model,
    choices: [],
    usage: {
      prompt_tokens: promptTokens,
      completion_tokens: completionTokens,
      total_tokens: promptTokens + completionTokens,
    },
  })}\n\n`;
  yield 'data: [DONE]\n\n';
}

/** Word-ish tokens, so streaming looks like streaming. */
function tokenize(text: string): string[] {
  return text.match(/\s*\S+|\s+/g) ?? [text];
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
