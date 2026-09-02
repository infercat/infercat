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

export async function getMe(t: Transport, secret: string): Promise<Me> {
  return (await call(t, secret, '/me')).json() as Promise<Me>;
}

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
  /** The host said it finished: `[DONE]`, or the usage-bearing final chunk the gateway injects. */
  | { kind: 'done' }
  /** The stream ended without the host saying so. The answer is truncated, not finished. */
  | { kind: 'eof' }
  | { kind: 'aborted' }
  | { kind: 'error'; code: string; error: FriendlyError };

/**
 * A chat completion as a stream of events. Nothing here throws: a failure anywhere — a non-200
 * head, a gateway error inside an already-flushed 200 stream, a broken tunnel, an abort during the
 * dial — arrives as the last event, so one reducer decides what the message says.
 */
export async function* chatEvents(
  t: Transport,
  secret: string,
  req: ChatRequest,
  signal?: AbortSignal,
): AsyncGenerator<StreamEvent, void, void> {
  let saidSo = false;
  try {
    const res = await call(t, secret, '/v1/chat/completions', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ ...req, stream: true }),
      ...(signal ? { signal } : {}),
    });
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

export function isAbort(err: unknown): boolean {
  return (err as { name?: string } | null)?.name === 'AbortError';
}

interface ChatChunk {
  choices?: { delta?: { content?: string; reasoning_content?: string } }[];
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
}

/** `retry`: the code means "this clears on its own", so the surface offers a wait, not a dead end. */
const COPY: Record<string, { title: string; detail: string; fatal?: boolean; retry?: boolean }> = {
  invalid_request: { title: 'The host could not read that request',
    detail: 'This is the app’s fault, not yours. Start a new chat; if it keeps happening the host and this app disagree about the API.' },
  not_found: { title: 'The host has no such endpoint',
    detail: 'This app is talking to something that is not a Bunny gateway, or to an older one.' },
  invalid_key: { fatal: true, title: 'The host does not recognise this invite',
    detail: 'It may have been rotated or deleted. Ask your host for a fresh one.' },
  // One vocabulary, said once: the friend has an invite, not a key, and not both in two registers.
  key_paused: { fatal: true, title: 'Your invite is paused',
    detail: 'Ask your host to resume it.' },
  key_revoked: { fatal: true, title: 'This invite was revoked',
    detail: 'Ask your host for a new one.' },
  model_not_allowed: { title: 'That model is not shared with you',
    detail: 'Pick one of the models in the picker — those are the ones this invite may use.' },
  body_too_large: { title: 'That message is too large to send',
    detail: 'Shorten it, or split what you are pasting into a couple of messages.' },
  context_too_long: { title: 'This conversation no longer fits the model',
    detail: 'Start a new chat, or shorten what you just sent.' },
  rate_limited: { retry: true, title: 'You are sending faster than the host allows',
    detail: 'The limit is per minute and clears on its own.' },
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

/** Turns anything thrown on the request path into copy a stranger can act on. */
export function describeError(err: unknown): FriendlyError {
  if (err instanceof GatewayError) {
    const copy = COPY[err.code];
    // Our copy says what to do about it; the host's sentence is evidence, not a replacement. Both.
    const said = err.message.trim();
    // A host that forgot Retry-After still means "wait": five seconds beats a button that lies.
    const seconds = err.retryAfterS ?? (copy?.retry ? 5 : undefined);
    const retry = seconds ? { retryAfterS: seconds } : {};
    if (copy) {
      return {
        title: copy.title,
        detail: copy.detail,
        ...(said && said !== copy.detail ? { hostSaid: said } : {}),
        ...(copy.fatal ? { fatal: true } : {}),
        ...retry,
      };
    }
    // status 0 = the error arrived inside an already-flushed 200 stream.
    return err.status > 0
      ? { title: `The host answered ${err.status}`, detail: said, ...retry }
      : { title: 'The host stopped the reply', detail: said || 'The stream ended with an error.', ...retry };
  }
  if (isAbort(err)) {
    return { title: 'Stopped', detail: 'You stopped this reply.' };
  }
  return {
    title: 'The connection to the host broke',
    detail: err instanceof Error ? err.message : String(err),
  };
}
