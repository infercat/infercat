// The message lifecycle: one pure reducer from the stream events in api.ts to what the bubble
// says. The rule it exists to enforce is that a reply is finished only when the host says it is —
// running out of tokens is not an ending, and must never render as one (pm/BELIEFS.md, "Surfaces
// tell the truth").
import type { StreamEvent } from './api';
import type { Message, MessageStatus } from './storage';

/** The parts of a Message this machine owns. `status` is undefined while it is still streaming. */
export type Reply = Pick<Message, 'content' | 'reasoning' | 'tokens' | 'status' | 'note'>;

export const NEW_REPLY: Reply = { content: '', reasoning: '' };

export function reduceReply(r: Reply, e: StreamEvent): Reply {
  switch (e.kind) {
    case 'reasoning':
      return { ...r, reasoning: (r.reasoning ?? '') + e.text };
    case 'content':
      return { ...r, content: r.content + e.text };
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
      // Our copy and the host's own sentence, both — one is what to do, the other is evidence.
      return finish(
        r,
        'interrupted',
        `${e.error.title}. ${e.error.detail}${e.error.hostSaid ? ` The host said: “${e.error.hostSaid}”` : ''}`,
      );
  }
}

/**
 * "Done" is the host's word about the transfer, not about the answer. A reply that ends with only
 * thinking in it (a small `max_tokens` on a reasoning model does exactly this), or with nothing at
 * all, has no answer in it — and an empty assistant row that looks complete is the lie.
 */
function finish(r: Reply, status: MessageStatus, note?: string): Reply {
  const empty = r.content.trim() === '';
  const thought = (r.reasoning ?? '').trim() !== '';
  if (empty && status === 'complete') {
    return {
      ...r,
      status: 'no_answer',
      note: thought
        ? 'The model used its whole reply thinking and never got to an answer. Regenerate, or ask for a shorter answer.'
        : 'The host finished without sending an answer.',
    };
  }
  if (empty && status === 'stopped') {
    return {
      ...r,
      status,
      note: thought ? 'You stopped this while it was still thinking.' : 'You stopped this before it began.',
    };
  }
  return { ...r, status, ...(note ? { note } : {}) };
}
