import { turnAttachments, type DisplayAttachment } from '../attachments';
import { AttachedImages, ImageShots } from './Images';
import { imageCopy, type ImageData } from '../images';
import { tr } from '../i18n/text';
import { useEffect, useRef, useState } from 'react';
import { modelLabel } from '../api';
import { compact } from '../session';
import { isAnswer, type Message } from '../storage';
import { replyEnding, speed, speedLine } from '../stream';
import type { ThreadAction } from './Chat';
import Markdown from './Markdown';

interface Props {
  message: Message;
  /** The host's display name: waiting and failure copy name the machine, never "the endpoint". */
  host: string;
  /** True while this message is the one being streamed by this tab. */
  live: boolean;
  /** True while any reply is streaming in this tab: actions that would start a second one stay hidden. */
  busy: boolean;
  /** (User turns) the reply right after this turn is still arriving — in this tab or another. */
  answering: boolean;
  /** (User turns) no request carrying this turn has succeeded yet (022 promise 2). Derived by Chat, never stored. */
  undelivered: boolean;
  /** (Failed replies) a later question carried this row's turn anyway (024 promise 3): "Not sent" is no longer the last word. */
  carried: boolean;
  /** A follower tab (020 promise 6): nothing here may start a request or edit the thread. */
  readOnly: boolean;
  /** Actions only appear on the last exchange, the way ChatGPT does it. */
  last: boolean;
  /** The one action this exchange offers, named for what it will do (Regenerate / Try again /
   *  Reconnect), or null when nothing can be sent. Composed by Chat, the only place that knows. */
  action: ThreadAction | null;
  /** The two walls a reply can hit (020 promise 3): the invite's reply cap and the model's context. */
  limits: { maxOutputTokens: number; modelContext: number };
  /** Completion tokens this reply used fewer than the previous one by not thinking (031), or null. */
  saved: number | null;
  onContinue: () => void;
  onNewChat: () => void;
  imageData: ImageData;
  imagesLoaded: boolean;
  onResend: (text: string, attachments: DisplayAttachment[]) => void;
}

export default function MessageView({
  message: m,
  host,
  live,
  busy,
  answering,
  undelivered,
  carried,
  readOnly,
  last,
  action,
  limits,
  saved,
  onContinue,
  onNewChat,
  onResend,
  imageData,
  imagesLoaded,
}: Props) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(m.content);
  const [editAttachments, setEditAttachments] = useState(() => turnAttachments(m));

  if (m.role === 'user') {
    if (editing) {
      return (
        <div className="row user">
          <div className="bubble editing">
            <AttachedImages attachments={editAttachments} data={imageData} onRemove={(id) => setEditAttachments((v) => v.filter((i) => i.id !== id))} />
            <textarea value={draft} rows={Math.min(10, draft.split('\n').length + 1)} autoFocus onChange={(e) => setDraft(e.target.value)} />
            <div className="edit-actions">
              <button className="ghost" onClick={() => { setEditing(false); setDraft(m.content); }}>
                {tr('app_cancel')}
              </button>
              {/* It replaces the answer below it, so it says so before it is pressed (promise 16). */}
              <button className="primary small" onClick={() => { setEditing(false); onResend(draft, editAttachments); }} disabled={draft.trim() === '' && !editAttachments.length}>
                {tr('app_replace_answer')}
              </button>
            </div>
          </div>
        </div>
      );
    }
    // The mark is derived from delivery (022 promise 2): it clears the moment any request that
    // carried this turn succeeds, and it hides while this turn's own reply is on the way.
    const pending = undelivered && !answering;
    return (
      <div className="row user">
        <div className={`bubble ${pending ? 'pending' : ''}`}>{(m.images?.length || m.files?.length) ? <ImageShots attachments={turnAttachments(m)} data={imageData} loaded={imagesLoaded} host={host} /> : null}{m.content}</div>
        <div className="actions">
          {/* The pending turn (014 promise 1): the reader's words are still here and still theirs. */}
          {pending && <span className="pending-mark">{tr('app_not_delivered')}</span>}
          {last && !busy && !readOnly && (
            <button className="ghost tiny" onClick={() => { setDraft(m.content); setEditAttachments(turnAttachments(m)); setEditing(true); }}>
              {tr('app_edit')}
            </button>
          )}
        </div>
      </div>
    );
  }

  const ended = m.status !== undefined && m.status !== 'complete';
  // When the reply is all thinking and no answer, the explanation belongs inside the collapsed
  // Thinking block — there is nothing else for it to sit under. Otherwise it goes under the text.
  // Only a reply the model chose to spend on thinking explains itself inside the Thinking block; a
  // reply the host cut off before any answer says so under the row (024 promise 4).
  const inThinking = m.status === 'no_answer' && Boolean(m.reasoning);
  // Which wall a complete-but-cut reply hit is one function over four numbers (020 promise 3), so
  // this line and the context meter in the header can never disagree.
  const ending = !live && m.status === 'complete' ? replyEnding(m.capped, m.tokens, limits.maxOutputTokens, limits.modelContext) : null;
  // How fast it was, from this device (032): said the moment the first token lands, and no sooner.
  const measured = speed(m);
  const pace = measured ? speedLine(measured) : null;
  // The streaming cursor (promise 2, the mock's `.cursor`): shown only while this tab is writing
  // this reply and there is a line for it to sit on — before the first token the row already says
  // what it is waiting for, and a cursor next to that sentence would be a second claim.
  const streaming = live && m.content !== '';
  return (
    <div className={`row assistant ${streaming ? 'streaming' : ''}`}>
      {m.previous !== undefined && m.previous !== '' && (
        <details className="previous">
          <summary>{tr('app_previous_answer')}</summary>
          <Markdown text={m.previous} />
        </details>
      )}
      {m.reasoning && (
        <Thinking
          text={m.reasoning}
          answering={m.content !== '' || ended || m.status === 'complete'}
          note={inThinking ? m.note : undefined}
        />
      )}
      <Markdown text={live ? closeFences(m.content) : m.content} />
      {/* Three silences, three sentences (018): in line behind other people's requests, a host
          that has gone quiet, and the ordinary pause before the first token. */}
      {live && m.content === '' && !m.reasoning && (
        <p className="waiting">
          {m.queued === true
            ? tr('app_waiting_slot', { host: host || tr('app_the_host_lowercase') })
            : m.waiting === true
              ? tr('app_still_waiting_host', { host: host || tr('app_the_host_lowercase') })
              : tr('app_waiting_for_the_first_token')}
        </p>
      )}
      {/* A reply another tab is writing (020 promise 6): read as it is checkpointed, never as ours. */}
      {!live && m.status === undefined && <p className="waiting">{tr('app_arriving_in_another_tab')}</p>}
      {/* The reply did not simply stop, or something was left out of its question (024): said
          under the text it kept. */}
      {m.note && !inThinking && (
        carried ? (
          <p className="ended carried">{tr('app_your_message_was_carried_into_the_next_question')}</p>
        ) : (
          <p className={`ended ${m.status ?? ''}`}>{m.note}</p>
        )
      )}
      {/* Running out of allowance is not finishing (014 promise 14): the reader is told where it
          stopped and offered the only thing that helps — more of the same reply, or, when the
          model's memory is what filled, a new chat (020 promise 3). */}
      {ending === 'capped' && (
        <p className="ended capped">
          {tr('app_reply_limit', { limit: limits.maxOutputTokens })}{' '}
          {!readOnly && <button className="ghost tiny" onClick={onContinue}>{tr('app_continue')}</button>}
        </p>
      )}
      {/* The one wall that ends chats, said in the register it deserves (022 promise 3). What is
          true: the reply stopped here; the thread goes back to the host without the model's
          thinking, so the next message may still fit, but each reply has less room until one no
          longer fits and the host says so in its own row. Nothing is dropped behind the reader's
          back, and nothing is disabled. */}
      {ending === 'context' && (
        <p className="ended wall">
          <span>
            {tr('app_context_filled', { context: compact(limits.modelContext), host: host || tr('app_the_host_lowercase') })}
          </span>
          {!readOnly && <button className="ghost tiny" onClick={onNewChat}>{tr('app_new_chat')}</button>}
        </p>
      )}
      {ending === 'length' && (
        <p className="ended capped">
          {tr('app_this_stopped_at_a_length_limit_on_the_host')}{' '}
          {!readOnly && <button className="ghost tiny" onClick={onContinue}>{tr('app_continue')}</button>}
        </p>
      )}
      {/* The host's raw sentence is evidence, never what a stranger has to read first. */}
      {ended && m.details !== undefined && m.details !== '' && <Details text={m.details} />}
      <div className="meta">
        <span className="meta-text">
          {m.model ? modelLabel(m.model) : ''}
          {m.tokens ? (m.imageCount ? imageCopy('app_message_usage_images', m.imageCount, { input: m.tokens.in.toLocaleString('en-US'), output: m.tokens.out.toLocaleString('en-US') }) : tr('app_message_usage', { input: m.tokens.in, output: m.tokens.out })) : ''}
          {/* A stopped reply never gets its usage chunk, but the host counted what it made. */}
          {!m.tokens && m.status === 'stopped' ? tr('app_still_counted_against_today_s_tokens') : ''}
          {/* What was asked of the engine, and what it did (031): an engine that thought anyway is
              not called quiet. */}
          {m.thinking === 'off' ? (m.reasoning ? tr('app_thought_despite_thinking_off') : tr('app_thinking_off')) : ''}
          {saved !== null ? tr('app_tokens_saved', { count: saved }) : ''}
          {pace && <span className="speed" title={pace.title}>{` · ${pace.text}`}</span>}
          {/* Said only of text that is on screen: a reply with nothing in it is not "part" of anything,
              and under a turn that was never answered it read as a claim about the turn (022 promise 2). */}
          {!isAnswer(m) && m.content.trim() !== '' ? tr('app_not_part_of_the_next_question') : ''}
        </span>
        <span className="actions">
          {!live && m.content !== '' && <CopyButton text={m.content} />}
          {last && !busy && action && (
            <button className="ghost tiny" onClick={action.run} disabled={action.disabled === true}>
              {action.label}
            </button>
          )}
        </span>
      </div>
    </div>
  );
}

/** A raw transport string helps exactly one reader in a hundred; it waits until it is asked for. */
function Details({ text }: { text: string }) {
  return (
    <details className="host-said">
      <summary>{tr('app_details')}</summary>
      <p>{text}</p>
    </details>
  );
}

function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      className="ghost tiny"
      onClick={() => {
        void navigator.clipboard?.writeText(text);
        setDone(true);
        setTimeout(() => setDone(false), 2000);
      }}
    >
      {done ? tr('app_copied') : tr('app_copy')}
    </button>
  );
}

/**
 * Collapses itself the moment the answer starts, unless the reader opened or closed it by hand.
 * While the model is still thinking the block is a window that follows the newest line; once the
 * answer has started it is a record, and opening it shows all of it (020 promise 7).
 */
function Thinking({ text, answering, note }: { text: string; answering: boolean; note?: string }) {
  const [manual, setManual] = useState<boolean | null>(null);
  const open = manual ?? !answering;
  const body = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (open && !answering && body.current) body.current.scrollTop = body.current.scrollHeight;
  }, [open, answering, text]);

  // The body stays mounted and animates its height, so collapsing is a glide, not a jump.
  return (
    <div className={`thinking ${open ? 'open' : ''} ${answering ? 'full' : ''}`}>
      <button className="thinking-toggle" aria-expanded={open} onClick={() => setManual(!open)}>
        {tr('app_thinking')}<span className="thinking-hint">{open ? tr('app_hide') : tr('app_thinking_words', { count: words(text) })}</span>
      </button>
      <div className="thinking-clip">
        {/* Thinking is model output like any other: it arrives as markdown and reads as raw
            asterisks if it is not rendered as markdown (014 promise 17). */}
        <div className="thinking-body" ref={body}>
          <Markdown text={text} />
        </div>
      </div>
      {/* A reply that was all thinking and no answer explains itself here, not by being blank. */}
      {note && <p className="ended">{note}</p>}
    </div>
  );
}

function words(s: string): number {
  return s.trim() === '' ? 0 : s.trim().split(/\s+/).length;
}

/**
 * While tokens are still arriving a fenced code block is often unterminated, which makes the
 * markdown renderer flip the same text from paragraph to <pre> the moment the closing fence lands.
 * Closing it ourselves keeps the block a block from its first line: no reflow mid-stream.
 */
function closeFences(text: string): string {
  const fences = text.match(/^```/gm)?.length ?? 0;
  return fences % 2 === 1 ? `${text}\n\`\`\`` : text;
}
