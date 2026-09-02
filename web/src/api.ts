// The gateway HTTP API (docs/ARCHITECTURE.md §Gateway HTTP API) and the friendly copy for every
// error code it can return. Nothing else in the app parses a gateway response.
import type { Transport } from './transport';

export interface Limits {
  rpm: number;
  tpm: number;
  max_concurrent: number;
  max_output_tokens: number;
  max_context: number;
  daily_tokens: number;
  models?: string[];
}

export interface Me {
  key: { id: string; name: string; status: 'active' | 'paused' | 'revoked' };
  limits: Limits;
  usage: { rpm_used: number; tpm_used: number; today_tokens: number; in_flight: number };
  host: {
    name: string;
    upstream: { kind: string; healthy: boolean; model_context: number };
    models: string[];
    relay: { region: string };
    /** The host runs with --log-prompts. Absent on a gateway older than ticket 006: absent = false. */
    log_prompts?: boolean;
  };
}

/** The host's name, trimmed. It can be empty, and "You’re on ." is not a sentence: callers check. */
export function hostName(me: Me): string {
  return me.host.name.trim();
}

/** True only when the host has said so; a gateway that does not send the field is not logging. */
export function logsPrompts(me: Me): boolean {
  return me.host.log_prompts === true;
}

/**
 * A model id is a filename with a build in it; a reader wants the model. "gemma-4-E2B-it-Q4_K_M.gguf"
 * reads as "Gemma 4 E2B" (014 promise 11). The full id is not hidden — Settings shows it — but it is
 * not what belongs in a header next to a host's name.
 */
export function modelLabel(id: string): string {
  const base = (id.split('/').pop() ?? id).replace(/\.(gguf|safetensors|bin)$/i, '');
  const parts = base.split(/[-_]/).filter((p) => p !== '');
  const out: string[] = [];
  for (const p of parts) {
    // Quantisation and packaging tags say nothing about which model this is.
    if (/^(q\d|iq\d|f16|bf16|fp\d+|k|m|s|l|xs|xl|gguf|ggml|mlx|awq|gptq|int\d|\d+bit)$/i.test(p)) break;
    if (/^(it|instruct|chat|hf)$/i.test(p) && out.length > 0) continue;
    // "e2b" and "v4" are sizes and versions, not words: they read as themselves in caps.
    out.push(/\d/.test(p) && p.length <= 4 ? p.toUpperCase() : p.charAt(0).toUpperCase() + p.slice(1));
  }
  return out.join(' ') || base;
}

export class GatewayError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly type: string,
    message: string,
    readonly retryAfterS?: number,
  ) {
    super(message);
    this.name = 'GatewayError';
  }
}

async function call(
  t: Transport,
  secret: string,
  path: string,
  init: RequestInit = {},
): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set('authorization', `Bearer ${secret}`);
  const res = await t.fetch(path, { ...init, headers });
  if (!res.ok) throw await gatewayError(res);
  return res;
}

async function gatewayError(res: Response): Promise<GatewayError> {
  let message = `The host answered ${res.status}.`;
  let code = '';
  let type = '';
  try {
    const parsed = (await res.json()) as { error?: { message?: string; code?: string; type?: string } };
    if (parsed?.error) {
      message = parsed.error.message ?? message;
      code = parsed.error.code ?? '';
      type = parsed.error.type ?? '';
    }
  } catch {
    /* not the documented error shape; the status alone is the message */
  }
  const retry = Number(res.headers.get('retry-after'));
  return new GatewayError(
    res.status,
    code,
    type,
    message,
    Number.isFinite(retry) && retry > 0 ? Math.ceil(retry) : undefined,
  );
}

/**
 * `signal` is not optional in spirit: on a host that has gone away a /me over a dead tunnel session
 * hangs for the relay's own timeout, and until it does the header keeps showing numbers that are no
 * longer true. A bound on it is what makes "unknown" arrive while it still means something
 * (014 promise 3).
 */
export async function getMe(t: Transport, secret: string, signal?: AbortSignal): Promise<Me> {
  return (await call(t, secret, '/me', signal ? { signal } : {})).json() as Promise<Me>;
}

/** How long a background /me gets before its silence is itself the news. */
export const ME_TIMEOUT_MS = 10_000;

export async function getModels(t: Transport, secret: string): Promise<string[]> {
  const body = (await (await call(t, secret, '/v1/models')).json()) as { data?: { id?: string }[] };
  return (body.data ?? []).map((m) => m.id).filter((id): id is string => typeof id === 'string');
}

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant';
  content: string;
}

export interface ChatRequest {
  model: string;
  messages: ChatMessage[];
  temperature?: number;
}

/**
 * Everything that can happen to a reply, in arrival order. The last event is always one of
 * `done`, `eof`, `aborted` or `error` — a stream that merely stops producing tokens is not an
 * ending, and `reduceReply` in stream.ts is what turns these into a terminal status.
 */
export type StreamEvent =
  | { kind: 'reasoning'; text: string }
  | { kind: 'content'; text: string }
  | { kind: 'usage'; in: number; out: number }
  /** Nothing has come back yet and it has been long enough to say so out loud (014 promise 1). */
  | { kind: 'waiting' }
  /** The engine stopped because the reply hit its cap, not because it was finished (promise 14). */
  | { kind: 'capped' }
  /** The host said it finished: `[DONE]`, or the usage-bearing final chunk the gateway injects. */
  | { kind: 'done' }
  /** The stream ended without the host saying so. The answer is truncated, not finished. */
  | { kind: 'eof' }
  | { kind: 'aborted' }
  | { kind: 'error'; code: string; error: FriendlyError };

/**
 * How long the client waits for the *host* — not for the model. `noticeMs` is when the reply says
 * "still waiting"; `answerMs` is when a host that has not even answered the HTTP request is
 * declared asleep. The clock stops the moment a response head arrives: from then on the host is
 * demonstrably awake and only its own deadlines apply, so a slow GPU is never called offline
 * (014 promise 1; the bridge's own 30 s dial timeout is too long to be the first thing a friend
 * learns from).
 */
export interface StreamDeadlines {
  noticeMs: number;
  answerMs: number;
}

export const DEFAULT_DEADLINES: StreamDeadlines = { noticeMs: 5_000, answerMs: 15_000 };

/**
 * A chat completion as a stream of events. Nothing here throws: a failure anywhere — a non-200
 * head, a gateway error inside an already-flushed 200 stream, a broken tunnel, an abort during the
 * dial, a host that never answers at all — arrives as the last event, so one reducer decides what
 * the message says.
 */
export async function* chatEvents(
  t: Transport,
  secret: string,
  req: ChatRequest,
  signal?: AbortSignal,
  deadlines: StreamDeadlines = DEFAULT_DEADLINES,
  hostName?: string,
): AsyncGenerator<StreamEvent, void, void> {
  const pump = new Pump<StreamEvent>();
  const ac = new AbortController();
  const relay = () => ac.abort();
  if (signal?.aborted) ac.abort();
  else signal?.addEventListener('abort', relay, { once: true });

  let answered = false; // a response head arrived: the host is awake, whatever the model is doing
  let spoke = false; // a token arrived: there is something on screen
  let asleep = false; // we, not the reader, aborted — and this is why
  const notice = setTimeout(() => !spoke && pump.push({ kind: 'waiting' }), deadlines.noticeMs);
  const giveUp = setTimeout(() => {
    // What we know at this point is that the host has not answered at all. Two things cause that:
    // it is gone, or it is queued behind a full engine (the gateway parks a request for up to 30 s).
    // A tunnel ping cannot tell them apart — measured against a real host killed mid-send, the
    // relay kept answering pings for ~45 s after the process died — so the honest move is to stop
    // waiting and say the likelier thing with the hedge the copy already carries ("probably"). A
    // busy host that answers later says so in its own words, and Reconnect costs the reader 2 s.
    if (!answered) {
      asleep = true;
      ac.abort();
    }
  }, deadlines.answerMs);

  void (async () => {
    try {
      for await (const ev of rawChatEvents(t, secret, req, ac.signal, () => (answered = true))) {
        if (ev.kind === 'reasoning' || ev.kind === 'content') spoke = true;
        // Our own abort must not be reported as the reader's Stop, nor as a bare transport string.
        pump.push(asleep && (ev.kind === 'aborted' || ev.kind === 'error') ? hostAsleep(hostName) : ev);
      }
    } finally {
      pump.end();
    }
  })();

  try {
    for await (const ev of pump.drain()) yield ev;
  } finally {
    clearTimeout(notice);
    clearTimeout(giveUp);
    signal?.removeEventListener('abort', relay);
    ac.abort(); // an abandoned generator must not leave a request running
  }
}

/** The failure the reviewer's phone actually hit, in the friend's words rather than the dialler's. */
function hostAsleep(hostName?: string): StreamEvent {
  return {
    kind: 'error',
    code: 'host_asleep',
    error: describeError(new GatewayError(0, 'host_asleep', 'connection_error', ''), hostName),
  };
}

/** A tiny push queue: the timers above and the stream below both produce into one ordered stream. */
class Pump<T> {
  private items: T[] = [];
  private wake: (() => void) | null = null;
  private ended = false;

  push(v: T): void {
    this.items.push(v);
    this.wake?.();
    this.wake = null;
  }

  end(): void {
    this.ended = true;
    this.wake?.();
    this.wake = null;
  }

  async *drain(): AsyncGenerator<T, void, void> {
    for (;;) {
      while (this.items.length > 0) yield this.items.shift() as T;
      if (this.ended) return;
      await new Promise<void>((resolve) => (this.wake = resolve));
    }
  }
}

async function* rawChatEvents(
  t: Transport,
  secret: string,
  req: ChatRequest,
  signal: AbortSignal,
  onAnswered: () => void,
): AsyncGenerator<StreamEvent, void, void> {
  let saidSo = false;
  try {
    const res = await call(t, secret, '/v1/chat/completions', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ ...req, stream: true }),
      signal,
    });
    onAnswered();
    if (!res.body) throw new Error('the host sent a reply with no body');
    for await (const data of sseData(res.body)) {
      if (data === '[DONE]') {
        yield { kind: 'done' };
        return;
      }
      let chunk: ChatChunk;
      try {
        chunk = JSON.parse(data) as ChatChunk;
      } catch {
        continue; // a partial or non-JSON event; the next one carries the tokens
      }
      // An error can arrive inside a 200 stream: the head was flushed before it happened.
      if (chunk.error) {
        const code = chunk.error.code ?? '';
        yield {
          kind: 'error',
          code,
          error: describeError(
            new GatewayError(0, code, chunk.error.type ?? '', chunk.error.message ?? ''),
          ),
        };
        return;
      }
      const delta = chunk.choices?.[0]?.delta;
      if (delta?.reasoning_content) yield { kind: 'reasoning', text: delta.reasoning_content };
      if (delta?.content) yield { kind: 'content', text: delta.content };
      // "length" is the engine saying it ran out of allowance, not that it had finished. A reply
      // that ends there is complete as a transfer and unfinished as an answer (014 promise 14).
      if (chunk.choices?.[0]?.finish_reason === 'length') yield { kind: 'capped' };
      if (chunk.usage) {
        saidSo = true;
        yield {
          kind: 'usage',
          in: chunk.usage.prompt_tokens ?? 0,
          out: chunk.usage.completion_tokens ?? 0,
        };
      }
    }
    yield saidSo ? { kind: 'done' } : { kind: 'eof' };
  } catch (err) {
    if (isAbort(err)) {
      yield { kind: 'aborted' };
      return;
    }
    yield {
      kind: 'error',
      code: err instanceof GatewayError ? err.code : '',
      error: describeError(err),
    };
  }
}

/**
 * Failures that mean *this connection* is finished, whatever the host is doing. Retrying a request
 * over a broken tunnel session costs the reader another 30 s and tells them nothing new, so these
 * offer a reconnect instead of a retry (014 promise 13).
 */
export function needsRedial(code: string): boolean {
  return code === 'host_asleep' || code === '';
}

export function isAbort(err: unknown): boolean {
  return (err as { name?: string } | null)?.name === 'AbortError';
}

interface ChatChunk {
  choices?: { delta?: { content?: string; reasoning_content?: string }; finish_reason?: string | null }[];
  usage?: { prompt_tokens?: number; completion_tokens?: number };
  error?: { message?: string; code?: string; type?: string };
}

/** Yields the `data:` payload of each SSE event as it arrives. */
export async function* sseData(
  stream: ReadableStream<Uint8Array>,
): AsyncGenerator<string, void, void> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (value) buf += decoder.decode(value, { stream: true });
      if (done) break;
      // Line endings are matched here rather than normalised on arrival: a CRLF split across two
      // reads used to leave a stray \r glued to the payload, which turned `[DONE]` into `[DONE]\r`
      // and made every stream look truncated.
      for (;;) {
        const end = EVENT_END.exec(buf);
        if (!end) break;
        const payload = dataOf(buf.slice(0, end.index));
        buf = buf.slice(end.index + end[0].length);
        if (payload !== null) yield payload;
      }
    }
    const tail = dataOf(buf);
    if (tail !== null) yield tail;
  } finally {
    reader.releaseLock();
  }
}

const EVENT_END = /\r?\n\r?\n/;

function dataOf(block: string): string | null {
  const data = block
    .split(/\r?\n/)
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).replace(/^ /, ''))
    .join('\n');
  return data === '' ? null : data;
}

export interface FriendlyError {
  title: string;
  detail: string;
  /** The host's own diagnostic, shown *next to* our copy — never instead of it. */
  hostSaid?: string;
  /** Seconds the host asked us to wait, when it said so. */
  retryAfterS?: number;
  /** The invite itself is no longer usable: go back to the connect screen. */
  fatal?: boolean;
  /** The invite is paused: the session degrades, the chat stays, nothing is thrown away. */
  paused?: boolean;
}

/**
 * `retry`: the code means "this clears on its own", so the surface offers a wait, not a dead end.
 * `paused`: the invite is not usable *right now* but the chat is still the reader's — the session
 * degrades instead of ending (014 promise 2). `fatal` is reserved for the two codes no waiting can
 * fix. `{host}` is filled in with the host's name, or "your host" when it has none.
 */
const COPY: Record<
  string,
  { title: string; detail: string; fatal?: boolean; retry?: boolean; paused?: boolean }
> = {
  invalid_request: { title: 'The host could not read that request',
    detail: 'This is the app’s fault, not yours. Start a new chat; if it keeps happening the host and this app disagree about the API.' },
  not_found: { title: 'The host has no such endpoint',
    detail: 'This app is talking to something that is not a Bunny gateway, or to an older one.' },
  invalid_key: { fatal: true, title: 'The host does not recognise this invite',
    detail: 'It may have been rotated or deleted. Ask {host} for a fresh code.' },
  // Paused is a pause, not an ending. Mid-session the chat stays where it is (the header says so,
  // session.degradedLine); this copy is for the one place a pause really is a dead end — a connect
  // attempt, where there is no chat to keep.
  key_paused: { paused: true, title: 'Your invite is paused',
    detail: 'Ask {host} to resume it, then try again — your chats are still on this device.' },
  key_revoked: { fatal: true, title: 'This invite was revoked',
    detail: 'Ask {host} for a new code.' },
  // The blocker: the machine on the other end is not there (014 promise 1).
  host_asleep: { retry: true, title: '{host} didn’t answer',
    detail: 'It’s probably asleep or offline — your message is saved, try again in a minute.' },
  model_not_allowed: { title: 'That model is not shared with you',
    detail: 'Pick one of the models in the picker — those are the ones this invite may use.' },
  body_too_large: { title: 'That message is too large to send',
    detail: 'Shorten it, or split what you are pasting into a couple of messages.' },
  context_too_long: { title: 'This conversation no longer fits the model',
    detail: 'Start a new chat, or shorten what you just sent.' },
  rate_limited: { retry: true, title: 'Too fast for this invite',
    detail: '{host} allows a set number of messages a minute. The count clears on its own.' },
  concurrency_limited: { retry: true, title: 'One reply at a time',
    detail: 'This invite may have one request in flight. Wait for the current reply to finish.' },
  budget_exhausted: { title: "Today's token budget is used up",
    detail: 'The host sets a daily cap per invite. It resets, or they can raise it.' },
  queue_timeout: { retry: true, title: "The host's GPU is busy",
    detail: 'Requests are queued and yours waited too long. Try again in a moment.' },
  upstream_down: { retry: true, title: "The host's engine is offline",
    detail: 'Their machine is reachable but the model server is not running. Nothing you can fix.' },
  upstream_error: { title: "The host's engine returned an error",
    detail: 'The tunnel and the gateway are fine; the model server itself failed.' },
};

/**
 * Turns anything thrown on the request path into copy a stranger can act on. `host` is the host's
 * display name: the copy names the machine that let the reader down rather than "the host", and a
 * nameless gateway falls back to "your host" so no sentence ever has a hole in it.
 */
export function describeError(err: unknown, host?: string): FriendlyError {
  const who = (host ?? '').trim() || 'your host';
  const fill = (s: string): string => s.replaceAll('{host}', who);
  if (err instanceof GatewayError) {
    const copy = COPY[err.code];
    // Our copy says what to do about it; the host's sentence is evidence, not a replacement. Both.
    const said = err.message.trim();
    // A host that forgot Retry-After still means "wait": five seconds beats a button that lies.
    const seconds = err.retryAfterS ?? (copy?.retry ? 5 : undefined);
    const retry = seconds ? { retryAfterS: seconds } : {};
    if (copy) {
      const detail = fill(copy.detail);
      return {
        title: fill(copy.title),
        detail,
        ...(said && said !== detail ? { hostSaid: said } : {}),
        ...(copy.fatal ? { fatal: true } : {}),
        ...(copy.paused ? { paused: true } : {}),
        ...retry,
      };
    }
    // status 0 = the error arrived inside an already-flushed 200 stream.
    return err.status > 0
      ? { title: `${who} answered ${err.status}`, detail: said, ...retry }
      : { title: `${who} stopped the reply`, detail: said || 'The stream ended with an error.', ...retry };
  }
  if (isAbort(err)) {
    return { title: 'Stopped', detail: 'You stopped this reply.' };
  }
  // A raw transport string ("dial port 80: context deadline exceeded") is never the primary copy:
  // it goes to `hostSaid`, which every surface renders as evidence behind a "Details" disclosure.
  return {
    title: `The connection to ${who} broke`,
    detail: 'The tunnel dropped part way through. Try again — if it keeps happening, their machine may have gone to sleep.',
    ...(err instanceof Error && err.message.trim() !== '' ? { hostSaid: err.message.trim() } : {}),
  };
}
