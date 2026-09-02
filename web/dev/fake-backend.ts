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
  /** `/me` reports the host running with --log-prompts (ticket 006 adds the real field). */
  logPrompts?: boolean;
  /** `/me` reports the engine as not answering. */
  upstreamDown?: boolean;
  /** How a streamed reply ends. The last three are the endings 007 promise 1 is about. */
  streamMode?: StreamMode;
  /**
   * The host went away: a chat request is accepted by the tunnel and simply never answered, and
   * ping() fails with it. This is the blocker 014 promise 1 is about, so it is a mode, not a code.
   * Handled by the conn (fake-bunny-tunnel), which never writes a response.
   */
  hostAsleep?: boolean;
  /** The host paused this invite mid-session: chat is 403 key_paused, /me still answers. */
  keyPaused?: boolean;
  /** /me answers this many times and then fails: the "unknown, not zero" surface (promise 3). */
  meFailsAfter?: number;
}

/**
 * `normal` ends with usage + [DONE] · `error-mid-stream` flushes a 200, streams part of an answer
 * and then sends an OpenAI-shaped error member · `eof-no-done` streams part of an answer and the
 * connection simply ends · `reasoning-only` thinks and never answers.
 */
export type StreamMode = 'normal' | 'error-mid-stream' | 'eof-no-done' | 'reasoning-only' | 'capped';

/** Counters so the usage bar in the header actually moves while you use the demo. */
const counters = { rpm_used: 0, tpm_used: 0, today_tokens: 0, in_flight: 0 };

/** How many times /me has been asked, for the mode where it stops answering. */
let meCalls = 0;
/** A pause is something the host does *while* you are chatting: /me only reports it once it bites. */
let pauseBit = false;

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
    if (opts.meFailsAfter !== undefined && meCalls++ >= opts.meFailsAfter) {
      return error(503, 'upstream_error', 'upstream_down', 'The host is not answering right now.');
    }
    return json(200, {
      key: { id: 'k_7f3a2b', name: 'alice', status: opts.keyPaused && pauseBit ? 'paused' : 'active' },
      limits: LIMITS,
      usage: { ...counters },
      host: {
        name: opts.hostName ?? "Max's workstation",
        upstream: { kind: 'llama.cpp', healthy: !opts.upstreamDown, model_context: 8192 },
        models,
        relay: { region: opts.region ?? 'sfo' },
        ...(opts.logPrompts ? { log_prompts: true } : {}),
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
    // 014 promise 2: paused is something the host did, and something they can undo.
    if (opts.keyPaused) {
      pauseBit = true;
      return error(403, 'permission_error', 'key_paused', 'This invite is paused by the host.');
    }
    const parsed = JSON.parse(req.body || '{}') as {
      model?: string;
      messages?: { role: string; content: string }[];
    };
    const last = [...(parsed.messages ?? [])].reverse().find((m) => m.role === 'user');
    const text = (last?.content ?? '').trim();
    const word = text.split(/\s/)[0] ?? '';
    const mode: StreamMode =
      word === '/cap' ? 'capped'
      : word === '/cut' ? 'eof-no-done'
      : word === '/mid' ? 'error-mid-stream'
      : word === '/think' ? 'reasoning-only'
      : (opts.streamMode ?? 'normal');
    const failure = FAILURES[word];
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
      sse: chatStream(parsed.model ?? models[0] ?? 'model', text, opts, mode),
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

// Deliberately marked up: reasoning is model output like any other and must render as markdown,
// not as raw asterisks on screen (014 promise 17).
const REASONING =
  '**A real question**, from a friend on a GPU on somebody else’s desk. ' +
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
  mode: StreamMode = 'normal',
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
  // A reasoning model with a small cap: it thinks for its whole budget and never answers.
  if (mode === 'reasoning-only') {
    yield usageFrame(id, model, Math.ceil(prompt.length / 4) + 12, Math.ceil(REASONING.length / 4));
    yield 'data: [DONE]\n\n';
    return;
  }
  const body = reply(prompt);
  const tokens = tokenize(body);
  const cut = mode === 'normal' ? tokens.length : Math.max(4, Math.floor(tokens.length / 6));
  for (const token of tokens.slice(0, cut)) {
    yield frame({ content: token });
    await sleep(delay);
  }
  // The engine ran out of the invite's reply allowance: a complete transfer of an unfinished
  // answer, which is the one ending that used to render as a finished one (014 promise 14).
  if (mode === 'capped') {
    yield `data: ${JSON.stringify({
      id,
      object: 'chat.completion.chunk',
      model,
      choices: [{ index: 0, delta: {}, finish_reason: 'length' }],
    })}\n\n`;
    yield usageFrame(id, model, Math.ceil(prompt.length / 4) + 12, 2048);
    yield 'data: [DONE]\n\n';
    return;
  }
  // The two ways a stream lies about being finished, exactly as a real one would.
  if (mode === 'error-mid-stream') {
    yield `data: ${JSON.stringify({
      error: {
        message: 'The engine dropped the request after 6 s.',
        type: 'upstream_error',
        code: 'upstream_error',
      },
    })}\n\n`;
    return;
  }
  if (mode === 'eof-no-done') return; // no usage chunk, no [DONE]: the connection just ends
  const promptTokens = Math.ceil(prompt.length / 4) + 12;
  const completionTokens = Math.ceil((REASONING.length + body.length) / 4);
  counters.tpm_used += promptTokens + completionTokens;
  counters.today_tokens += promptTokens + completionTokens;
  yield usageFrame(id, model, promptTokens, completionTokens);
  yield 'data: [DONE]\n\n';
}

function usageFrame(id: string, model: string, prompt_tokens: number, completion_tokens: number): string {
  return `data: ${JSON.stringify({
    id,
    object: 'chat.completion.chunk',
    model,
    choices: [],
    usage: { prompt_tokens, completion_tokens, total_tokens: prompt_tokens + completion_tokens },
  })}\n\n`;
}

/** Word-ish tokens, so streaming looks like streaming. */
function tokenize(text: string): string[] {
  return text.match(/\s*\S+|\s+/g) ?? [text];
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
