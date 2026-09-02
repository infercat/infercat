import { useEffect, useRef, useState } from 'react';
import type { Message } from '../storage';
import type { ThreadAction } from './Chat';
import Markdown from './Markdown';

interface Props {
  message: Message;
  /** The host's display name: waiting and failure copy name the machine, never "the endpoint". */
  host: string;
  /** True while this message is the one being streamed. */
  live: boolean;
  /** True while any reply is streaming: actions that would start a second one stay hidden. */
  busy: boolean;
  /** Actions only appear on the last exchange, the way ChatGPT does it. */
  last: boolean;
  /** The one action this exchange offers, named for what it will do (Regenerate / Try again /
   *  Reconnect). Composed by Chat, which is the only place that knows which of those is true. */
  action: ThreadAction;
  /** The invite's per-reply token cap, for the ending a capped reply gets (014 promise 14). */
  capTokens: number;
  onContinue: () => void;
  onResend: (text: string) => void;
}

export default function MessageView({
  message: m,
  host,
  live,
  busy,
  last,
  action,
  capTokens,
  onContinue,
  onResend,
}: Props) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(m.content);

  if (m.role === 'user') {
    if (editing) {
      return (
        <div className="row user">
          <div className="bubble editing">
            <textarea value={draft} rows={Math.min(10, draft.split('\n').length + 1)} autoFocus onChange={(e) => setDraft(e.target.value)} />
            <div className="edit-actions">
              <button className="ghost" onClick={() => { setEditing(false); setDraft(m.content); }}>
                Cancel
              </button>
              {/* It replaces the answer below it, so it says so before it is pressed (promise 16). */}
              <button className="primary small" onClick={() => { setEditing(false); onResend(draft); }} disabled={draft.trim() === ''}>
                Replace answer
              </button>
            </div>
          </div>
        </div>
      );
    }
    const pending = m.pending === true && !busy;
    return (
      <div className="row user">
        <div className={`bubble ${pending ? 'pending' : ''}`}>{m.content}</div>
        <div className="actions">
          {/* The pending turn (014 promise 1): the reader's words are still here and still theirs. */}
          {pending && <span className="pending-mark">Not delivered</span>}
          {last && !busy && (
            <button className="ghost tiny" onClick={() => { setDraft(m.content); setEditing(true); }}>
              Edit
            </button>
          )}
        </div>
      </div>
    );
  }

  const ended = m.status !== undefined && m.status !== 'complete';
  // When the reply is all thinking and no answer, the explanation belongs inside the collapsed
  // Thinking block — there is nothing else for it to sit under. Otherwise it goes under the text.
  const inThinking = ended && m.content.trim() === '' && Boolean(m.reasoning);
  return (
    <div className="row assistant">
      {m.previous !== undefined && m.previous !== '' && (
        <details className="previous">
          <summary>Previous answer</summary>
          <Markdown text={m.previous} />
        </details>
      )}
      {m.reasoning && (
        <Thinking
          text={m.reasoning}
          answering={m.content !== '' || ended}
          note={inThinking ? m.note : undefined}
        />
      )}
      <Markdown text={live ? closeFences(m.content) : m.content} />
      {/* Three silences, three sentences (018): in line behind other people's requests, a host
          that has gone quiet, and the ordinary pause before the first token. */}
      {live && m.content === '' && !m.reasoning && (
        <p className="waiting">
          {m.queued === true
            ? `Waiting for a free slot on ${host || 'the host'}…`
            : m.waiting === true
              ? `Still waiting for ${host || 'the host'}…`
              : 'Waiting for the first token…'}
        </p>
      )}
      {/* The reply did not simply stop: it says which way it stopped, under the text it kept. */}
      {ended && !inThinking && <p className={`ended ${m.status}`}>{m.note}</p>}
      {/* Running out of allowance is not finishing (014 promise 14): the reader is told where it
          stopped and offered the only thing that helps — more of the same reply. */}
      {!live && m.capped === true && m.status === 'complete' && (
        <p className="ended capped">
          This stopped at your invite’s {capTokens}-token reply limit.{' '}
          <button className="ghost tiny" onClick={onContinue}>
            Continue
          </button>
        </p>
      )}
      {/* The host's raw sentence is evidence, never what a stranger has to read first. */}
      {ended && m.details !== undefined && m.details !== '' && <Details text={m.details} />}
      <div className="meta">
        <span className="meta-text">
          {m.model ?? ''}
          {m.tokens ? ` · ${m.tokens.in} tokens in · ${m.tokens.out} out` : ''}
          {m.status === 'interrupted' || m.status === 'no_answer'
            ? ' · not part of the next question'
            : ''}
        </span>
        <span className="actions">
          {last && !busy && m.content !== '' && <CopyButton text={m.content} />}
          {last && !busy && (
            <button className="ghost tiny" onClick={action.run}>
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
      <summary>Details</summary>
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
        setTimeout(() => setDone(false), 1400);
      }}
    >
      {done ? 'Copied' : 'Copy'}
    </button>
  );
}

/** Collapses itself the moment the answer starts, unless the reader opened or closed it by hand. */
function Thinking({ text, answering, note }: { text: string; answering: boolean; note?: string }) {
  const [manual, setManual] = useState<boolean | null>(null);
  const open = manual ?? !answering;
  const body = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (open && body.current) body.current.scrollTop = body.current.scrollHeight;
  }, [open, text]);

  // The body stays mounted and animates its height, so collapsing is a glide, not a jump.
  return (
    <div className={`thinking ${open ? 'open' : ''}`}>
      <button className="thinking-toggle" aria-expanded={open} onClick={() => setManual(!open)}>
        Thinking
        <span className="thinking-hint">{open ? 'hide' : `${words(text)} words`}</span>
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
