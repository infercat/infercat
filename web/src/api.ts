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
  };
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

export interface ChatDelta {
  content?: string;
  reasoning?: string;
  usage?: { prompt_tokens: number; completion_tokens: number };
}

export interface ChatRequest {
  model: string;
  messages: ChatMessage[];
  temperature?: number;
}

/** Streams a chat completion, calling `onDelta` as each SSE event lands. */
export async function streamChat(
  t: Transport,
  secret: string,
  req: ChatRequest,
  onDelta: (d: ChatDelta) => void,
  signal?: AbortSignal,
): Promise<void> {
  const res = await call(t, secret, '/v1/chat/completions', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ ...req, stream: true }),
    ...(signal ? { signal } : {}),
  });
  if (!res.body) throw new Error('the host sent a reply with no body');
  for await (const data of sseData(res.body)) {
    if (data === '[DONE]') return;
    let chunk: ChatChunk;
    try {
      chunk = JSON.parse(data) as ChatChunk;
    } catch {
      continue; // a partial or non-JSON event; the next one carries the tokens
    }
    const delta = chunk.choices?.[0]?.delta;
    if (delta?.reasoning_content) onDelta({ reasoning: delta.reasoning_content });
    if (delta?.content) onDelta({ content: delta.content });
    if (chunk.usage) {
      onDelta({
        usage: {
          prompt_tokens: chunk.usage.prompt_tokens ?? 0,
          completion_tokens: chunk.usage.completion_tokens ?? 0,
        },
      });
    }
  }
}

interface ChatChunk {
  choices?: { delta?: { content?: string; reasoning_content?: string } }[];
  usage?: { prompt_tokens?: number; completion_tokens?: number };
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
      if (value) buf += decoder.decode(value, { stream: true }).replace(/\r\n/g, '\n');
      if (done) break;
      let i: number;
      while ((i = buf.indexOf('\n\n')) >= 0) {
        const payload = dataOf(buf.slice(0, i));
        buf = buf.slice(i + 2);
        if (payload !== null) yield payload;
      }
    }
    const tail = dataOf(buf);
    if (tail !== null) yield tail;
  } finally {
    reader.releaseLock();
  }
}

function dataOf(block: string): string | null {
  const data = block
    .split('\n')
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).replace(/^ /, ''))
    .join('\n');
  return data === '' ? null : data;
}

export interface FriendlyError {
  title: string;
  detail: string;
  /** Seconds the host asked us to wait, when it said so. */
  retryAfterS?: number;
  /** The invite itself is no longer usable: go back to the connect screen. */
  fatal?: boolean;
}

const COPY: Record<string, { title: string; detail: string; fatal?: boolean }> = {
  invalid_key: { fatal: true, title: 'The host does not recognise this invite',
    detail: 'It may have been rotated or deleted. Ask your host for a fresh one.' },
  key_paused: { fatal: true, title: 'Your access is paused',
    detail: 'The host paused this invite. It works again the moment they resume it.' },
  key_revoked: { fatal: true, title: 'This invite was revoked',
    detail: 'The host turned it off for good. You will need a new invite from them.' },
  model_not_allowed: { title: 'That model is not shared with you',
    detail: 'Pick one of the models in the picker — those are the ones this invite may use.' },
  body_too_large: { title: 'That message is too large to send',
    detail: 'Shorten it, or split what you are pasting into a couple of messages.' },
  context_too_long: { title: 'This conversation no longer fits the model',
    detail: 'Start a new chat, or shorten what you just sent.' },
  rate_limited: { title: 'You are sending faster than the host allows',
    detail: 'The limit is per minute and clears on its own.' },
  concurrency_limited: { title: 'One reply at a time',
    detail: 'This invite may have one request in flight. Wait for the current reply to finish.' },
  budget_exhausted: { title: "Today's token budget is used up",
    detail: 'The host sets a daily cap per invite. It resets, or they can raise it.' },
  queue_timeout: { title: "The host's GPU is busy",
    detail: 'Requests are queued and yours waited too long. Try again in a moment.' },
  upstream_down: { title: "The host's engine is offline",
    detail: 'Their machine is reachable but the model server is not running. Nothing you can fix.' },
  upstream_error: { title: "The host's engine returned an error",
    detail: 'The tunnel and the gateway are fine; the model server itself failed.' },
};

/** Turns anything thrown on the request path into copy a stranger can act on. */
export function describeError(err: unknown): FriendlyError {
  if (err instanceof GatewayError) {
    const copy = COPY[err.code];
    if (copy) {
      return {
        title: copy.title,
        detail: err.message || copy.detail,
        ...(copy.fatal ? { fatal: true } : {}),
        ...(err.retryAfterS ? { retryAfterS: err.retryAfterS } : {}),
      };
    }
    return {
      title: `The host answered ${err.status}`,
      detail: err.message,
      ...(err.retryAfterS ? { retryAfterS: err.retryAfterS } : {}),
    };
  }
  if (err instanceof DOMException && err.name === 'AbortError') {
    return { title: 'Stopped', detail: 'You stopped this reply.' };
  }
  return {
    title: 'The connection to the host broke',
    detail: err instanceof Error ? err.message : String(err),
  };
}
