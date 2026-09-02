import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { Live } from '../App';
import {
  describeError,
  getMe,
  getModels,
  streamChat,
  type ChatMessage,
  type FriendlyError,
} from '../api';
import {
  DEFAULT_SETTINGS,
  KEYS,
  load,
  newConversation,
  newId,
  prune,
  save,
  titleFrom,
  type Conversation,
  type Message,
  type Settings,
} from '../storage';
import { describePath } from '../transport';
import MessageView from './Message';

interface Props {
  live: Live;
  onDisconnect: (reason?: string) => void;
}

export default function Chat({ live, onDisconnect }: Props) {
  const [me, setMe] = useState(live.me);
  const [path, setPath] = useState(live.path);
  const [models, setModels] = useState<string[]>(live.me.host.models);
  const [settings, setSettings] = useState<Settings>(() => load(KEYS.settings, DEFAULT_SETTINGS));
  const [convs, setConvs] = useState<Conversation[]>(() => {
    const stored = prune(load<Conversation[]>(KEYS.conversations, []));
    return stored.length > 0 ? stored : [newConversation()];
  });
  const [currentId, setCurrentId] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState<FriendlyError | null>(null);
  const [retryIn, setRetryIn] = useState(0);
  const [drawer, setDrawer] = useState(false);
  const [sheet, setSheet] = useState(false);
  const abort = useRef<AbortController | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const composer = useRef<HTMLTextAreaElement>(null);

  const conv = convs.find((c) => c.id === currentId) ?? (convs[0] as Conversation);
  const model = settings.model ?? models[0] ?? '';

  const patch = useCallback((id: string, fn: (c: Conversation) => Conversation) => {
    setConvs((prev) => prev.map((c) => (c.id === id ? fn(c) : c)));
  }, []);

  // The path is measured, not assumed: every 30 s, and shown as text.
  useEffect(() => {
    let alive = true;
    const tick = () => {
      void live.transport
        .ping()
        .then((p) => alive && setPath(p))
        .catch(() => {});
    };
    const timer = setInterval(tick, 30_000);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [live.transport]);

  useEffect(() => {
    void getModels(live.transport, live.secret)
      .then((list) => list.length > 0 && setModels(list))
      .catch(() => {});
  }, [live.transport, live.secret]);

  // History is written when the stream is quiet, so localStorage is never in the token path.
  useEffect(() => {
    if (!streaming) save(KEYS.conversations, prune(convs));
  }, [convs, streaming]);
  useEffect(() => save(KEYS.settings, settings), [settings]);

  useEffect(() => {
    if (!error?.retryAfterS) {
      setRetryIn(0);
      return;
    }
    setRetryIn(error.retryAfterS);
    const timer = setInterval(() => {
      setRetryIn((v) => {
        if (v <= 1) clearInterval(timer);
        return Math.max(0, v - 1);
      });
    }, 1000);
    return () => clearInterval(timer);
  }, [error]);

  // Follow the stream only while the reader is already at the bottom; never yank them back.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [conv.messages]);

  const refreshMe = useCallback(() => {
    void getMe(live.transport, live.secret)
      .then(setMe)
      .catch(() => {});
  }, [live.transport, live.secret]);

  const run = useCallback(
    async (convId: string, history: Message[]) => {
      if (model === '') {
        setError({ title: 'No model available', detail: 'This host has not shared a model with your invite.' });
        return;
      }
      const replyId = newId();
      patch(convId, (c) => ({
        ...c,
        updatedAt: Date.now(),
        messages: [...history, { id: replyId, role: 'assistant', content: '', model }],
      }));
      setError(null);
      const ac = new AbortController();
      abort.current = ac;
      setStreaming(true);
      try {
        await streamChat(
          live.transport,
          live.secret,
          { model, messages: toChatMessages(history, settings), temperature: settings.temperature },
          (delta) => {
            patch(convId, (c) => ({
              ...c,
              messages: c.messages.map((m) =>
                m.id !== replyId
                  ? m
                  : {
                      ...m,
                      content: delta.content ? m.content + delta.content : m.content,
                      reasoning: delta.reasoning ? (m.reasoning ?? '') + delta.reasoning : m.reasoning,
                      tokens: delta.usage
                        ? { in: delta.usage.prompt_tokens, out: delta.usage.completion_tokens }
                        : m.tokens,
                    },
              ),
            }));
          },
          ac.signal,
        );
      } catch (err) {
        const friendly = describeError(err);
        const stopped = err instanceof DOMException && err.name === 'AbortError';
        if (!stopped) {
          setError(friendly);
          patch(convId, (c) => ({
            ...c,
            messages: c.messages.map((m) =>
              m.id === replyId && m.content === '' ? { ...m, error: friendly.title } : m,
            ),
          }));
          if (friendly.fatal) {
            onDisconnect(friendly.title);
            return;
          }
        }
      } finally {
        setStreaming(false);
        abort.current = null;
        refreshMe();
      }
    },
    [live.transport, live.secret, model, settings, patch, onDisconnect, refreshMe],
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
    setError(null);
    composer.current?.focus();
  }

  const lastUser = lastIndexOfRole(conv.messages, 'user');
  const region = me.host.relay.region;

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
          <button className="ghost tiny" onClick={() => onDisconnect()}>
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
            <span className="path">{describePath(path, region)}</span>
            <Meters me={me} />
          </div>
          <button className="ghost tiny" onClick={() => setSheet(true)}>
            Settings
          </button>
        </header>

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
              <Empty hostName={me.host.name} model={model} onPick={send} />
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

        {error && (
          <div className="banner" role="alert">
            <div>
              <strong>{error.title}</strong> <span className="dim">{error.detail}</span>
            </div>
            <div className="banner-actions">
              {error.retryAfterS !== undefined && (
                <button className="ghost tiny" disabled={retryIn > 0} onClick={regenerate}>
                  {retryIn > 0 ? `Try again in ${retryIn}s` : 'Try again'}
                </button>
              )}
              <button className="ghost tiny" onClick={() => setError(null)}>
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
          me={me}
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

function Empty({ hostName, model, onPick }: { hostName: string; model: string; onPick: (t: string) => void }) {
  const prompts = [
    'Explain what just happened when I pasted that code.',
    'Write a haiku about borrowing a stranger’s GPU.',
    'What can you help me with?',
  ];
  return (
    <div className="empty">
      <h2>You’re on {hostName}.</h2>
      <p className="dim">
        {model ? `${model} is listening.` : 'Waiting for a model.'} Ask it anything — the host sees
        counts, not conversations.
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
            if (e.key === 'Enter' && !e.shiftKey) {
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
  me,
  onChange,
  onClose,
}: {
  settings: Settings;
  models: string[];
  me: Live['me'];
  onChange: (s: Settings) => void;
  onClose: () => void;
}) {
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
          Your invite is <code>{me.key.name}</code> ({me.key.id}) on {me.host.name}. Limits:{' '}
          {me.limits.rpm}/min, {compact(me.limits.daily_tokens)} tokens a day, {me.limits.max_concurrent} at
          a time. Engine: {me.host.upstream.kind}
          {me.host.upstream.model_context > 0 ? `, ${compact(me.host.upstream.model_context)} context` : ''}.
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
    if (m.error) continue;
    if (m.role === 'assistant' && m.content.trim() === '') continue;
    out.push({ role: m.role, content: m.content });
  }
  return out;
}

function lastIndexOfRole(messages: Message[], role: Message['role']): number {
  for (let i = messages.length - 1; i >= 0; i--) if (messages[i]?.role === role) return i;
  return -1;
}
