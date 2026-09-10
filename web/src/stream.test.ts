// The message lifecycle. Every case here is a way a reply can end that used to render as a
// finished answer (promises 1 and 2).
import { describe, expect, it } from 'vitest';
import type { StreamEvent } from './api';
import {
  carried,
  chatSpeed,
  contextCarried,
  estimateTokens,
  msText,
  NEW_REPLY,
  rateText,
  reduceReply,
  replyEnding,
  saidInBanner,
  speed,
  speedLine,
  startReply,
  thinkingFields,
  tokensSaved,
  type Reply,
} from './stream';
import type { Message, Timing } from './storage';

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

  it('the meter reads what the next question will carry, never the last exchange', () => {
    const turn = (id: string, content: string, over: Partial<import('./storage').ChatItem> = {}): Message => ({ id, role: 'user', content, ...over });
    const reply = (id: string, content: string, over: Partial<import('./storage').ChatItem> = {}): Message => ({ id, role: 'assistant', content, status: 'complete', ...over });
    const s = { systemPrompt: 'Be brief.' };
    const est = (text: string) => estimateTokens(text) + 4;
    // Counted: the system prompt and every answer. Never thinking, never a reply that was cut off or empty.
    const h1 = [turn('u1', 'hello there'), reply('a1', 'hi', { reasoning: 'x'.repeat(4000) })];
    expect(contextCarried(h1, s, 8192)).toBe(est('Be brief.') + est('hello there') + est('hi'));
    expect(contextCarried([...h1, reply('cut', 'part', { status: 'interrupted' }), reply('none', '', { status: 'no_answer' })], s, 8192)).toBe(contextCarried(h1, s, 8192));
    // Never decreasing as the chat grows.
    let last = 0;
    const grown: Message[] = [];
    for (let i = 0; i < 6; i++) {
      grown.push(turn(`u${i}`, `question ${i} `.repeat(i + 1)), reply(`a${i}`, `answer ${i} `.repeat(i + 2)));
      const now = contextCarried(grown, s, 8192);
      expect(now).toBeGreaterThanOrEqual(last);
      last = now;
    }
  });

  it('a turn that alone would not fit the memory is sent once, refused by the host, and left out from then on (024)', () => {
    const paste: Message = { id: 'big', role: 'user', content: 'w'.repeat(20_000) }; // ~5000 tokens
    const before: Message[] = [{ id: 'u1', role: 'user', content: 'short' }, { id: 'a1', role: 'assistant', content: 'ok', status: 'complete' }];
    const s = { systemPrompt: '' };
    // Its own request carries it: the host is the one that says it does not fit.
    const own = carried([...before, paste], s, 4096, paste);
    expect(own.messages.map((m) => m.content.length)).toEqual([5, 2, 20_000]);
    expect(own.leftOut).toEqual([]);
    // The next question leaves it out, and says which turn.
    const refused: Message = { id: 'r', role: 'assistant', content: '', status: 'interrupted', note: 'no longer fits' };
    const next: Message = { id: 'u2', role: 'user', content: 'What is the capital of France?' };
    const later = carried([...before, paste, refused, next], s, 4096, next);
    expect(later.messages.map((m) => m.role)).toEqual(['user', 'assistant', 'user']);
    expect(later.leftOut).toEqual([paste]);
    // The meter is what that next question carries — the paste is not in it — and it does not
    // depend on the host having answered anything yet.
    expect(contextCarried([...before, paste, refused], s, 4096)).toBe(contextCarried(before, s, 4096));
    // A host that has not said how big its memory is leaves nothing out.
    expect(carried([...before, paste, refused, next], s, 0, next).leftOut).toEqual([]);
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

// 024 promise 4: a host that died before any of the answer had arrived says so, not "what arrived is above".
describe('a host that died before the answer began', () => {
  const stalled: StreamEvent = { kind: 'error', code: 'host_stalled', error: { title: 'desk stopped answering mid-reply', detail: 'What arrived is above. Try again — if it keeps happening, their machine may have gone to sleep.', code: 'host_stalled' } };

  it('names the thinking that did arrive', () => {
    const r = reduceReply(reduceReply(NEW_REPLY, { kind: 'reasoning', text: 'hmm' }), stalled);
    expect(r.status).toBe('interrupted');
    expect(r.note).toBe('desk stopped answering mid-reply — nothing of the answer had arrived yet, only its thinking. Try again — if it keeps happening, their machine may have gone to sleep.');
  });

  it('says nothing arrived when nothing did, and keeps the ordinary line once the answer had started', () => {
    expect(reduceReply(NEW_REPLY, stalled).note).toBe('desk stopped answering mid-reply — nothing of the answer had arrived yet. Try again — if it keeps happening, their machine may have gone to sleep.');
    expect(reduceReply(reduceReply(NEW_REPLY, { kind: 'content', text: 'Part of' }), stalled).note).toBe('desk stopped answering mid-reply. What arrived is above. Try again — if it keeps happening, their machine may have gone to sleep.');
  });
});

// ---- 031: thinking on or off --------------------------------------------------------------------

describe('the thinking switch the request carries', () => {
  it('sends nothing for the model default, so the engine decides', () => {
    expect(thinkingFields('default')).toEqual({});
  });

  // One field for every engine kind: verified live on llama-server b9553 (Gemma 4) and vLLM 0.25.
  // The ticket's `reasoning_budget` is a server flag; per request it was ignored, so it is not sent.
  it('is the chat template switch, on llama.cpp and vLLM alike', () => {
    expect(thinkingFields('off')).toEqual({ chat_template_kwargs: { enable_thinking: false } });
    expect(thinkingFields('on')).toEqual({ chat_template_kwargs: { enable_thinking: true } });
    expect(thinkingFields('off')).not.toHaveProperty('reasoning_budget');
  });
});

describe('tokens saved by not thinking', () => {
  const user: Message = { id: 'u', role: 'user', content: 'capital of France?' };
  const thought: Message = { id: 'a', role: 'assistant', content: 'Paris.', reasoning: 'Thinking Process…', tokens: { in: 20, out: 160 } };
  const quiet: Message = { id: 'b', role: 'assistant', content: 'Paris.', thinking: 'off', tokens: { in: 20, out: 8 } };

  it('is the difference in completion tokens when the previous reply thought and this one was asked not to', () => {
    expect(tokensSaved([user, thought, user, quiet], 3)).toBe(152);
  });

  it('is nothing to show when either count is unknown, the previous reply did not think, or thinking was the default', () => {
    expect(tokensSaved([user, { ...thought, tokens: undefined }, user, quiet], 3)).toBeNull();
    expect(tokensSaved([user, thought, user, { ...quiet, tokens: undefined }], 3)).toBeNull();
    expect(tokensSaved([user, { ...thought, reasoning: '' }, user, quiet], 3)).toBeNull();
    expect(tokensSaved([user, thought, user, { ...quiet, thinking: undefined }], 3)).toBeNull();
    expect(tokensSaved([user, quiet], 1)).toBeNull();
  });

  it('claims no saving for a reply that thought anyway, or used more', () => {
    expect(tokensSaved([user, thought, user, { ...quiet, reasoning: 'still thinking' }], 3)).toBeNull();
    expect(tokensSaved([user, thought, user, { ...quiet, tokens: { in: 20, out: 400 } }], 3)).toBeNull();
  });
});

// ---- 032: how fast it was, from where the reader sits --------------------------------------------

describe('a reply keeps the moments its tokens arrived', () => {
  /** Plays events against a clock: each at its own millisecond after a Send at t = 1000. */
  function at(...steps: [number, StreamEvent][]): Reply {
    return steps.reduce((r, [ms, ev]) => reduceReply(r, ev, 1000 + ms), startReply(1000));
  }
  const usage = (out: number): StreamEvent => ({ kind: 'usage', in: 9, out });

  it('stamps Send, the last keepalive, the first token and the latest — and nothing on a reply without a clock', () => {
    const r = at([0, { kind: 'queued' }], [61, think('h')], [500, say('P')], [2061, say('aris')], [2100, usage(84)], [2100, { kind: 'done' }]);
    expect(r.timing).toEqual({ sent: 1000, queued: 1000, first: 1061, last: 3061 });
    expect(play(say('x'), { kind: 'done' })).not.toHaveProperty('timing');
  });

  it('measures ttft to the first token, thinking or answer, and tok/s over first-to-last token, thinking included', () => {
    const r = at([61, think('h')], [500, say('P')], [2061, say('aris')], [2100, usage(84)], [2100, { kind: 'done' }]);
    expect(speed(r)).toEqual({ ttftMs: 61, tokPerS: 42 });
    expect(speedLine(speed(r) as NonNullable<ReturnType<typeof speed>>).text).toBe('ttft 61 ms · 42 tok/s');
    expect(speedLine(speed(r) as NonNullable<ReturnType<typeof speed>>).title).toContain('24 ms per token');
  });

  // The gateway says `: queued` on joining and every 5 s, and nothing when the slot comes: the wait
  // is a floor, and the rest is not called the model's.
  it('says how long the host had it in line, as a floor, and never invents the split', () => {
    const r = at([0, { kind: 'queued' }], [5000, { kind: 'queued' }], [7100, say('Paris')], [9100, say('.')], [9100, usage(84)], [9100, { kind: 'done' }]);
    const s = speed(r) as NonNullable<ReturnType<typeof speed>>;
    expect(s).toEqual({ ttftMs: 7100, queuedMs: 5000, tokPerS: 42 });
    expect(speedLine(s).text).toBe('ttft 7.1 s (≥5.0 s of it in line for a slot) · 42 tok/s');
    expect(speedLine(s).title).toMatch(/at least 5\.0 s/);
  });

  it('times a reply that only thought: its tokens streamed like any other', () => {
    const r = at([200, think('a')], [1200, think('b')], [1200, usage(50)], [1200, { kind: 'done' }]);
    expect(r.status).toBe('no_answer');
    expect(speed(r)).toEqual({ ttftMs: 200, tokPerS: 50 });
  });

  it('keeps ttft for a stopped reply, claims no rate without a count, and says nothing before the first token', () => {
    const stopped = at([90, say('as far')], [400, say(' as')], [400, { kind: 'aborted' }]);
    expect(speed(stopped)).toEqual({ ttftMs: 90 });
    expect(speedLine(speed(stopped) as NonNullable<ReturnType<typeof speed>>).text).toBe('ttft 90 ms');
    expect(speed(at([0, { kind: 'waiting' }], [15000, { kind: 'aborted' }]))).toBeNull();
    expect(speed({ timing: { sent: 0 } })).toBeNull();
  });

  it('claims no rate from a single token', () => {
    expect(speed(at([50, say('Hi')], [50, usage(1)], [50, { kind: 'done' }]))).toEqual({ ttftMs: 50 });
  });
});

describe('the chat’s medians', () => {
  const reply = (id: string, timing: Timing | undefined, out = 100): Message => ({ id, role: 'assistant', content: 'x', timing, tokens: { in: 1, out } });

  it('is the median first-token time over replies that did not wait, and tok/s over all of them', () => {
    const msgs: Message[] = [
      { id: 'u', role: 'user', content: '?' },
      reply('a', { sent: 0, first: 100, last: 1100 }), // 100 ms · 100 tok/s
      reply('b', { sent: 0, first: 300, last: 2300 }), // 300 ms · 50 tok/s
      reply('c', { sent: 0, queued: 5000, first: 7000, last: 8000 }), // waited: out of the ttft median · 100 tok/s
      reply('d', { sent: 0 }), // never answered: not timed
      reply('e', undefined), // an older build's reply
    ];
    expect(chatSpeed(msgs)).toEqual({ ttftMs: 200, tokPerS: 100, n: 3 });
    expect(chatSpeed([reply('c', { sent: 0, queued: 5000, first: 7000, last: 8000 })])).toEqual({ tokPerS: 100, n: 1 });
    expect(chatSpeed([])).toEqual({ n: 0 });
  });

  it('formats under a second in ms, from a second on in seconds, and rates as whole numbers', () => {
    expect(msText(61)).toBe('61 ms');
    expect(msText(4260)).toBe('4.3 s');
    expect(rateText(42.4)).toBe('42 tok/s');
    expect(rateText(7.25)).toBe('7.3 tok/s');
  });
});
