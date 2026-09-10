import { handleImageJobs, fakeImageJobs, imageControl } from './fake-image-jobs';
import { handleFakeRuns } from './fake-runs';
import type { Me } from '../../packages/client/src/contract';

// A stand-in for the real gateway (ticket 002), shaped exactly by docs/ARCHITECTURE.md §Gateway
// HTTP API. One implementation feeds two adapters: dev/fake-gateway.ts (a Node http server, for
// Direct mode) and dev/fake-infercat-tunnel.ts (an in-page fake of window.InfercatTunnel, for Tunnel
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
  bytes?: Uint8Array;
  /** When set, the body is streamed as these SSE frames, in order, as they are produced. */
  sse?: AsyncIterable<string>;
}

export interface FakeOptions {
  agent?: boolean;
  imageJobs?: boolean;
  runState?: string;
  hostName?: string;
  models?: string[];
  vision?: boolean | null;
  transcriptions?: boolean;
  speech?: boolean;
  audioFailure?: number;
  audioDelayMs?: number;
  rejectImages?: boolean;
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
   * Handled by the conn (fake-infercat-tunnel), which never writes a response.
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
export type StreamMode = 'normal' | 'error-mid-stream' | 'eof-no-done' | 'reasoning-only' | 'capped' | 'context-wall';

/** Counters so the usage bar in the header actually moves while you use the demo. */
const counters = { rpm_used: 0, tpm_used: 0, today_tokens: 0, in_flight: 0 };

/** How many times /me has been asked, for the mode where it stops answering. */
let meCalls = 0;
/** A pause is something the host does *while* you are chatting: /me only reports it once it bites. */
let pauseBit = false;
/** A revoke is for good: once /403 has been sent, /me refuses too (020 promise 5). */
let revokedBit = false;

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

export function fakeMe(opts: FakeOptions = {}) {
  const models = opts.models ?? ['gemma-4-e2b-it', 'deepseek-v4-flash'];
  return {
    ...(opts.agent ? { agent: true } : {}),
    key: { id: 'k_7f3a2b', name: 'alice', status: opts.keyPaused && pauseBit ? 'paused' : 'active' },
    limits: { ...LIMITS, ...(opts.imageJobs ? { daily_images: imageControl.daily, max_queued_images: 8 } : {}) },
    usage: { ...counters, ...(opts.imageJobs ? { today_images: fakeImageJobs().filter((j) => j.state === 'done').length } : {}) },
    host: {
      name: opts.hostName ?? "Max's workstation",
      upstream: { kind: 'llama.cpp', healthy: !opts.upstreamDown, model_context: 8192 },
      models,
      ...(opts.imageJobs ? { images: { model: 'Qwen-Image', retention_days: 7, queue_cap: imageControl.cap, queued: fakeImageJobs().filter((j) => j.state === 'queued').length } } : {}),
      audio: { transcriptions: opts.transcriptions ? 'whisper-large-v3' : null, speech: opts.speech ? 'kokoro' : null },
      vision: Object.fromEntries(models.map((id) => [id, opts.vision ?? null])),
      relay: { region: opts.region ?? 'sfo' },
      ...(opts.logPrompts ? { log_prompts: true } : {}),
    },
  } satisfies Me;
}

export function handleFake(req: FakeRequest, opts: FakeOptions = {}): FakeResponse {
  const models = opts.models ?? ['gemma-4-e2b-it', 'deepseek-v4-flash'];
  const path = req.path.split('?')[0] ?? '/';

  if (path === '/healthz') return json(200, { ok: true });

  const auth = req.headers['authorization'] ?? '';
  if (!auth.startsWith('Bearer ') || auth.length < 8) {
    return error(401, 'authentication_error', 'invalid_key', 'No key was presented with this request.');
  }

  if (opts.imageJobs) { const image = handleImageJobs(req); if (image) return image; }
  if (opts.agent || opts.imageJobs) { const run = handleFakeRuns(req, opts.runState); if (run) return run; }

  if (path === '/me') {
    if (revokedBit) return error(403, 'permission_error', 'key_revoked', 'This invite was revoked by the host.');
    if (opts.meFailsAfter !== undefined && meCalls++ >= opts.meFailsAfter) {
      return error(503, 'upstream_error', 'upstream_down', 'The host is not answering right now.');
    }
    return json(200, fakeMe(opts));
  }

  if (path === '/v1/models') {
    return json(200, {
      object: 'list',
      data: models.map((id) => ({ id, object: 'model', owned_by: 'host' })),
    });
  }

  if (path.startsWith('/v1/audio/')) {
    const hears = path === '/v1/audio/transcriptions';
    if (!(hears ? opts.transcriptions : opts.speech)) return error(404, 'invalid_request_error', 'not_found', 'Audio route is not configured.');
    if (opts.audioFailure) {
      const refused = error(opts.audioFailure, 'upstream_error', opts.audioFailure === 429 ? (hears ? 'audio_budget_exhausted' : 'speech_budget_exhausted') : 'upstream_error', 'The fake audio engine refused this request.');
      if (opts.audioFailure === 429) refused.headers['retry-after'] = '12';
      return refused;
    }
    if (hears) return json(200, { text: req.body.includes('\r\nzh\r\n') ? '请用一句话回答这个问题。' : 'Please answer this question in one sentence.' });
    return { status: 200, headers: { 'content-type': 'audio/mpeg' }, bytes: Uint8Array.from(atob(VOICE_TONE), (c) => c.charCodeAt(0)) };
  }

  if (path === '/v1/chat/completions' && req.method === 'POST') {
    // 014 promise 2: paused is something the host did, and something they can undo.
    if (opts.keyPaused) {
      pauseBit = true;
      return error(403, 'permission_error', 'key_paused', 'This invite is paused by the host.');
    }
    const parsed = JSON.parse(req.body || '{}') as {
      model?: string;
      messages?: { role: string; content: string | { type: string; text?: string; image_url?: { url: string } }[] }[];
    };
    const last = [...(parsed.messages ?? [])].reverse().find((m) => m.role === 'user');
    const content = last?.content;
    const text = (typeof content === 'string' ? content : content?.filter((p) => p.type === 'text').map((p) => p.text ?? '').join('') ?? '').trim();
    const imageCount = (parsed.messages ?? []).reduce((n, m) => n + (Array.isArray(m.content) ? m.content.filter((p) => p.type === 'image_url').length : 0), 0);
    if (imageCount && (opts.rejectImages || text.startsWith('/images'))) return error(400, 'invalid_request_error', 'images_not_supported', 'This model does not support image input.');
    const word = text.split(/\s/)[0] ?? '';
    const mode: StreamMode =
      word === '/cap' ? 'capped'
      : word === '/wall' ? 'context-wall'
      : word === '/cut' ? 'eof-no-done'
      : word === '/mid' ? 'error-mid-stream'
      : word === '/think' ? 'reasoning-only'
      : (opts.streamMode ?? 'normal');
    const failure = FAILURES[word];
    if (failure) {
      if (failure.code === 'key_revoked') revokedBit = true;
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
      sse: chatStream(parsed.model ?? models[0] ?? 'model', text, opts, mode, imageCount),
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
    '    "https://infercat.local/v1/chat/completions",\n' +
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
  imageCount = 0,
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
  // Two walls, one finish_reason (020 promise 3): the invite's reply cap, or the model's context
  // — the numbers in the usage chunk are the only way to tell them apart.
  if (mode === 'capped' || mode === 'context-wall') {
    yield `data: ${JSON.stringify({
      id,
      object: 'chat.completion.chunk',
      model,
      choices: [{ index: 0, delta: {}, finish_reason: 'length' }],
    })}\n\n`;
    yield mode === 'capped'
      ? usageFrame(id, model, Math.ceil(prompt.length / 4) + 12, 2048)
      : usageFrame(id, model, 7000, 1192); // 8192 = model_context, and 1192 < the 2048 cap
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
  const promptTokens = Math.ceil(prompt.length / 4) + 12 + imageCount * 640;
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

// Two seconds of a generated 440 Hz test tone (16 kHz mono MP3, 8 kbit/s); no recorded speech.
const VOICE_TONE = 'SUQzBAAAAAAAIlRTU0UAAAAOAAADTGF2ZjYzLjEuMTAxAAAAAAAAAAAAAAD/81jAAAAAAAAAAAAASW5mbwAAAA8AAAA6AAAI3AAYHBwgICQoKCwsMDA0ODg8PEFFRUlJTU1RVVVZWV1dYWVlaWltcXF1dXl5fYKChoaKjo6SkpaWmp6eoqKmpqqurrKytrq6vr7Dw8fLy8/P09PX29vf3+Pn5+vr7+/z9/f7+/8AAAAATGF2YzYzLjEuAAAAAAAAAAAAAAAAJANAAAAAAAAACNyD8mezAAAAAAAAAAAAAAD/8xjEAAUgAt5hQwABtrIFJqw8PDw98D6z3eZ+44eH//iFSS//8xjEAgV4asQBkDABLxjXYckG8AzgWxIcCuLRk3eI3jT/0in/8xjEAwWoXogB1AAAFYckBwkDhAgP6KABABiUL4j7KrWEjrT/8xjEAwOoWpAgAHZCyvoARwOPpTaXLG7ShIdSVr4MFiAcoO//8xjECwP4WpAgAHZCv+rZGrfu1KDwhoZECAVIhdQBwTUrIA3/8xjEEgVYWmwAAbglKPUNKhIA5aSgGhWKLGbVKrdELNWGWcz/8xjEEwPIWqDgAjRGN3BmkMRxiMNbpbwSG1haKJSOxUeu7qr/8xjEGgN4WpggAHRCDgoD1VlhQeCmQ01ZrLRldrkGgZ6m26D/8xjEIwLQWqzAALBGh3BAyN08YDrqlRgQIA1+7HAASInRPw3/8xjELgQ4XqWAAjIqqKR7NfjKGpXaQEma9pxwKzaSozQBZyX/8xjENAPQUphgAHRCDQwDeW7kfIN2J1HcV3FaJRAsB9i0AGb/8xjEOwQgWqogALAOJCNrxSkeOqvDyoWHKlcIyF4PFQmYWTH/8xjEQQSgXnwAAPYk/Kqw1aCStgSfIAoBNIGCAfF52DjwAOf/8xjERQPgWqmAAHImPneW1vXaSH2uJFmqhq/XHjhIYVAJdcH/8xjETAQYVp0AAbJGAkS2h+oW1jlTS2HnFTCN1TZfdPmCEoH/8xjEUgOQWpAgAHRAOmWFxchwf2mBqqFWmZI+KiRUCEEx6YL/8xjEWgYAXmgAAHohgZqGOok08kJvqxEMB6OgPgiUarljsJ7/8xjEWQaAXmQABjgp8Rwy6gIIAu1hQWATSvTDRZvKHQAkDer/8xjEVgaQXmAABjgpR6oWxCnAvlQyNasXnuUMCE+eFO6Bg6D/8xjEUgW4XmwAAHohHTIfhteEqzUcANOzImSmYoI2GtDiaU3/8xjEUgQYWqUAAjJGBAS///0y6gIIABVFQIjsbJdSZB53JDX/8xjEWANwWrGAALBGEQwGDdSJhTUWblj+LTlNjLoZkBgNKLv/8xjEYQSoXpogAHQKOwMXQKdTkw1WhpqEjrTK+gBHA4+lNpr/8xjEZQQgWplgAHQmzT2qAgwDtbOIFeJmW3/VsjVu+cWmdrD/8xjEawVYVoQYAHZCY4HgIew2RBIFrTIAoxqhpgIMA707CRH/8xjEbAPYWrGgAXJGyoW5UqlUMTuGWcw3cGaQxHGIw1ulvBL/8xjEcwQoWqUAAHJCANrC0USkdiILbjuqEoDhYJAcDPBaazP/8xjEeQQgVqYgAXAPSqsTtcrGeq7wCO4HBJXL3ctX+RAAM/T/8xjEfwOgWpAgAHZAOxL2WGndCeRk8NqKR7P+1qvmE5gD0x//8xjEhwOwWrGAAbBGxM1MCV/SUVADoS3JEwDRSgXIN2J1HcX/8xjEjwUwWnAAA/gpdxWiEIwGu4RsCTH7yyUN3mcNKg4MA1n/8xjEkQO4Wq2AAbJGiygQWoRWvRxBmA6a2KMC4IY2SBIQ7Iz/8xjEmQN4WpggAHRCBCBabvrMk8nV9H6sJ2AHDW/Q+AKDANf/8xjEogMgWqzgALBGOIgpuVPh6zCRUtd4tia71nxAabLxjIX/8xjErAPgXqDgAjJGN0pSqt0jEsD+JUEphfZmChCvh+FnSCT/8xjEswN4XpwgAHRC1/kRDAejoD4IlGq5Y7Ce8Rwy6gKOB6L/8xjEvASAWongAHQK1alrXjORBd09kwwmbPaP6v/0zfmKFAD/8xjEwQUwXngAA/goPvWNarHmwkKjAl0MWhhssjWrF55WDAL/8xjEwwOAWqjgAfJGKAOMJ88Kd0DB0A6ZD8NrwlR/Z///s/T/8xjEzAPYWqUAAHBC1RwA07MiZKZigjYa0OJpTQQEv//9Mur/8xjE0wPwWq2AALAqHBAZwGvMt3IJEc0vzWkyDzuSF9cCADb/8xjE2gUAWnQAAXglAmCvEFVzFK0BS7XIYYHKbD6f//9f6k3/8xjE3QT4XnQAA/ZEX5M0IzIYJxJfkQQLg2kAYADCtoLciQP/8xjE4AUYWnQAAPYlcYSghkkEOzaXLG7XP6P///V+tQgCKPr/8xjE4gU4YnQABPgokOpK18GCxAOUHff9WyNW8p/sEx/3UYP/8xjE5AQYWqUAAjJGF8QjBY2AxUGQHQH6EoVG/3ceOta/N13/8xjE6gYgVox4AHRCW+GYimgcwOWDQCT0lcaMfjyRSobWWF//8xjE6AWwWnngDjhH/vJY4WCvEARDzPFhif6FIU24ALA1BUr/8xjE6AZIVpm4AHQmhoRHi1Z1Tu6ViKIvQkxBTUU0LjCqqqr/8xjE5QVYVoQYAHZCqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqr/8xjE5gT4WqJAAHQKqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqr/8xjE6QZAWoh4AHZCqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqr/8xjE5wPAVoQAAHZBqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqr/8xjE7wbgXoB4A/hGqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqr/8xjE6gWAWpGgAHYmqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqr/8xjE6wUAXqDBVAACqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqr/8xjE7gtgiqABmngAqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqr/8xjE1wUoBhh5wAgAqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqo=';
