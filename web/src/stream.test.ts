// The message lifecycle. Every case here is a way a reply can end that used to render as a
// finished answer (promises 1 and 2).
import { describe, expect, it } from 'vitest';
import type { StreamEvent } from './api';
import { NEW_REPLY, reduceReply, saidInBanner, type Reply } from './stream';

function play(...events: StreamEvent[]): Reply {
  return events.reduce(reduceReply, NEW_REPLY);
}

const say = (text: string): StreamEvent => ({ kind: 'content', text });
const think = (text: string): StreamEvent => ({ kind: 'reasoning', text });

describe('a reply that finishes', () => {
  it('is complete, with its text, thinking and token counts', () => {
    const r = play(think('hmm'), say('Hello'), say(' world'), { kind: 'usage', in: 11, out: 4 }, { kind: 'done' });
    expect(r).toEqual({
      content: 'Hello world',
      reasoning: 'hmm',
      tokens: { in: 11, out: 4 },
      status: 'complete',
      waiting: false,
    });
    expect(r.note).toBeUndefined();
  });
});

describe('a reply that does not finish', () => {
  it('keeps its partial text and says the connection dropped', () => {
    const r = play(say('half an ans'), { kind: 'eof' });
    expect(r.content).toBe('half an ans');
    expect(r.status).toBe('interrupted');
    expect(r.note).toMatch(/only part of it/);
  });

  it('carries the gateway copy when the stream carried an error', () => {
    const r = play(say('half'), {
      kind: 'error',
      code: 'upstream_error',
      error: { title: "The host's engine returned an error", detail: 'The model server itself failed.' },
    });
    expect(r.status).toBe('interrupted');
    expect(r.note).toBe("The host's engine returned an error. The model server itself failed.");
  });

  // 007 promise 11 and 014 promise 1: our copy and the host's diagnostic, both — but the raw
  // sentence is evidence, so it goes to `details` (a disclosure) and never into the primary line.
  it('shows the host diagnostic beside our copy, behind a disclosure, not instead of it', () => {
    const r = play({
      kind: 'error',
      code: 'upstream_error',
      error: {
        title: "The host's engine returned an error",
        detail: 'The model server itself failed.',
        hostSaid: 'The engine dropped the request after 6 s.',
      },
    });
    expect(r.note).toContain('The model server itself failed.');
    expect(r.note).not.toContain('The engine dropped the request after 6 s.');
    expect(r.details).toBe('The engine dropped the request after 6 s.');
  });

  it('is stopped, not interrupted, when the reader pressed Stop', () => {
    const r = play(say('as far as I got'), { kind: 'aborted' });
    expect(r).toMatchObject({ status: 'stopped', content: 'as far as I got', note: 'You stopped this reply.' });
  });
});

// Promise 2: never an empty assistant row.
describe('a reply with no answer in it', () => {
  it('is no_answer when the host finishes having only thought', () => {
    const r = play(think('I should be brief'), { kind: 'usage', in: 9, out: 40 }, { kind: 'done' });
    expect(r.status).toBe('no_answer');
    expect(r.note).toMatch(/whole reply thinking/);
    expect(r.tokens).toEqual({ in: 9, out: 40 });
  });

  it('is no_answer when the host finishes having sent nothing at all', () => {
    const r = play({ kind: 'done' });
    expect(r.status).toBe('no_answer');
    expect(r.note).toBe('The host finished without sending an answer.');
  });

  it('says the stop landed during thinking', () => {
    expect(play(think('still working'), { kind: 'aborted' }).note).toBe(
      'You stopped this while it was still thinking.',
    );
    expect(play({ kind: 'aborted' }).note).toBe('You stopped this before it began.');
  });

  it('does not call whitespace an answer', () => {
    expect(play(say('   \n'), { kind: 'done' }).status).toBe('no_answer');
  });

  it('an interrupted reply with nothing in it still explains itself', () => {
    const r = play({ kind: 'eof' });
    expect(r.status).toBe('interrupted');
    expect(r.note?.length ?? 0).toBeGreaterThan(10);
  });
});

// 014 promise 1: waiting is not an ending. 014 promise 10: a wait explained by the banner is not
// explained again under the message.
describe('a reply that has not started yet', () => {
  it('marks itself as waiting and unmarks itself on the first token', () => {
    expect(play({ kind: 'waiting' }).waiting).toBe(true);
    expect(play({ kind: 'waiting' }).status).toBeUndefined();
    expect(play({ kind: 'waiting' }, say('hi')).waiting).toBe(false);
    expect(play({ kind: 'waiting' }, think('hm')).waiting).toBe(false);
  });

  it('never leaves a finished reply claiming to be waiting', () => {
    for (const end of [{ kind: 'done' } as const, { kind: 'eof' } as const, { kind: 'aborted' } as const]) {
      expect(play({ kind: 'waiting' }, end).waiting).toBe(false);
    }
  });

  it('says a host that never answered in full, under the message', () => {
    const r = play({ kind: 'waiting' }, {
      kind: 'error',
      code: 'host_asleep',
      error: { title: 'desk didn’t answer', detail: 'It’s probably asleep or offline.' },
    });
    expect(r.status).toBe('interrupted');
    expect(r.note).toContain('desk');
    expect(r.note).toContain('asleep or offline');
  });

  it('says a rate limit in four words, and leaves the countdown to the one banner', () => {
    const r = play({
      kind: 'error',
      code: 'rate_limited',
      error: { title: 'Too fast for this invite', detail: 'A long explanation with a countdown.', retryAfterS: 42 },
    });
    expect(r.note).toBe('Too fast — not sent.');
    expect(r.note).not.toContain('countdown');
    expect(saidInBanner('rate_limited')).toBe(true);
    expect(saidInBanner('host_asleep')).toBe(false);
    expect(saidInBanner('upstream_error')).toBe(false);
  });
});

// 014 promise 14: running out of allowance is not finishing. The transfer is complete; the answer
// is not, and the reply says which.
describe('a reply that ran out of allowance', () => {
  it('is complete as a transfer and marked as capped', () => {
    const r = play(say('As far as I got'), { kind: 'capped' }, { kind: 'done' });
    expect(r.status).toBe('complete');
    expect(r.capped).toBe(true);
  });

  it('is not confused with a reply that simply finished', () => {
    expect(play(say('all of it'), { kind: 'done' }).capped).toBeUndefined();
  });

  // The cap is about the answer, not the transfer: a capped reply is still real text, so it stays
  // in the next question's context and the reader is offered more of it rather than a retry.
  it('stays in context, because what it did say is real', () => {
    const r = play(say('half'), { kind: 'capped' }, { kind: 'done' });
    expect(r.status).toBe('complete');
    expect(r.note).toBeUndefined();
  });
});
