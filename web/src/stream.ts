// The message lifecycle: one pure reducer from the stream events in api.ts to what the bubble
// says. The rule it exists to enforce is that a reply is finished only when the host says it is —
// running out of tokens is not an ending, and must never render as one (pm/BELIEFS.md, "Surfaces
// tell the truth").
import type { StreamEvent } from './api';
import type { Message, MessageStatus } from './storage';

/** The parts of a Message this machine owns. `status` is undefined while it is still streaming. */
export type Reply = Pick<
  Message,
  'content' | 'reasoning' | 'tokens' | 'status' | 'note' | 'waiting' | 'queued' | 'details' | 'capped'
>;

export const NEW_REPLY: Reply = { content: '', reasoning: '' };

export function reduceReply(r: Reply, e: StreamEvent): Reply {
  switch (e.kind) {
    // Not an ending: a reply that has produced nothing for long enough that saying nothing would
    // itself be a lie. It clears on the first token and on every terminal event.
    // The newer of the two says what the silence is: a keepalive means the host has the request
    // and it is in line; a notice that outlives the keepalives means the line is over and the
    // model is at work — neither is an ending, and neither is a host that is not there (018).
    case 'waiting':
      return r.status === undefined ? { ...r, waiting: true, queued: false } : r;
    case 'queued':
      return r.status === undefined ? { ...r, queued: true, waiting: false } : r;
    case 'capped':
      return { ...r, capped: true };
    case 'reasoning':
      return { ...r, reasoning: (r.reasoning ?? '') + e.text, waiting: false, queued: false };
    case 'content':
      return { ...r, content: r.content + e.text, waiting: false, queued: false };
    case 'usage':
      return { ...r, tokens: { in: e.in, out: e.out } };
    case 'done':
      return finish(r, 'complete');
    case 'eof':
      return finish(
        r,
        'interrupted',
        'The connection dropped before the host finished this reply — what is above is only part of it.',
      );
    // "You stopped this reply" is reserved for the reader's own Stop (020 promise 2): an abort the
    // app asked for carries its reason and is a cut, not a choice.
    case 'aborted':
      return e.why ? finish(r, 'interrupted', e.why) : finish(r, 'stopped', 'You stopped this reply.');
    case 'error':
      // Said once (014 promise 10). A wait the reader has to sit out is explained by the banner,
      // with its countdown, so the message only says it did not go; everything else is explained
      // where it happened. Either way the host's own sentence is evidence, not primary copy: it
      // goes to `details`, which the surface hides behind a disclosure (014 promise 1).
      return finish(
        r,
        'interrupted',
        saidInBanner(e.code) ? SHORT[e.code] : `${e.error.title}. ${e.error.detail}`,
        e.error.hostSaid,
      );
  }
}

/**
 * The failures whose explanation belongs in the one banner rather than under the message: they are
 * all "wait, then it works", and a countdown said twice is a countdown nobody believes. The three
 * invite states are the same shape: the header line says what the host did and what to do about
 * it, so the message says only that it did not go (020 promise 5).
 */
const SHORT: Record<string, string> = {
  rate_limited: 'Too fast — not sent.',
  concurrency_limited: 'Not sent — one reply at a time.',
  queue_timeout: 'Not sent — every slot was taken.',
  budget_exhausted: 'Not sent — today’s tokens are used up.',
  key_paused: 'Not sent — your invite is paused.',
  key_revoked: 'Not sent — this invite was revoked.',
  invalid_key: 'Not sent — this invite no longer works.',
};

export function saidInBanner(code: string): boolean {
  return code in SHORT;
}

/**
 * "Done" is the host's word about the transfer, not about the answer. A reply that ends with only
 * thinking in it (a small `max_tokens` on a reasoning model does exactly this), or with nothing at
 * all, has no answer in it — and an empty assistant row that looks complete is the lie.
 */
function finish(r: Reply, status: MessageStatus, note?: string, details?: string): Reply {
  const empty = r.content.trim() === '';
  const thought = (r.reasoning ?? '').trim() !== '';
  const base = { ...r, waiting: false, queued: false, ...(details ? { details } : {}) };
  if (empty && status === 'complete') {
    return {
      ...base,
      status: 'no_answer',
      note: thought
        ? 'The model used its whole reply thinking and never got to an answer. Regenerate, or ask for a shorter answer.'
        : 'The host finished without sending an answer.',
    };
  }
  if (empty && status === 'stopped') {
    return {
      ...base,
      status,
      note: thought ? 'You stopped this while it was still thinking.' : 'You stopped this before it began.',
    };
  }
  return { ...base, status, ...(note ? { note } : {}) };
}

// ---- how a reply that ran out of allowance ended (020 promise 3) --------------------------------

/**
 * Which wall a `finish_reason: "length"` reply hit. `capped`: the invite's per-reply cap, and more
 * of the same reply helps (Continue). `context`: the conversation has filled the model's memory,
 * and nothing but a new chat helps — the copy that says "reply limit" next to numbers that add up
 * to the context is the self-refuting screenshot this exists to end. `length`: the engine stopped
 * for a length it did not tell us about. Null: it did not stop for length at all.
 *
 * One pure function over four numbers, so the two surfaces that say it (the ending line and the
 * context meter) cannot disagree. `slack` absorbs the tokens an engine reserves or miscounts at
 * the edge of its window: an exact sum is the common case (2971 + 1125 = 4096), not the only one.
 */
export type Ending = 'capped' | 'context' | 'length';

export function replyEnding(
  capped: boolean | undefined,
  tokens: { in: number; out: number } | undefined,
  maxOutputTokens: number,
  modelContext: number,
): Ending | null {
  if (!capped) return null;
  if (!tokens) return 'length';
  if (maxOutputTokens > 0 && tokens.out >= maxOutputTokens) return 'capped';
  if (modelContext > 0 && tokens.in + tokens.out >= modelContext - contextSlack(modelContext)) return 'context';
  return 'length';
}

/** What the last reply says the whole thread cost the engine: the context meter's number. */
export function contextUsed(messages: readonly Pick<Message, 'role' | 'tokens'>[]): number | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m?.role === 'assistant' && m.tokens) return m.tokens.in + m.tokens.out;
  }
  return null;
}

function contextSlack(modelContext: number): number {
  return Math.max(8, Math.floor(modelContext / 64));
}
