import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import {
  chatEvents,
  describeError,
  getMe,
  getModels,
  hostName,
  logsPrompts,
  ME_TIMEOUT_MS,
  modelLabel,
  needsRedial,
  type ChatMessage,
  type FriendlyError,
} from '../api';
import { privacyLine, VERSION } from '../product';
import {
  ago,
  compact,
  degradedLine,
  meters,
  metersUnknown,
  pathLine,
  waitText,
  type Live,
  type SessionEvent,
  type SessionState,
} from '../session';
import {
  chatsChanged,
  deleteChat,
  DEFAULT_SETTINGS,
  hostScope,
  isAnswer,
  load,
  loadChats,
  mergeChats,
  modelFor,
  newConversation,
  newId,
  save,
  saveChat,
  scopedKeys,
  titleFrom,
  type Conversation,
  type Message,
  type Settings,
} from '../storage';
import { NEW_REPLY, reduceReply, saidInBanner, type Reply } from '../stream';
import { composing } from './composing';
import MessageView from './Message';
import { coarsePointer } from './pointer';

interface Props {
  state: SessionState;
  live: Live;
  dispatch: (e: SessionEvent) => void;
  /** Throw this session away and dial the same host again (014 promise 13). */
  onRedial: (l: Live) => void;
}

/** What the last exchange offers the reader, when the reply did not simply work. */
export interface ThreadAction {
  label: string;
  run: () => void;
}

/** How long "Chat deleted · Undo" stays up before the deletion is really a deletion. */
const UNDO_MS = 6000;
/** A streaming reply is written to storage at most this often (DESIGN §2.3 persistence rules). */
const CHECKPOINT_MS = 2000;

export default function Chat({ state, live, dispatch, onRedial }: Props) {
  // Conversations and settings belong to this host and this invite, never to "the browser".
  const scope = hostScope(live.addr, live.me.key.id);
  const keys = scopedKeys(scope);
  const me = live.me;
  const host = hostName(me);
  const touch = coarsePointer();

  const [listed, setListed] = useState<string[]>([]);
  const [settings, setSettings] = useState<Settings>(() => load(keys.settings, DEFAULT_SETTINGS));
  const [convs, setConvs] = useState<Conversation[]>(() => {
    const stored = loadChats(scope);
    return stored.length > 0 ? stored : [newConversation()];
  });
  const [currentId, setCurrentId] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [draft, setDraft] = useState('');
  const [banner, setBanner] = useState<FriendlyError | null>(null);
  const [retryUntil, setRetryUntil] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const [drawer, setDrawer] = useState(false);
  const [sheet, setSheet] = useState(false);
  const [limitsSheet, setLimitsSheet] = useState(false);
  const [undo, setUndo] = useState<Conversation | null>(null);
  // The connection, not the request, is what failed: offering another 30 s wait would be a lie.
  const [broken, setBroken] = useState(false);
  const abort = useRef<AbortController | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const composer = useRef<HTMLTextAreaElement>(null);
  const savedAt = useRef(0);

  const conv = convs.find((c) => c.id === currentId) ?? (convs[0] as Conversation);
  const models = me.host.models.length > 0 ? me.host.models : listed;
  const model = modelFor(settings.model, models) ?? models[0] ?? '';
  const waiting = Math.max(0, retryUntil - now);
  const paused = live.paused;

  const patch = useCallback((id: string, fn: (c: Conversation) => Conversation) => {
    setConvs((prev) => prev.map((c) => (c.id === id ? fn(c) : c)));
  }, []);

  const persist = useCallback(
    (c: Conversation) => {
      savedAt.current = Date.now();
      saveChat(scope, c);
    },
    [scope],
  );

  const refreshMe = useCallback(() => {
    void getMe(live.transport, live.secret, timeoutSignal(ME_TIMEOUT_MS))
      .then((next) => dispatch({ t: 'meOk', me: next }))
      // The machine decides: a fatal code ends the session, a pause degrades it, anything else
      // keeps the snapshot but stops presenting it as current.
      .catch((err: unknown) => dispatch({ t: 'meError', error: describeError(err, host) }));
  }, [live.transport, live.secret, dispatch, host]);

  // The path is measured, not assumed: every 30 s, and a failure says so rather than leaving the
  // last number on screen as if it were current.
  useEffect(() => {
    const tick = () => {
      void live.transport
        .ping()
        .then((p) => dispatch(p ? { t: 'pingOk', path: p, at: Date.now() } : { t: 'pingFail' }))
        .catch(() => dispatch({ t: 'pingFail' }));
    };
    const timer = setInterval(tick, 30_000);
    return () => clearInterval(timer);
  }, [live.transport, dispatch]);

  // /v1/models is a fallback, not a routine (014 promise 5): /me already carries the models this
  // invite may use, so asking again spends one of the friend's own requests for an answer we
  // already have. Only a host that reported none — LM Studio with nothing loaded — is worth asking.
  useEffect(() => {
    if (me.host.models.length > 0) return;
    void getModels(live.transport, live.secret)
      .then((list) => list.length > 0 && setListed(list))
      .catch(() => {});
  }, [live.transport, live.secret, me.host.models.length]);

  // A host with nothing loaded (LM Studio) may have a model a minute from now; a paused invite may
  // be resumed a minute from now; a failed /me may succeed a minute from now. Ask, rather than
  // making the reader reconnect to find out.
  useEffect(() => {
    if (models.length > 0 && !paused && live.meOk) return;
    const timer = setInterval(refreshMe, 60_000);
    return () => clearInterval(timer);
  }, [models.length, paused, live.meOk, refreshMe]);

  // One clock, for the things on screen that are about elapsed time: the retry countdown and the
  // age of a stale path measurement. Re-derived from Date.now() on every tick and whenever the tab
  // comes back, so a backgrounded tab never resumes with a countdown that kept its own time.
  useEffect(() => {
    if (retryUntil === 0 && live.pathOk && live.meOk) return;
    const tick = () => setNow(Date.now());
    tick();
    const timer = setInterval(tick, 1000);
    document.addEventListener('visibilitychange', tick);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', tick);
    };
  }, [retryUntil, live.pathOk, live.meOk]);

  // Persistence is rules, not a streaming guard (DESIGN §2.3): the user turn is written by send()
  // before any I/O, a streaming reply is checkpointed at most every 2 s, and every terminal
  // transition — which is what `streaming` going false is — is written at once.
  useEffect(() => {
    if (conv.messages.length === 0) return;
    if (!streaming) {
      persist(conv);
      return;
    }
    const timer = setTimeout(
      () => persist(conv),
      Math.max(0, CHECKPOINT_MS - (Date.now() - savedAt.current)),
    );
    return () => clearTimeout(timer);
  }, [conv, streaming, persist]);

  useEffect(() => save(keys.settings, settings), [settings, keys.settings]);

  // Another tab of this browser wrote to this host's history. Re-read before writing again: with
  // one key per conversation the only thing that can be lost is a turn we never saw, and this is
  // where we see it (014 promise 4).
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (!chatsChanged(scope, e.key)) return;
      setConvs((prev) => mergeChats(scope, prev));
    };
    globalThis.addEventListener('storage', onStorage);
    return () => globalThis.removeEventListener('storage', onStorage);
  }, [scope]);

  // A model this host does not share is not a choice; drop it rather than showing a picker that
  // disagrees with what is sent.
  useEffect(() => {
    if (settings.model !== null && models.length > 0 && !models.includes(settings.model)) {
      setSettings((s) => ({ ...s, model: null }));
    }
  }, [settings.model, models]);

  // Follow the stream only while the reader is already at the bottom; never yank them back.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [conv.messages]);

  const run = useCallback(
    async (convId: string, history: Message[], previous?: string) => {
      if (model === '') {
        setBanner({
          title: 'No model available',
          detail: 'This host has not shared a model with your invite.',
        });
        return;
      }
      const replyId = newId();
      // The user's turn is pending from the moment it is sent: that mark is what keeps their words
      // in the thread, with something to press, if nothing ever comes back (014 promise 1).
      patch(convId, (c) => ({
        ...c,
        updatedAt: Date.now(),
        messages: [
          ...history.map((m) => (m.role === 'user' ? { ...m, pending: true } : m)),
          { id: replyId, role: 'assistant', content: '', model, ...(previous ? { previous } : {}) },
        ],
      }));
      setBanner(null);
      setRetryUntil(0);
      setBroken(false);
      const ac = new AbortController();
      abort.current = ac;
      setStreaming(true);

      let reply: Reply = NEW_REPLY;
      let failed: { code: string; error: FriendlyError } | null = null;
      try {
        for await (const ev of chatEvents(
          live.transport,
          live.secret,
          { model, messages: toChatMessages(history, settings), temperature: settings.temperature },
          ac.signal,
          undefined,
          host,
        )) {
          if (ev.kind === 'error') failed = { code: ev.code, error: ev.error };
          reply = reduceReply(reply, ev);
          patch(convId, (c) => ({
            ...c,
            messages: c.messages.map((m) => (m.id === replyId ? { ...m, ...reply } : m)),
          }));
        }
      } finally {
        setStreaming(false);
        abort.current = null;
      }

      // The turn was delivered when the host produced something. Anything else leaves it pending,
      // which is what the thread and the Try again render from.
      const delivered = reply.status === 'complete' || reply.status === 'stopped';
      const lastUserText = [...history].reverse().find((m) => m.role === 'user')?.content ?? '';
      if (failed?.error.paused) {
        // Paused is recoverable (014 promise 2): the words go back to the composer where the reader
        // can send them again, and the turn that never happened does not clutter the thread.
        patch(convId, (c) => ({
          ...c,
          messages: c.messages.filter((m) => m.id !== replyId).slice(0, -1),
        }));
        setDraft(lastUserText);
        composer.current?.focus();
      } else if (delivered) {
        patch(convId, (c) => ({
          ...c,
          messages: c.messages.map((m) => (m.pending ? { ...m, pending: false } : m)),
        }));
      }

      if (failed) {
        // The message already carries the failure as its own status; the banner is only for the
        // waits the reader has to sit out, so nothing is said in two places (014 promise 10).
        setBroken(needsRedial(failed.code));
        dispatch({ t: 'streamError', code: failed.code, error: failed.error });
        if (saidInBanner(failed.code) && failed.error.retryAfterS !== undefined) {
          setBanner(failed.error);
          setRetryUntil(Date.now() + failed.error.retryAfterS * 1000);
          setNow(Date.now());
        }
      }
      refreshMe();
    },
    [live.transport, live.secret, model, settings, patch, dispatch, refreshMe, host],
  );

  function send(text: string): void {
    if (streaming || text.trim() === '') return;
    const message: Message = { id: newId(), role: 'user', content: text.trim() };
    const history = [...conv.messages, message];
    const next: Conversation = {
      ...conv,
      title: conv.messages.length === 0 ? titleFrom(message.content) : conv.title,
      updatedAt: Date.now(),
      messages: history,
    };
    setConvs((prev) => prev.map((c) => (c.id === next.id ? next : c)));
    persist(next); // before any I/O: a reload from here still has the reader's words (013)
    setDraft('');
    pinned.current = true;
    void run(conv.id, history);
  }

  /**
   * Both ways of asking again drop the answer that is on screen. It is not thrown away: the reply
   * that replaces it carries it as `previous`, behind a disclosure, so a reader who preferred the
   * old one has not lost it to a click (014 promise 16).
   */
  function replaceAnswer(text?: string): void {
    if (streaming) return;
    const idx = lastIndexOfRole(conv.messages, 'user');
    if (idx < 0) return;
    const replaced = conv.messages.slice(idx + 1).find((m) => m.content.trim() !== '')?.content;
    const asked = text === undefined ? (conv.messages[idx] as Message) : { ...(conv.messages[idx] as Message), content: text.trim() };
    const history: Message[] = [...conv.messages.slice(0, idx), { ...asked, pending: false }];
    patch(conv.id, (c) => ({
      ...c,
      // An edited first message is what this chat is now about; a title from the old one is stale.
      title: idx === 0 ? titleFrom(asked.content) : c.title,
      messages: history,
    }));
    void run(conv.id, history, replaced);
  }

  const regenerate = () => replaceAnswer();
  const resend = (text: string) => replaceAnswer(text);

  function startNew(): void {
    const next = newConversation();
    setConvs((prev) => [next, ...prev.filter((c) => c.messages.length > 0)]);
    setCurrentId(next.id);
    setDrawer(false);
    setBanner(null);
    composer.current?.focus();
  }

  // Deleting is undoable rather than confirmed (014 promise 8): a dialog asks a question the reader
  // has already answered; six seconds of Undo answers the one they might actually have.
  function remove(c: Conversation): void {
    setConvs((prev) => {
      const rest = prev.filter((x) => x.id !== c.id);
      return rest.length > 0 ? rest : [newConversation()];
    });
    deleteChat(scope, c.id);
    if (c.id === conv.id) setCurrentId('');
    setUndo(c);
  }

  useEffect(() => {
    if (!undo) return;
    const timer = setTimeout(() => setUndo(null), UNDO_MS);
    return () => clearTimeout(timer);
  }, [undo]);

  const lastUser = lastIndexOfRole(conv.messages, 'user');
  const pendingTurn = conv.messages.some((m) => m.pending === true);
  // One action on the last exchange, named for what it will actually do.
  const action: ThreadAction = broken
    ? { label: 'Reconnect', run: () => onRedial(live) }
    : pendingTurn
      ? { label: 'Try again', run: regenerate }
      : { label: 'Regenerate', run: regenerate };
  const degraded = state.name === 'degraded' ? degradedLine(state.reason, me) : null;
  const unknown = metersUnknown(live);

  return (
    <div className={`app ${drawer ? 'drawer-open' : ''}`}>
      <aside className="sidebar">
        <div className="sidebar-head">
          <button className="secondary wide" onClick={startNew}>
            New chat
          </button>
        </div>
        <nav className="conv-list">
          {convs.map((c) => (
            <div key={c.id} className={`conv ${c.id === conv.id ? 'current' : ''}`}>
              <button
                className="conv-open"
                onClick={() => {
                  setCurrentId(c.id);
                  setDrawer(false);
                }}
              >
                {c.title}
              </button>
              <button className="conv-del" aria-label={`Delete ${c.title}`} onClick={() => remove(c)}>
                ×
              </button>
            </div>
          ))}
        </nav>
        <div className="sidebar-foot">
          <button className="ghost tiny" onClick={() => dispatch({ t: 'abort', error: null })}>
            Disconnect
          </button>
        </div>
      </aside>

      <div className="backdrop" onClick={() => setDrawer(false)} />

      <main className={`main ${unknown ? 'stale' : ''}`}>
        <header className="topbar">
          <button className="hamburger" aria-label="Conversations" onClick={() => setDrawer(true)}>
            ☰
          </button>
          <div className="who">
            <strong>{host || 'This host'}</strong>
            <span className="dim">{modelLabel(model)}</span>
          </div>
          <div className="truth">
            <span className="path">{pathLine(live, now)}</span>
            <Meters live={live} onOpen={() => setLimitsSheet(true)} />
          </div>
          <button className="ghost tiny" onClick={() => setSheet(true)}>
            Settings
          </button>
        </header>

        {degraded && (
          <p className={`degraded ${state.name === 'degraded' ? state.reason : ''}`} role="status">
            {degraded}
          </p>
        )}
        {logsPrompts(me) && (
          <p className="logging" role="status">
            This host records prompts and replies to a log on its machine.
          </p>
        )}

        <div
          className="scroll"
          ref={scroller}
          onScroll={(e) => {
            const el = e.currentTarget;
            pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 120;
          }}
        >
          <div className="thread">
            {conv.messages.length === 0 ? (
              <Empty
                host={host}
                model={model}
                logging={logsPrompts(me)}
                engineDown={!me.host.upstream.healthy}
                onPick={send}
              />
            ) : (
              conv.messages.map((m, i) => (
                <MessageView
                  key={m.id}
                  message={m}
                  host={host}
                  live={streaming && i === conv.messages.length - 1}
                  busy={streaming}
                  last={i >= lastUser && i >= conv.messages.length - 2}
                  action={action}
                  capTokens={me.limits.max_output_tokens}
                  onContinue={() => send('Continue from where you stopped.')}
                  onResend={resend}
                />
              ))
            )}
          </div>
        </div>

        {undo && (
          <div className="toast" role="status">
            <span>Chat deleted</span>
            <button
              className="ghost tiny"
              onClick={() => {
                const back = undo;
                setConvs((prev) => [back, ...prev.filter((c) => c.messages.length > 0)]);
                saveChat(scope, back);
                setCurrentId(back.id);
                setUndo(null);
              }}
            >
              Undo
            </button>
          </div>
        )}

        {banner && (
          <div className="banner" role="alert">
            <div>
              <strong>{banner.title}</strong> <span className="dim">{banner.detail}</span>
            </div>
            <div className="banner-actions">
              {banner.retryAfterS !== undefined && (
                <button className="ghost tiny" disabled={waiting > 0} onClick={regenerate}>
                  {waiting > 0 ? `Try again in ${waitText(waiting)}` : 'Try again'}
                </button>
              )}
              <button
                className="ghost tiny"
                onClick={() => {
                  setBanner(null);
                  setRetryUntil(0);
                }}
              >
                Dismiss
              </button>
            </div>
          </div>
        )}

        <Composer
          ref={composer}
          text={draft}
          onText={setDraft}
          streaming={streaming}
          touch={touch}
          paused={paused}
          onSend={send}
          onStop={() => abort.current?.abort()}
        />
      </main>

      {sheet && (
        <SettingsSheet
          settings={settings}
          models={models}
          live={live}
          onChange={setSettings}
          onClose={() => setSheet(false)}
        />
      )}
      {limitsSheet && <LimitsSheet me={me} onClose={() => setLimitsSheet(false)} />}
    </div>
  );
}

function Meters({ live, onOpen }: { live: Live; onOpen: () => void }) {
  return (
    <button className="meters" onClick={onOpen} aria-label="What these limits mean">
      {meters(live).map((m) => (
        <span className="meter" key={m.label} title={m.label}>
          <span className="meter-label">{m.label}</span>
          <span className={`meter-track ${m.unknown ? 'unknown' : ''}`}>
            {!m.unknown && (
              <span className="meter-fill" style={{ width: `${Math.min(100, Math.round(m.value * 100))}%` }} />
            )}
          </span>
        </span>
      ))}
    </button>
  );
}

/** One sheet, one sentence each: the whole explanation of the two numbers in the header. */
function LimitsSheet({ me, onClose }: { me: Live['me']; onClose: () => void }) {
  const { rpm, daily_tokens: daily } = me.limits;
  return (
    <div className="sheet-wrap" onClick={onClose}>
      <div className="sheet" onClick={(e) => e.stopPropagation()}>
        <h3>Your limits</h3>
        <p>
          <strong>About {rpm} messages a minute.</strong> Your host caps how fast one invite can
          send, so a burst from you never stalls their machine for everyone else.
        </p>
        <p>
          <strong>{compact(daily)} tokens a day.</strong> A token is roughly three quarters of a
          word, counting both what you write and what the model answers. The count resets daily.
        </p>
        <button className="primary small" onClick={onClose}>
          Got it
        </button>
      </div>
    </div>
  );
}

function Empty({
  host,
  model,
  logging,
  engineDown,
  onPick,
}: {
  host: string;
  model: string;
  logging: boolean;
  engineDown: boolean;
  onPick: (t: string) => void;
}) {
  const prompts = [
    'How can you answer me if you are running on someone else’s computer?',
    'Write a haiku about borrowing a stranger’s GPU.',
    'What can you help me with?',
  ];
  return (
    <div className="empty">
      {/* A gateway need not have a name for itself; "You’re on ." is not a sentence. */}
      {host !== '' && <h2>You’re on {host}.</h2>}
      <p className="dim">
        {engineDown
          ? `${modelLabel(model) || 'The model'} is not answering right now.`
          : model !== ''
            ? `${modelLabel(model)} is listening.`
            : 'Waiting for a model.'}
      </p>
      <p className="dim">{privacyLine(host, logging)}</p>
      <div className="suggestions">
        {prompts.map((p) => (
          <button key={p} className="suggestion" onClick={() => onPick(p)}>
            {p}
          </button>
        ))}
      </div>
    </div>
  );
}

function Composer({
  ref,
  text,
  onText,
  streaming,
  touch,
  paused,
  onSend,
  onStop,
}: {
  ref: React.RefObject<HTMLTextAreaElement | null>;
  text: string;
  onText: (t: string) => void;
  streaming: boolean;
  touch: boolean;
  paused: boolean;
  onSend: (t: string) => void;
  onStop: () => void;
}) {
  const resize = (el: HTMLTextAreaElement | null) => {
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(200, el.scrollHeight)}px`;
  };
  return (
    <div className="composer">
      <div className="composer-box">
        <textarea
          ref={ref}
          value={text}
          rows={1}
          placeholder="Message the host’s model…"
          onChange={(e) => {
            onText(e.target.value);
            resize(e.target);
          }}
          onKeyDown={(e) => {
            // On a touch keyboard Return is the only way to make a new line, so Send is the only
            // way to send (014 promise 6). Enter mid-composition commits an IME candidate and must
            // not send either (007 promise 9).
            if (touch || e.key !== 'Enter' || e.shiftKey || composing(e)) return;
            e.preventDefault();
            if (!streaming && text.trim() !== '') {
              onSend(text);
              resize(e.currentTarget);
            }
          }}
        />
        {streaming ? (
          <button className="primary small" onClick={onStop}>
            Stop
          </button>
        ) : (
          <button
            className="primary small"
            disabled={text.trim() === ''}
            onClick={() => {
              onSend(text);
              resize(ref.current);
              ref.current?.focus();
            }}
          >
            Send
          </button>
        )}
      </div>
      {paused ? (
        <p className="hint">Send will work again the moment your host resumes your invite.</p>
      ) : touch ? null : (
        <p className="hint">Enter sends · Shift+Enter makes a new line</p>
      )}
    </div>
  );
}

function SettingsSheet({
  settings,
  models,
  live,
  onChange,
  onClose,
}: {
  settings: Settings;
  models: string[];
  live: Live;
  onChange: (s: Settings) => void;
  onClose: () => void;
}) {
  const me = live.me;
  // Cancel has to mean cancel, so the sheet edits a copy and only Done commits it.
  const [draft, setDraft] = useState<Settings>(settings);
  const chosen = draft.model ?? models[0] ?? '';
  return (
    <div className="sheet-wrap" onClick={onClose}>
      <div className="sheet" onClick={(e) => e.stopPropagation()}>
        <h3>Settings</h3>
        <label className="field">
          <span className="field-label">Model</span>
          <select value={chosen} onChange={(e) => setDraft({ ...draft, model: e.target.value })}>
            {models.map((m) => (
              <option key={m} value={m}>
                {modelLabel(m)}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span className="field-label">System prompt</span>
          <textarea
            rows={4}
            value={draft.systemPrompt}
            placeholder="Optional. Sent ahead of every message in this browser."
            onChange={(e) => setDraft({ ...draft, systemPrompt: e.target.value })}
          />
        </label>
        <label className="field">
          <span className="field-label">Temperature · {draft.temperature.toFixed(2)}</span>
          <input
            type="range"
            min={0}
            max={2}
            step={0.05}
            value={draft.temperature}
            onChange={(e) => setDraft({ ...draft, temperature: Number(e.target.value) })}
          />
          <span className="field-hint">Lower is more predictable, higher is more surprising.</span>
        </label>
        <p className="dim small-print">
          {privacyLine(hostName(me), logsPrompts(me))} Your invite is <code>{me.key.name}</code> (
          {me.key.id}). Limits: about {me.limits.rpm} messages a minute,{' '}
          {compact(me.limits.daily_tokens)} tokens a day, {me.limits.max_concurrent} at a time, and
          up to {compact(me.limits.max_output_tokens)} tokens in any one reply.
          Engine: {me.host.upstream.kind}
          {me.host.upstream.model_context > 0 ? `, ${compact(me.host.upstream.model_context)} context` : ''}
          {me.host.upstream.healthy ? '' : ' — not answering right now'}. Model id:{' '}
          <code>{chosen || 'none'}</code>.
          {live.ephemeral
            ? ' Another tab of this browser holds the saved tunnel identity, so this tab connected as a second client.'
            : ''}
          {live.pathOk ? '' : ` The path last measured ${ago(Date.now() - live.pathAt)}.`}
        </p>
        {/* The app's half of a bug report; the host's half is `bunny-network version`. */}
        <p className="dim small-print">App version {VERSION}</p>
        <div className="sheet-actions">
          <button className="ghost" onClick={onClose}>
            Cancel
          </button>
          <button
            className="primary small"
            onClick={() => {
              onChange(draft);
              onClose();
            }}
          >
            Done
          </button>
        </div>
      </div>
    </div>
  );
}

function toChatMessages(history: Message[], settings: Settings): ChatMessage[] {
  const out: ChatMessage[] = [];
  if (settings.systemPrompt.trim() !== '') {
    out.push({ role: 'system', content: settings.systemPrompt.trim() });
  }
  for (const m of history) {
    // A turn that was cut off or never answered is not context: sending it back asks the model to
    // continue something the host never finished saying.
    if (!isAnswer(m)) continue;
    if (m.role === 'assistant' && m.content.trim() === '') continue;
    out.push({ role: m.role, content: m.content });
  }
  return out;
}

/** AbortSignal.timeout, where it exists; a hand-rolled one where it does not. */
function timeoutSignal(ms: number): AbortSignal {
  const T = AbortSignal as typeof AbortSignal & { timeout?: (ms: number) => AbortSignal };
  if (typeof T.timeout === 'function') return T.timeout(ms);
  const ac = new AbortController();
  setTimeout(() => ac.abort(), ms);
  return ac.signal;
}

function lastIndexOfRole(messages: Message[], role: Message['role']): number {
  for (let i = messages.length - 1; i >= 0; i--) if (messages[i]?.role === role) return i;
  return -1;
}
