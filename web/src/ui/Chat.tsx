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
  type ChatMessage,
  type FriendlyError,
  type Me,
} from '../api';
import { privacyLine, VERSION } from '../product';
import {
  ago,
  compact,
  contextMeter,
  degradedLine,
  keyDead,
  meters,
  metersUnknown,
  pathLine,
  waitText,
  type Live,
  type MeterView,
  type SessionEvent,
  type SessionState,
} from '../session';
import {
  chatsChanged,
  deleteChat,
  DEFAULT_SETTINGS,
  electStore,
  forget,
  hostScope,
  isAnswer,
  KEYS,
  load,
  loadChats,
  mergeChats,
  modelFor,
  newConversation,
  newId,
  reopenChats,
  save,
  saveChat,
  scopedKeys,
  settlePending,
  titleFrom,
  type Conversation,
  type Message,
  type Settings,
} from '../storage';
import { contextUsed, NEW_REPLY, reduceReply, saidInBanner, type Reply } from '../stream';
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
/** The one poll behind everything the header claims (020 promise 4): the path and /me, together. */
const POLL_MS = 30_000;
/** Why a reply ends when another tab takes the chat over: the reason the abort carries (promise 6). */
const TAKEN_OVER = 'Another tab took over this chat — what is above is only part of it.';

export default function Chat({ state, live, dispatch, onRedial }: Props) {
  // Conversations and settings belong to this host and this invite, never to "the browser".
  const scope = hostScope(live.addr, live.me.key.id);
  const keys = scopedKeys(scope);
  const me = live.me;
  const host = hostName(me);
  const touch = coarsePointer();

  const [listed, setListed] = useState<string[]>([]);
  const [settings, setSettings] = useState<Settings>(() => load(keys.settings, DEFAULT_SETTINGS));
  const [convs, setConvs] = useState<Conversation[]>(() => orNew(loadChats(scope)));
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
  // Which tab writes this host's store (020 promise 6): null until the election has answered.
  const [leader, setLeader] = useState<boolean | null>(null);
  const leaderRef = useRef(false);
  const takeOver = useRef<() => void>(() => {});
  const abort = useRef<AbortController | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const composer = useRef<HTMLTextAreaElement>(null);
  const savedAt = useRef(0);

  const conv = convs.find((c) => c.id === currentId) ?? (convs[0] as Conversation);
  const models = me.host.models.length > 0 ? me.host.models : listed;
  const model = modelFor(settings.model, models) ?? models[0] ?? '';
  const waiting = Math.max(0, retryUntil - now);
  // Nothing can be sent while the invite is off, or from a tab that does not own the store.
  const locked = live.key !== 'active';
  const readOnly = leader !== true;

  const patch = useCallback((id: string, fn: (c: Conversation) => Conversation) => {
    setConvs((prev) => prev.map((c) => (c.id === id ? fn(c) : c)));
  }, []);

  const persist = useCallback(
    (c: Conversation) => {
      if (!leaderRef.current) return; // a follower reads; it never writes (promise 6)
      savedAt.current = Date.now();
      saveChat(scope, c);
    },
    [scope],
  );

  /**
   * The one source of the engine's health and the meters (020 promise 4). Resolves to whether the
   * host is reachable and its engine healthy — which is also what the stream asks when it has gone
   * quiet (promise 2). What it learns is shared with every other tab of this browser (promise 6).
   */
  const refreshMe = useCallback((): Promise<boolean> => {
    return getMe(live.transport, live.secret, timeoutSignal(ME_TIMEOUT_MS))
      .then((next) => {
        dispatch({ t: 'meOk', me: next });
        save(keys.me, next);
        return next.host.upstream.healthy;
      })
      .catch((err: unknown) => {
        // The machine decides: an invite code changes the key's state, anything else keeps the
        // snapshot but stops presenting it as current.
        dispatch({ t: 'meError', error: describeError(err, host) });
        return false;
      });
  }, [live.transport, live.secret, dispatch, host, keys.me]);

  // One tab writes (020 promise 6). The election is the same Web Lock the tunnel identity uses;
  // the leader takes what is on disk as the truth — including a reply nobody is writing any more,
  // which is an orphan and says so — and a tab that loses the lock mid-reply ends that reply now,
  // with its reason, so the tab that took over never finds it still writing.
  useEffect(() => {
    const store = electStore(scope, (isLeader) => {
      leaderRef.current = isLeader;
      setLeader(isLeader);
      if (isLeader) setConvs(orNew(reopenChats(loadChats(scope))));
      else abort.current?.abort(TAKEN_OVER);
    });
    takeOver.current = store.takeOver;
    return store.release;
  }, [scope]);

  // The path is measured and /me is asked on one clock, every 30 s, so the meters, the engine
  // line and the pill can never be older than that (020 promise 4). A failure says so rather than
  // leaving the last number on screen as if it were current.
  useEffect(() => {
    const tick = () => {
      void live.transport
        .ping()
        .then((p) => dispatch(p ? { t: 'pingOk', path: p, at: Date.now() } : { t: 'pingFail' }))
        .catch(() => dispatch({ t: 'pingFail' }));
      void refreshMe();
    };
    const timer = setInterval(tick, POLL_MS);
    return () => clearInterval(timer);
  }, [live.transport, dispatch, refreshMe]);

  // The moment a cooldown reaches zero the numbers it was about have changed: ask, rather than
  // showing "0 messages left" above an enabled Try again (promise 4).
  const cooled = retryUntil > 0 && now >= retryUntil;
  useEffect(() => {
    if (cooled) void refreshMe();
  }, [cooled, refreshMe]);

  // /v1/models is a fallback, not a routine (014 promise 5): /me already carries the models this
  // invite may use, so asking again spends one of the friend's own requests for an answer we
  // already have. Only a host that reported none — LM Studio with nothing loaded — is worth asking.
  useEffect(() => {
    if (me.host.models.length > 0) return;
    void getModels(live.transport, live.secret)
      .then((list) => list.length > 0 && setListed(list))
      .catch(() => {});
  }, [live.transport, live.secret, me.host.models.length]);

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
  // transition — which is what `streaming` going false is — is written at once. By the leader.
  useEffect(() => {
    if (conv.messages.length === 0 || leader !== true) return;
    if (!streaming) {
      persist(conv);
      return;
    }
    const timer = setTimeout(
      () => persist(conv),
      Math.max(0, CHECKPOINT_MS - (Date.now() - savedAt.current)),
    );
    return () => clearTimeout(timer);
  }, [conv, streaming, leader, persist]);

  useEffect(() => save(keys.settings, settings), [settings, keys.settings]);

  // Another tab of this browser wrote to this host's history, or got a fresher /me. A follower
  // takes the store as it is; the leader re-reads before writing again, so the only thing that can
  // be lost is a turn it never saw, and this is where it sees it (014 promise 4, 020 promise 6).
  // A shared /me only ever updates a session that is itself reaching the host: another tab's good
  // news must not make this tab's broken session look healthy.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === keys.me) {
        const shared = load<Me | null>(keys.me, null);
        if (shared && live.meOk) dispatch({ t: 'meOk', me: shared });
        return;
      }
      if (!chatsChanged(scope, e.key)) return;
      setConvs((prev) => (leaderRef.current ? mergeChats(scope, prev) : orNew(loadChats(scope))));
    };
    globalThis.addEventListener('storage', onStorage);
    return () => globalThis.removeEventListener('storage', onStorage);
  }, [scope, keys.me, live.meOk, dispatch]);

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
      patch(convId, (c) => ({
        ...c,
        updatedAt: Date.now(),
        messages: [...history, { id: replyId, role: 'assistant', content: '', model, ...(previous ? { previous } : {}) }],
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
          undefined,
          host,
          refreshMe,
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

      // The turn was delivered when the host produced something, or the reader stopped it. The
      // rule that clears the mark is the store's (020 promise 1), so what is shown and what is
      // persisted can never disagree; anything else leaves exactly this turn pending.
      patch(convId, (c) => ({ ...c, messages: settlePending(c.messages) }));

      if (failed) {
        // The message already carries the failure as its own status; the banner is only for the
        // waits the reader has to sit out, so nothing is said in two places (014 promise 10).
        dispatch({ t: 'streamError', code: failed.code, error: failed.error });
        if (saidInBanner(failed.code) && failed.error.retryAfterS !== undefined) {
          setBanner(failed.error);
          setRetryUntil(Date.now() + failed.error.retryAfterS * 1000);
          setNow(Date.now());
        }
      }
      void refreshMe();
    },
    [live.transport, live.secret, model, settings, patch, dispatch, refreshMe, host],
  );

  function send(text: string): void {
    if (streaming || locked || readOnly || text.trim() === '') return;
    // Pending from the moment it is sent, and only this turn (020 promise 1): that mark is what
    // keeps the reader's words in the thread, with something to press, if nothing comes back.
    const message: Message = { id: newId(), role: 'user', content: text.trim(), pending: true };
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
    if (streaming || locked || readOnly) return;
    const idx = lastIndexOfRole(conv.messages, 'user');
    if (idx < 0) return;
    const replaced = conv.messages.slice(idx + 1).find((m) => m.content.trim() !== '')?.content;
    const asked = text === undefined ? (conv.messages[idx] as Message) : { ...(conv.messages[idx] as Message), content: text.trim() };
    const history: Message[] = [...conv.messages.slice(0, idx), { ...asked, pending: true }];
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

  /** A revoked invite's one move (020 promise 5): a fresh connect card, never Connect for this code. */
  function pasteNewCode(): void {
    forget(KEYS.invite, KEYS.lastHost);
    dispatch({ t: 'abort', error: null });
  }

  const lastUser = lastIndexOfRole(conv.messages, 'user');
  const pendingTurn = conv.messages.some((m) => m.pending === true);
  // One action on the last exchange, named for what it will actually do. Reconnect is offered
  // exactly while this session has not reached the host since it last tried (a request that got
  // no answer, a /me that timed out): retrying over a session we have not proved alive is the
  // 30 s wait 014 promise 13 exists to remove, and the next /me that gets through changes the word.
  const action: ThreadAction | null =
    readOnly || locked
      ? null
      : !live.meOk
        ? { label: 'Reconnect', run: () => onRedial(live) }
        : pendingTurn
          ? { label: 'Try again', run: regenerate }
          : { label: 'Regenerate', run: regenerate };
  const degraded = state.name === 'degraded' ? degradedLine(state.reason, live) : null;
  const unknown = metersUnknown(live);
  const limits = { maxOutputTokens: me.limits.max_output_tokens, modelContext: me.host.upstream.model_context };

  return (
    <div className={`app ${drawer ? 'drawer-open' : ''}`}>
      <aside className="sidebar">
        <div className="sidebar-head">
          <button className="secondary wide" onClick={startNew} disabled={readOnly}>
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
              {!readOnly && (
                <button className="conv-del" aria-label={`Delete ${c.title}`} onClick={() => remove(c)}>
                  ×
                </button>
              )}
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
            <Meters live={live} used={contextUsed(conv.messages)} onOpen={() => setLimitsSheet(true)} />
          </div>
          <button className="ghost tiny" onClick={() => setSheet(true)}>
            Settings
          </button>
        </header>

        {degraded && (
          <p className={`degraded ${state.name === 'degraded' ? state.reason : ''}`} role="status">
            <span>{degraded}</span>
            {keyDead(live) && (
              <button className="ghost tiny" onClick={pasteNewCode}>
                Paste a new code
              </button>
            )}
          </p>
        )}
        {leader === false && (
          <p className="degraded follower" role="status">
            <span>This chat is open in another tab.</span>
            <button className="ghost tiny" onClick={() => takeOver.current()}>
              Use this tab instead
            </button>
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
                engineDown={live.meOk && !me.host.upstream.healthy}
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
                  answering={conv.messages[i + 1]?.role === 'assistant' && conv.messages[i + 1]?.status === undefined}
                  readOnly={readOnly}
                  last={i >= lastUser && i >= conv.messages.length - 2}
                  action={action}
                  limits={limits}
                  onContinue={() => send('Continue from where you stopped.')}
                  onNewChat={startNew}
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
          disabled={locked || readOnly}
          hint={
            live.key === 'paused'
              ? 'Send will work again the moment your host resumes your invite.'
              : locked || readOnly || touch
                ? null
                : 'Enter sends · Shift+Enter makes a new line'
          }
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

/** A store with nothing in it still needs a chat to type into. */
function orNew(convs: Conversation[]): Conversation[] {
  return convs.length > 0 ? convs : [newConversation()];
}

function Meters({ live, used, onOpen }: { live: Live; used: number | null; onOpen: () => void }) {
  const context = contextMeter(used, live.me.host.upstream.model_context);
  const all: MeterView[] = context ? [...meters(live), context] : meters(live);
  return (
    <button className="meters" onClick={onOpen} aria-label="What these limits mean">
      {all.map((m) => (
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

/** One sheet, one sentence each: the whole explanation of the numbers in the header. */
function LimitsSheet({ me, onClose }: { me: Live['me']; onClose: () => void }) {
  const { rpm, daily_tokens: daily } = me.limits;
  const context = me.host.upstream.model_context;
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
        {context > 0 && (
          <p>
            <strong>{compact(context)} tokens of context.</strong> The model’s memory of this chat —
            everything said so far, both sides. When it fills, this chat cannot go on; a new chat
            starts empty.
          </p>
        )}
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
  disabled,
  hint,
  onSend,
  onStop,
}: {
  ref: React.RefObject<HTMLTextAreaElement | null>;
  text: string;
  onText: (t: string) => void;
  streaming: boolean;
  touch: boolean;
  /** The invite is off, or this tab does not own the store: nothing can be sent from here. */
  disabled: boolean;
  hint: string | null;
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
          disabled={disabled}
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
            if (!streaming && !disabled && text.trim() !== '') {
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
            disabled={disabled || text.trim() === ''}
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
      {hint && <p className="hint">{hint}</p>}
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
          {live.meOk && !me.host.upstream.healthy ? ' — not answering right now' : ''}. Model id:{' '}
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
