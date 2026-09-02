// The message lifecycle. Every case here is a way a reply can end that used to render as a
// finished answer (promises 1 and 2).
import { describe, expect, it } from 'vitest';
import type { StreamEvent } from './api';
import { contextUsed, NEW_REPLY, reduceReply, replyEnding, saidInBanner, type Reply } from './stream';

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
      queued: false,
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

  // 018: the second silence. In line is not absent, and not an ending either.
  it('marks itself as queued, and unmarks itself on the first token or any ending', () => {
    expect(play({ kind: 'queued' })).toMatchObject({ queued: true, waiting: false });
    expect(play({ kind: 'queued' }).status).toBeUndefined();
    expect(play({ kind: 'queued' }, say('hi')).queued).toBe(false);
    expect(play({ kind: 'queued' }, think('hm')).queued).toBe(false);
    for (const end of [{ kind: 'done' } as const, { kind: 'eof' } as const, { kind: 'aborted' } as const]) {
      expect(play({ kind: 'queued' }, end).queued).toBe(false);
    }
  });

  it('lets the newer of queued and waiting say what the silence is', () => {
    expect(play({ kind: 'waiting' }, { kind: 'queued' })).toMatchObject({ queued: true, waiting: false });
    expect(play({ kind: 'queued' }, { kind: 'waiting' })).toMatchObject({ queued: false, waiting: true });
  });

  it('says a busy host in five words, and leaves the countdown to the one banner', () => {
    const r = play({ kind: 'queued' }, {
      kind: 'error',
      code: 'queue_timeout',
      error: { title: 'desk is busy', detail: 'Every slot was taken — your message is still here.', retryAfterS: 5 },
    });
    expect(r).toMatchObject({ status: 'interrupted', queued: false, note: 'Not sent — every slot was taken.' });
    expect(saidInBanner('queue_timeout')).toBe(true);
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

// 020 promise 2 and 6: "You stopped this reply" is the reader's sentence and nobody else's.
describe('an abort the app asked for', () => {
  it('is a cut with its reason, never a stop', () => {
    const r = play(say('as far as'), { kind: 'aborted', why: 'Another tab took over this chat — what is above is only part of it.' });
    expect(r.status).toBe('interrupted');
    expect(r.note).toContain('Another tab took over');
    expect(play(say('x'), { kind: 'aborted' })).toMatchObject({ status: 'stopped', note: 'You stopped this reply.' });
  });

  it('says a dead or paused invite in five words and leaves the rest to the header', () => {
    for (const [code, word] of [['key_paused', 'paused'], ['key_revoked', 'revoked'], ['invalid_key', 'no longer works']]) {
      const r = play({ kind: 'error', code: code as string, error: { title: 'long', detail: 'longer' } });
      expect(r.status).toBe('interrupted');
      expect(r.note).toMatch(/^Not sent — /);
      expect(r.note).toContain(word as string);
    }
  });
});

// 020 promise 3: which wall a reply hit is one function over four numbers.
describe('which wall a reply hit', () => {
  const cap = 2048;
  const ctx = 4096; // slack for 4096 is 64

  it('is nothing unless the engine stopped for length', () => {
    expect(replyEnding(undefined, { in: 100, out: 2048 }, cap, ctx)).toBeNull();
    expect(replyEnding(false, { in: 3000, out: 1096 }, cap, ctx)).toBeNull();
  });

  it('is the reply cap only when the output reached it', () => {
    expect(replyEnding(true, { in: 100, out: 2048 }, cap, ctx)).toBe('capped');
    expect(replyEnding(true, { in: 100, out: 2100 }, cap, ctx)).toBe('capped');
    // Both walls at once: the cap is what stopped it, and Continue still helps.
    expect(replyEnding(true, { in: 2048, out: 2048 }, cap, ctx)).toBe('capped');
  });

  // The two friends' own arithmetic: 2971 + 1125 and 3047 + 1049, under a line claiming a 2048 cap.
  it('is the context wall when the sum filled the model, and the output did not reach the cap', () => {
    expect(replyEnding(true, { in: 2971, out: 1125 }, cap, ctx)).toBe('context');
    expect(replyEnding(true, { in: 3047, out: 1049 }, cap, ctx)).toBe('context');
    expect(replyEnding(true, { in: 3000, out: 1032 }, cap, ctx)).toBe('context'); // 4032 = ctx − slack
    expect(replyEnding(true, { in: 3000, out: 1031 }, cap, ctx)).toBe('length'); // one under the slack
  });

  it('is a plain length stop when it cannot tell, and never a wall it cannot see', () => {
    expect(replyEnding(true, undefined, cap, ctx)).toBe('length');
    expect(replyEnding(true, { in: 3000, out: 1096 }, cap, 0)).toBe('length'); // no context size known
    expect(replyEnding(true, { in: 100, out: 500 }, 0, ctx)).toBe('length'); // no cap known, nowhere near the wall
  });

  it('reads the context the last reply reported, for the meter', () => {
    expect(contextUsed([])).toBeNull();
    expect(contextUsed([{ role: 'user' }, { role: 'assistant' }])).toBeNull();
    expect(contextUsed([{ role: 'user' }, { role: 'assistant', tokens: { in: 10, out: 5 } }, { role: 'user' }])).toBe(15);
    expect(contextUsed([{ role: 'assistant', tokens: { in: 10, out: 5 } }, { role: 'assistant', tokens: { in: 20, out: 5 } }])).toBe(25);
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
