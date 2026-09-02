// The message lifecycle: one pure reducer from the stream events in api.ts to what the bubble
// says. The rule it exists to enforce is that a reply is finished only when the host says it is —
// running out of tokens is not an ending, and must never render as one (pm/BELIEFS.md, "Surfaces
// tell the truth").
import type { StreamEvent } from './api';
import type { Message, MessageStatus } from './storage';

/** The parts of a Message this machine owns. `status` is undefined while it is still streaming. */
export type Reply = Pick<
  Message,
  'content' | 'reasoning' | 'tokens' | 'status' | 'note' | 'waiting' | 'details' | 'capped'
>;

export const NEW_REPLY: Reply = { content: '', reasoning: '' };

export function reduceReply(r: Reply, e: StreamEvent): Reply {
  switch (e.kind) {
    // Not an ending: a reply that has produced nothing for long enough that saying nothing would
    // itself be a lie. It clears on the first token and on every terminal event.
    case 'waiting':
      return r.status === undefined ? { ...r, waiting: true } : r;
    case 'capped':
      return { ...r, capped: true };
    case 'reasoning':
      return { ...r, reasoning: (r.reasoning ?? '') + e.text, waiting: false };
    case 'content':
      return { ...r, content: r.content + e.text, waiting: false };
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
    case 'aborted':
      return finish(r, 'stopped', 'You stopped this reply.');
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
 * all "wait, then it works", and a countdown said twice is a countdown nobody believes.
 */
const SHORT: Record<string, string> = {
  rate_limited: 'Too fast — not sent.',
  concurrency_limited: 'Not sent — one reply at a time.',
  queue_timeout: 'Not sent — the GPU was busy.',
  budget_exhausted: 'Not sent — today’s tokens are used up.',
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
  const base = { ...r, waiting: false, ...(details ? { details } : {}) };
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
