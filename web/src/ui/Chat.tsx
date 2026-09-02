import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import {
  chatEvents,
  describeError,
  getMe,
  getModels,
  hostName,
  logsPrompts,
  type ChatMessage,
  type FriendlyError,
} from '../api';
import {
  ago,
  degradedLine,
  pathLine,
  waitText,
  type Live,
  type SessionEvent,
  type SessionState,
} from '../session';
import {
  DEFAULT_SETTINGS,
  hostScope,
  isAnswer,
  load,
  modelFor,
  newConversation,
  newId,
  prune,
  save,
  scopedKeys,
  titleFrom,
  type Conversation,
  type Message,
  type Settings,
} from '../storage';
import { NEW_REPLY, reduceReply, type Reply } from '../stream';
import { composing } from './composing';
import MessageView from './Message';

interface Props {
  state: SessionState;
  live: Live;
  dispatch: (e: SessionEvent) => void;
}

export default function Chat({ state, live, dispatch }: Props) {
  // Conversations and settings belong to this host and this invite, never to "the browser".
  const keys = scopedKeys(hostScope(live.addr, live.me.key.id));
  const me = live.me;

  const [listed, setListed] = useState<string[]>([]);
  const [settings, setSettings] = useState<Settings>(() => load(keys.settings, DEFAULT_SETTINGS));
  const [convs, setConvs] = useState<Conversation[]>(() => {
    const stored = prune(load<Conversation[]>(keys.conversations, []));
    return stored.length > 0 ? stored : [newConversation()];
  });
  const [currentId, setCurrentId] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [banner, setBanner] = useState<FriendlyError | null>(null);
  const [retryUntil, setRetryUntil] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const [drawer, setDrawer] = useState(false);
  const [sheet, setSheet] = useState(false);
  const abort = useRef<AbortController | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const composer = useRef<HTMLTextAreaElement>(null);

  const conv = convs.find((c) => c.id === currentId) ?? (convs[0] as Conversation);
  const models = me.host.models.length > 0 ? me.host.models : listed;
  const model = modelFor(settings.model, models) ?? models[0] ?? '';
  const waiting = Math.max(0, retryUntil - now);

  const patch = useCallback((id: string, fn: (c: Conversation) => Conversation) => {
    setConvs((prev) => prev.map((c) => (c.id === id ? fn(c) : c)));
  }, []);

  const refreshMe = useCallback(() => {
    void getMe(live.transport, live.secret)
      .then((next) => dispatch({ t: 'meOk', me: next }))
      // The machine decides: a fatal code ends the session, anything transient keeps the snapshot.
      .catch((err: unknown) => dispatch({ t: 'meError', error: describeError(err) }));
  }, [live.transport, live.secret, dispatch]);

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

  useEffect(() => {
    void getModels(live.transport, live.secret)
      .then((list) => list.length > 0 && setListed(list))
      .catch(() => {});
  }, [live.transport, live.secret]);

  // A host with nothing loaded (LM Studio) may have a model a minute from now. Ask, rather than
  // making the reader reconnect to find out.
  useEffect(() => {
    if (models.length > 0) return;
    const timer = setInterval(refreshMe, 60_000);
    return () => clearInterval(timer);
  }, [models.length, refreshMe]);

  // One clock, for the two things on screen that are about elapsed time: the retry countdown and
  // the age of a stale path measurement. Re-derived from Date.now() on every tick and whenever the
  // tab comes back, so a backgrounded tab never resumes with a countdown that kept its own time.
  useEffect(() => {
    if (retryUntil === 0 && live.pathOk) return;
    const tick = () => setNow(Date.now());
    tick();
    const timer = setInterval(tick, 1000);
    document.addEventListener('visibilitychange', tick);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', tick);
    };
  }, [retryUntil, live.pathOk]);

  // History is written when the stream is quiet, so localStorage is never in the token path.
  useEffect(() => {
    if (!streaming) save(keys.conversations, prune(convs));
  }, [convs, streaming, keys.conversations]);
  useEffect(() => save(keys.settings, settings), [settings, keys.settings]);

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
    async (convId: string, history: Message[]) => {
      if (model === '') {
        setBanner({
          title: 'No model available',
          detail: 'This host has not shared a model with your invite.',
        });
        return;
      }
      const replyId = newId();
      patch(convId, (c) => ({
        ...c,
        updatedAt: Date.now(),
        messages: [...history, { id: replyId, role: 'assistant', content: '', model }],
      }));
      setBanner(null);
      setRetryUntil(0);
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

      if (failed) {
        // The message already carries the failure as its own status; the banner is only for the
        // errors that come with something to wait for.
        dispatch({ t: 'streamError', code: failed.code, error: failed.error });
        if (failed.error.retryAfterS !== undefined) {
          setBanner(failed.error);
          setRetryUntil(Date.now() + failed.error.retryAfterS * 1000);
          setNow(Date.now());
        }
      }
      refreshMe();
    },
    [live.transport, live.secret, model, settings, patch, dispatch, refreshMe],
  );

  function send(text: string): void {
    if (streaming || text.trim() === '') return;
    const message: Message = { id: newId(), role: 'user', content: text.trim() };
    const history = [...conv.messages, message];
    patch(conv.id, (c) => ({
      ...c,
      title: c.messages.length === 0 ? titleFrom(message.content) : c.title,
      messages: history,
    }));
    pinned.current = true;
    void run(conv.id, history);
  }

  function regenerate(): void {
    if (streaming) return;
    const idx = lastIndexOfRole(conv.messages, 'user');
    if (idx < 0) return;
    const history = conv.messages.slice(0, idx + 1);
    patch(conv.id, (c) => ({ ...c, messages: history }));
    void run(conv.id, history);
  }

  function resend(text: string): void {
    if (streaming) return;
    const idx = lastIndexOfRole(conv.messages, 'user');
    if (idx < 0) return;
    const history: Message[] = [
      ...conv.messages.slice(0, idx),
      { ...(conv.messages[idx] as Message), content: text.trim() },
    ];
    patch(conv.id, (c) => ({ ...c, messages: history }));
    void run(conv.id, history);
  }

  function startNew(): void {
    const next = newConversation();
    setConvs((prev) => [next, ...prune(prev)]);
    setCurrentId(next.id);
    setDrawer(false);
    setBanner(null);
    composer.current?.focus();
  }

  const lastUser = lastIndexOfRole(conv.messages, 'user');
  const degraded = state.name === 'degraded' ? degradedLine(state.reason, me) : null;

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
              <button
                className="conv-del"
                aria-label={`Delete ${c.title}`}
                onClick={() => {
                  const rest = convs.filter((x) => x.id !== c.id);
                  setConvs(rest.length > 0 ? rest : [newConversation()]);
                  if (c.id === conv.id) setCurrentId('');
                }}
              >
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

      <main className="main">
        <header className="topbar">
          <button className="hamburger" aria-label="Conversations" onClick={() => setDrawer(true)}>
            ☰
          </button>
          <div className="who">
            <strong>{me.host.name}</strong>
            <span className="dim">{model}</span>
          </div>
          <div className="truth">
            <span className="path">{pathLine(live, now)}</span>
            <Meters me={me} />
          </div>
          <button className="ghost tiny" onClick={() => setSheet(true)}>
            Settings
          </button>
        </header>

        {degraded && (
          <p className="degraded" role="status">
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
                hostName={hostName(me)}
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
                  live={streaming && i === conv.messages.length - 1}
                  busy={streaming}
                  last={i >= lastUser && i >= conv.messages.length - 2}
                  onRegenerate={regenerate}
                  onResend={resend}
                />
              ))
            )}
          </div>
        </div>

        {banner && (
          <div className="banner" role="alert">
            <div>
              <strong>{banner.title}</strong> <span className="dim">{banner.detail}</span>
              {banner.hostSaid && <span className="dim"> The host said: “{banner.hostSaid}”</span>}
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
          streaming={streaming}
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
    </div>
  );
}

/** Two thin bars, each with the number it represents; a bar alone would say nothing. */
function Meters({ me }: { me: Live['me'] }) {
  const { rpm, daily_tokens: daily } = me.limits;
  const { rpm_used: used, today_tokens: today } = me.usage;
  return (
    <span className="meters">
      <Meter label={rpm ? `${used}/${rpm} per minute` : `${used} per minute`} value={rpm ? used / rpm : 0} />
      <Meter
        label={daily ? `${compact(today)}/${compact(daily)} tokens today` : `${compact(today)} tokens today`}
        value={daily ? today / daily : 0}
      />
    </span>
  );
}

function Meter({ label, value }: { label: string; value: number }) {
  return (
    <span className="meter" title={label}>
      <span className="meter-label">{label}</span>
      <span className="meter-track">
        <span className="meter-fill" style={{ width: `${Math.min(100, Math.round(value * 100))}%` }} />
      </span>
    </span>
  );
}

function compact(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10_000 ? 0 : 1)}k`;
  return String(n);
}

function Empty({
  hostName,
  model,
  logging,
  engineDown,
  onPick,
}: {
  hostName: string;
  model: string;
  logging: boolean;
  engineDown: boolean;
  onPick: (t: string) => void;
}) {
  const prompts = [
    'Explain what just happened when I pasted that code.',
    'Write a haiku about borrowing a stranger’s GPU.',
    'What can you help me with?',
  ];
  return (
    <div className="empty">
      {/* A gateway need not have a name for itself; "You’re on ." is not a sentence. */}
      {hostName !== '' && <h2>You’re on {hostName}.</h2>}
      <p className="dim">
        {engineDown
          ? `${model || 'The model'} is not answering right now.`
          : model
            ? `${model} is listening.`
            : 'Waiting for a model.'}{' '}
        {logging
          ? 'This host has prompt logging on: it can read everything you send.'
          : 'Ask it anything — the host sees counts, not conversations.'}
      </p>
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
  streaming,
  onSend,
  onStop,
}: {
  ref: React.RefObject<HTMLTextAreaElement | null>;
  streaming: boolean;
  onSend: (t: string) => void;
  onStop: () => void;
}) {
  const [text, setText] = useState('');
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
            setText(e.target.value);
            resize(e.target);
          }}
          onKeyDown={(e) => {
            // Enter mid-composition commits an IME candidate and must not send (promise 9).
            if (e.key === 'Enter' && !e.shiftKey && !composing(e)) {
              e.preventDefault();
              if (!streaming && text.trim() !== '') {
                onSend(text);
                setText('');
                resize(e.currentTarget);
              }
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
              setText('');
              resize(ref.current);
              ref.current?.focus();
            }}
          >
            Send
          </button>
        )}
      </div>
      <p className="hint">Enter sends · Shift+Enter makes a new line</p>
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
  return (
    <div className="sheet-wrap" onClick={onClose}>
      <div className="sheet" onClick={(e) => e.stopPropagation()}>
        <h3>Settings</h3>
        <label className="field">
          <span className="field-label">Model</span>
          <select
            value={settings.model ?? models[0] ?? ''}
            onChange={(e) => onChange({ ...settings, model: e.target.value })}
          >
            {models.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span className="field-label">System prompt</span>
          <textarea
            rows={4}
            value={settings.systemPrompt}
            placeholder="Optional. Sent ahead of every message in this browser."
            onChange={(e) => onChange({ ...settings, systemPrompt: e.target.value })}
          />
        </label>
        <label className="field">
          <span className="field-label">Temperature · {settings.temperature.toFixed(2)}</span>
          <input
            type="range"
            min={0}
            max={2}
            step={0.05}
            value={settings.temperature}
            onChange={(e) => onChange({ ...settings, temperature: Number(e.target.value) })}
          />
        </label>
        <p className="dim small-print">
          Your invite is <code>{me.key.name}</code> ({me.key.id}) on {hostName(me) || 'this host'}. Limits:{' '}
          {me.limits.rpm}/min, {compact(me.limits.daily_tokens)} tokens a day, {me.limits.max_concurrent} at
          a time. Engine: {me.host.upstream.kind}
          {me.host.upstream.model_context > 0 ? `, ${compact(me.host.upstream.model_context)} context` : ''}
          {me.host.upstream.healthy ? '' : ' — not answering right now'}.{' '}
          {logsPrompts(me)
            ? 'This host logs prompts and replies.'
            : 'This host sees counts, not what you write.'}
          {live.ephemeral
            ? ' Another tab of this browser holds the saved tunnel identity, so this tab connected as a second client.'
            : ''}
          {live.pathOk ? '' : ` The path last measured ${ago(Date.now() - live.pathAt)}.`}
        </p>
        <button className="primary small" onClick={onClose}>
          Done
        </button>
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

function lastIndexOfRole(messages: Message[], role: Message['role']): number {
  for (let i = messages.length - 1; i >= 0; i--) if (messages[i]?.role === role) return i;
  return -1;
}
