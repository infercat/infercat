import { useEffect, useRef, useState } from 'react';
import type { Message } from '../storage';
import Markdown from './Markdown';

interface Props {
  message: Message;
  /** True while this message is the one being streamed. */
  live: boolean;
  /** True while any reply is streaming: actions that would start a second one stay hidden. */
  busy: boolean;
  /** Actions only appear on the last exchange, the way ChatGPT does it. */
  last: boolean;
  onRegenerate: () => void;
  onResend: (text: string) => void;
}

export default function MessageView({ message: m, live, busy, last, onRegenerate, onResend }: Props) {
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
              <button className="primary small" onClick={() => { setEditing(false); onResend(draft); }} disabled={draft.trim() === ''}>
                Send again
              </button>
            </div>
          </div>
        </div>
      );
    }
    return (
      <div className="row user">
        <div className="bubble">{m.content}</div>
        <div className="actions">
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
      {m.reasoning && (
        <Thinking
          text={m.reasoning}
          answering={m.content !== '' || ended}
          note={inThinking ? m.note : undefined}
        />
      )}
      <Markdown text={live ? closeFences(m.content) : m.content} />
      {live && m.content === '' && !m.reasoning && <p className="waiting">Waiting for the first token…</p>}
      {/* The reply did not simply stop: it says which way it stopped, under the text it kept. */}
      {ended && !inThinking && <p className={`ended ${m.status}`}>{m.note}</p>}
      <div className="meta">
        <span className="meta-text">
          {m.model ?? ''}
          {m.tokens ? ` · ${m.tokens.in} in / ${m.tokens.out} out` : ''}
          {m.status === 'interrupted' || m.status === 'no_answer' ? ' · not sent as context' : ''}
        </span>
        <span className="actions">
          {last && !busy && m.content !== '' && <CopyButton text={m.content} />}
          {last && !busy && (
            <button className="ghost tiny" onClick={onRegenerate}>
              Regenerate
            </button>
          )}
        </span>
      </div>
    </div>
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
        <div className="thinking-body" ref={body}>
          {text}
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
