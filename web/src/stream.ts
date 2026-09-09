import { sentText, sentContent } from './attachments';
import type { ImageData } from './images';
import { tr } from './i18n/text';
// The message lifecycle: one pure reducer from the stream events in api.ts to what the bubble
// says. The rule it exists to enforce is that a reply is finished only when the host says it is —
// running out of tokens is not an ending, and must never render as one (docs/PRINCIPLES.md, "Surfaces
// tell the truth").
import type { ChatMessage, ChatRequest, StreamEvent } from './api';
import { isAnswer, type Message, type MessageStatus, type Settings, type Thinking, type Timing } from './storage';

/** The parts of a Message this machine owns. `status` is undefined while it is still streaming. */
export type Reply = Pick<
  Message,
  'content' | 'reasoning' | 'tokens' | 'status' | 'note' | 'waiting' | 'queued' | 'details' | 'capped' | 'timing'
>;

export const NEW_REPLY: Reply = { content: '', reasoning: '' };

/** A reply about to be asked for: its clock starts now (032). */
export function startReply(now = Date.now()): Reply {
  return { ...NEW_REPLY, timing: { sent: now } };
}

/**
 * `now` is when the event arrived, by the device's clock. It only ever lands on a reply that was
 * started with one (startReply); a reply without a clock is never given one half way.
 */
export function reduceReply(r: Reply, e: StreamEvent, now = Date.now()): Reply {
  const timed = (next: Reply, fn: (t: Timing) => Timing): Reply => (next.timing ? { ...next, timing: fn(next.timing) } : next);
  const token = (t: Timing): Timing => ({ ...t, first: t.first ?? now, last: now });
  switch (e.kind) {
    // Not an ending: a reply that has produced nothing for long enough that saying nothing would
    // itself be a lie. It clears on the first token and on every terminal event.
    // The newer of the two says what the silence is: a keepalive means the host has the request
    // and it is in line; a notice that outlives the keepalives means the line is over and the
    // model is at work — neither is an ending, and neither is a host that is not there (018).
    case 'waiting':
      return r.status === undefined ? { ...r, waiting: true, queued: false } : r;
    case 'queued':
      return r.status === undefined ? timed({ ...r, queued: true, waiting: false }, (t) => ({ ...t, queued: now })) : r;
    case 'capped':
      return { ...r, capped: true };
    case 'reasoning':
      return timed({ ...r, reasoning: (r.reasoning ?? '') + e.text, waiting: false, queued: false }, token);
    case 'content':
      return timed({ ...r, content: r.content + e.text, waiting: false, queued: false }, token);
    case 'usage':
      return { ...r, tokens: { in: e.in, out: e.out } };
    case 'done':
      return finish(r, 'complete');
    case 'eof':
      return finish(
        r,
        'interrupted',
        tr('app_the_connection_dropped_before_the_host_finished_this_reply'),
      );
    // "You stopped this reply" is reserved for the reader's own Stop (020 promise 2): an abort the
    // app asked for carries its reason and is a cut, not a choice.
    case 'aborted':
      return e.why ? finish(r, 'interrupted', e.why) : finish(r, 'stopped', tr('app_you_stopped_this_reply'));
    case 'error':
      // Said once (014 promise 10). A wait the reader has to sit out is explained by the banner,
      // with its countdown, so the message only says it did not go; everything else is explained
      // where it happened. Either way the host's own sentence is evidence, not primary copy: it
      // goes to `details`, which the surface hides behind a disclosure (014 promise 1).
      // A host that died before any of the answer had arrived must not say "what arrived is
      // above" (024 promise 4): it says what did arrive — its thinking, or nothing.
      return finish(
        r,
        'interrupted',
        e.code === 'concurrency_limited' && (e.error.limit ?? 0) > 1
          ? e.error.title
          : saidInBanner(e.code)
          ? SHORT[e.code]
          : e.code === 'host_stalled' && r.content.trim() === ''
            ? tr((r.reasoning ?? '').trim() !== '' ? 'app_stalled_after_thinking' : 'app_stalled_before_answer', { title: e.error.title })
            : tr('app_error_title_detail', { title: e.error.title, detail: e.error.detail }),
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
  get rate_limited() { return tr('app_too_fast_not_sent'); },
  get concurrency_limited() { return tr('app_not_sent_one_reply_at_a_time'); },
  get queue_timeout() { return tr('app_not_sent_every_slot_was_taken'); },
  get budget_exhausted() { return tr('app_not_sent_today_s_tokens_are_used_up'); },
  get key_paused() { return tr('app_not_sent_your_invite_is_paused'); },
  get key_revoked() { return tr('app_not_sent_this_invite_was_revoked'); },
  get invalid_key() { return tr('app_not_sent_this_invite_no_longer_works'); },
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
        ? tr('app_the_model_used_its_whole_reply_thinking_and_never')
        : tr('app_the_host_finished_without_sending_an_answer'),
    };
  }
  if (empty && status === 'stopped') {
    return {
      ...base,
      status,
      note: thought ? tr('app_you_stopped_this_while_it_was_still_thinking') : tr('app_you_stopped_this_before_it_began'),
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

// ---- what the next request carries (024) --------------------------------------------------------

/**
 * A token, estimated: four characters of text, plus a few per message for the chat template. The
 * gateway counts for real (a 13k-character essay came to 3339); this is what the client can know
 * before it sends, and it is the same estimate for the meter and for the rule below, so they agree.
 */
export function estimateTokens(text: string): number {
  return Math.ceil(text.length / 4);
}
const PER_MESSAGE = 4;

/**
 * The messages the next request will carry, and the turns it leaves out. One function, two readers
 * — the request and the header's context meter — so the meter can never argue with what is sent.
 * Not carried: the system prompt when empty; a reply that was cut off or had no answer (not context:
 * sending it back asks the model to continue what the host never finished); the model's thinking
 * (never resent); and, when the host has said how big the model's memory is, any single turn that
 * alone would not fit it (024) — except `asking`, the turn being sent now: the host refuses that
 * one itself, honestly, and from then on it is left out, so a 5000-word paste is never resent
 * under a nine-word question, and the reply to that question says so.
 */
export function carried(
  history: readonly Message[],
  settings: Pick<Settings, 'systemPrompt'>,
  modelContext: number,
  asking?: Message,
  images: ImageData = {},
): { messages: ChatMessage[]; leftOut: Message[] } {
  const messages: ChatMessage[] = [];
  const leftOut: Message[] = [];
  const system = settings.systemPrompt.trim();
  if (system !== '') messages.push({ role: 'system', content: system });
  for (const m of history) {
    if (!isAnswer(m)) continue;
    const text = sentText(m);
    if (m.role === 'assistant' && text.trim() === '') continue;
    if (m !== asking && modelContext > 0 && estimateTokens(text) + PER_MESSAGE >= modelContext) {
      leftOut.push(m);
      continue;
    }
    messages.push({ role: m.role, content: m.role === 'user' ? sentContent(m, images, text) : text });
  }
  return { messages, leftOut };
}

/** What the next question will carry, as the context meter's number: the chat so far as it will be sent under it. */
export function contextCarried(
  history: readonly Message[],
  settings: Pick<Settings, 'systemPrompt'>,
  modelContext: number,
): number {
  return carried(history, settings, modelContext).messages.reduce((n, m) => n + estimateTokens(typeof m.content === 'string' ? m.content : m.content.filter((p) => p.type === 'text').map((p) => p.text).join('')) + PER_MESSAGE, 0);
}

function contextSlack(modelContext: number): number {
  return Math.max(8, Math.floor(modelContext / 64));
}

// ---- thinking on or off (031) ------------------------------------------------------------------

/**
 * The engine's own switch for thinking, sent only when the reader chose. One field for every engine
 * kind: verified live on llama-server b9553 (Gemma 4: `enable_thinking=false` → no reasoning, 35 ms
 * of generation instead of 800) and on vLLM 0.25 (accepted; that host's model never thinks). The
 * ticket's `reasoning_budget` is a llama-server *flag*, not a request field — sent per request it is
 * ignored (measured), so it is not sent.
 */
export function thinkingFields(thinking: Thinking): Pick<ChatRequest, 'chat_template_kwargs'> {
  return thinking === 'default' ? {} : { chat_template_kwargs: { enable_thinking: thinking === 'on' } };
}

/**
 * How many fewer completion tokens a reply asked not to think used than the reply before it, which
 * did think — when both counts are known (031 promise 2). Null otherwise: a saving nobody measured
 * is not shown, and a reply that thought anyway saved nothing.
 */
export function tokensSaved(messages: readonly Message[], i: number): number | null {
  const m = messages[i];
  if (!m || m.thinking !== 'off' || m.reasoning || !m.tokens) return null;
  for (let j = i - 1; j >= 0; j--) {
    const p = messages[j] as Message;
    if (p.role !== 'assistant') continue;
    return p.tokens && p.reasoning && p.tokens.out > m.tokens.out ? p.tokens.out - m.tokens.out : null;
  }
  return null;
}

// ---- how fast it was, from where the reader sits (032) -------------------------------------------

/**
 * What this device measured about one reply. `ttftMs` is Send to the first token, thinking or
 * answer. `queuedMs` is how long the host had said "in line for a slot" by its last keepalive — a
 * floor, because the gateway says `: queued` every 5 s and nothing at the moment the slot comes, so
 * the split between the wait and the model is not something this device can see and is never
 * invented. `tokPerS` is completion tokens over first-to-last token, thinking included because it
 * streamed. The relay hop and this app are inside every number; the host's usage log times the same
 * reply from its side.
 */
export interface Speed {
  ttftMs: number;
  queuedMs?: number;
  tokPerS?: number;
}

export function speed(m: Pick<Message, 'timing' | 'tokens'>): Speed | null {
  const t = m.timing;
  if (!t || t.first === undefined) return null;
  const s: Speed = { ttftMs: t.first - t.sent };
  if (t.queued !== undefined) s.queuedMs = t.queued - t.sent;
  if (m.tokens && m.tokens.out > 0 && t.last !== undefined && t.last > t.first) s.tokPerS = (m.tokens.out * 1000) / (t.last - t.first);
  return s;
}

/** The footer's words for a reply's speed, and the fuller sentence behind them. */
export function speedLine(s: Speed): { text: string; title: string } {
  const wait = s.queuedMs === undefined ? '' : tr('app_queued_time', { duration: msText(s.queuedMs) });
  const rate = s.tokPerS === undefined ? '' : ` · ${rateText(s.tokPerS)}`;
  return {
    text: `ttft ${msText(s.ttftMs)}${wait}${rate}`,
    title:
      tr('app_measured_on_this_device_time_to_first_token_from') +
      (s.queuedMs === undefined ? '' : tr('app_queue_timing_explanation', { duration: msText(s.queuedMs) })) +
      (s.tokPerS === undefined ? '' : tr('app_token_speed_explanation', { rate: rateText(s.tokPerS), duration: msText(1000 / s.tokPerS) })),
  };
}

/**
 * The chat's typical numbers: medians over its timed replies. A reply that waited for a slot is
 * left out of the first-token median — the wait was the host's, not the model's — and counts for
 * tokens per second, which it measures the same as any other.
 */
export function chatSpeed(messages: readonly Message[]): { ttftMs?: number; tokPerS?: number; n: number } {
  const all = messages.filter((m) => m.role === 'assistant').map(speed).filter((s): s is Speed => s !== null);
  return {
    ttftMs: median(all.filter((s) => s.queuedMs === undefined).map((s) => s.ttftMs)),
    tokPerS: median(all.map((s) => s.tokPerS).filter((v): v is number => v !== undefined)),
    n: all.length,
  };
}

function median(xs: number[]): number | undefined {
  if (xs.length === 0) return undefined;
  const s = [...xs].sort((a, b) => a - b);
  const mid = s.length >> 1;
  return s.length % 2 === 1 ? (s[mid] as number) : ((s[mid - 1] as number) + (s[mid] as number)) / 2;
}

/** "61 ms" under a second, "4.3 s" from there. */
export function msText(ms: number): string {
  return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(1)} s`;
}

/** "42 tok/s"; a decimal only when there is little else. */
export function rateText(tokPerS: number): string {
  return `${tokPerS >= 10 ? Math.round(tokPerS) : tokPerS.toFixed(1)} tok/s`;
}

/** Count exactly the parts emitted by carried(), after omission and missing-blob filtering. */
export function carriedImageCount(messages: readonly ChatMessage[]): number {
  return messages.reduce((n, m) => n + (Array.isArray(m.content) ? m.content.filter((p) => p.type === 'image_url').length : 0), 0);
}
