// The message lifecycle. Every case here is a way a reply can end that used to render as a
// finished answer (promises 1 and 2).
import { describe, expect, it } from 'vitest';
import type { StreamEvent } from './api';
import { NEW_REPLY, reduceReply, type Reply } from './stream';

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

  // Promise 11: our copy and the host's diagnostic, both — never one replacing the other.
  it('shows the host diagnostic next to our copy, not instead of it', () => {
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
    expect(r.note).toContain('The engine dropped the request after 6 s.');
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
