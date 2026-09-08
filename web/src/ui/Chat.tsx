import { Text } from '../i18n/RichText';
import { tr, privacy, appLanguage } from '../i18n/text';
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
  timeoutSignal,
  type FriendlyError,
  type Me,
} from '../api';
import { SOURCE_URL, VERSION } from '../product';

/** The composer grows with its text up to this many pixels, then scrolls (040). */
const COMPOSER_MAX = 200;
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
  adoptInviteScope,
  carriedAfter,
  chatsChanged,
  deleteChat,
  DEFAULT_SETTINGS,
  electStore,
  forget,
  hostScope,
  KEYS,
  load,
  loadChats,
  mergeChats,
  modelFor,
  newConversation,
  newId,
  rememberLeft,
  reopenChats,
  save,
  saveChat,
  scopedKeys,
  titleFrom,
  undelivered,
  type Conversation,
  type Message,
  type Settings,
  type Thinking,
} from '../storage';
import { carried, chatSpeed, contextCarried, msText, rateText, reduceReply, saidInBanner, startReply, thinkingFields, tokensSaved, type Reply } from '../stream';
import { composing } from './composing';
import MessageView from './Message';
import { coarsePointer } from './pointer';

interface Props {
  state: SessionState;
  live: Live;
  dispatch: (e: SessionEvent) => void;
  /** Throw this session away and dial the same host again (014 promise 13). */
  onRedial: (l: Live) => void;
  /** A redial is in flight (023): this screen is rendered from the session being replaced, so it
   *  stays mounted and the fresh tunnel is never torn down by a remount. Nothing can be sent yet. */
  reconnecting?: boolean;
}

/** What the last exchange offers the reader, when the reply did not simply work. */
export interface ThreadAction {
  label: string;
  run: () => void;
  /** A wait the reader has to sit out (024 promise 3): the same countdown the banner shows. */
  disabled?: boolean;
}

/** How long "Chat deleted · Undo" stays up before the deletion is really a deletion. */
const UNDO_MS = 6000;
/** A streaming reply is written to storage at most this often (DESIGN §2.3 persistence rules). */
const CHECKPOINT_MS = 2000;
/** The one poll behind everything the header claims (020 promise 4): the path and /me, together. */
const POLL_MS = 30_000;
const TAKEN_OVER = () => tr('app_another_tab_took_over_this_chat_what_is_above');

export default function Chat({ state, live, dispatch, onRedial, reconnecting = false }: Props) {
  // Conversations and settings belong to this host and this invite, never to "the browser".
  const scope = hostScope(live.addr);
  const keys = scopedKeys(scope);
  const me = live.me;
  const host = hostName(me);
  const touch = coarsePointer();

  const [listed, setListed] = useState<string[]>([]);
  // Merged over the defaults: a build that did not know a setting stored none of it.
  const [settings, setSettings] = useState<Settings>(() => ({ ...DEFAULT_SETTINGS, ...load(keys.settings, DEFAULT_SETTINGS) }));
  const [convs, setConvs] = useState<Conversation[]>(() => {
    adoptInviteScope(live.addr, live.me.key.id); // an earlier build's invite-scoped chats, once (024)
    return orNew(loadChats(scope));
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
  const locked = live.key !== 'active' || reconnecting;
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
      else abort.current?.abort(TAKEN_OVER());
    });
    takeOver.current = store.takeOver;
    return store.release;
  }, [scope]);

  // The path is measured and /me is asked on one clock, every 30 s, so the meters, the engine
  // line and the pill can never be older than that (020 promise 4). A failure says so rather than
  // leaving the last number on screen as if it were current.
  useEffect(() => {
    if (reconnecting) return; // the session on screen is the one being replaced: nothing to ask it
    const tick = () => {
      void live.transport
        .ping()
        .then((p) => dispatch(p ? { t: 'pingOk', path: p, at: Date.now() } : { t: 'pingFail' }))
        .catch(() => dispatch({ t: 'pingFail' }));
      void refreshMe();
    };
    const timer = setInterval(tick, POLL_MS);
    return () => clearInterval(timer);
  }, [live.transport, dispatch, refreshMe, reconnecting]);

  // The moment a cooldown reaches zero the numbers it was about have changed: ask, rather than
  // showing "0 messages left" above an enabled Try again (promise 4).
  const cooled = retryUntil > 0 && now >= retryUntil;
  useEffect(() => {
    if (cooled && !reconnecting) void refreshMe();
  }, [cooled, refreshMe, reconnecting]);

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
          title: tr('app_no_model_available'),
          detail: tr('app_this_host_has_not_shared_a_model_with_your'),
        });
        return;
      }
      const replyId = newId();
      // One derivation of what goes to the host (024): the meter reads the same function. A turn
      // too long for the model's memory on its own is left out, and this reply says so.
      const ctx = me.host.upstream.model_context;
      const { messages, leftOut } = carried(history, settings, ctx, history[history.length - 1]);
      const note =
        leftOut.length === 0
          ? undefined
          : (leftOut.length === 1 ? tr('app_earlier_message_omitted', { context: compact(ctx), host: host || tr('app_the_host_lowercase') }) : tr('app_earlier_messages_omitted', { count: leftOut.length, context: compact(ctx), host: host || tr('app_the_host_lowercase') }));
      patch(convId, (c) => ({
        ...c,
        updatedAt: Date.now(),
        messages: [...history, { id: replyId, role: 'assistant', content: '', model, ...(previous ? { previous } : {}), ...(note ? { note } : {}), ...(settings.thinking !== 'default' ? { thinking: settings.thinking } : {}) }],
      }));
      setBanner(null);
      setRetryUntil(0);
      const ac = new AbortController();
      abort.current = ac;
      setStreaming(true);

      let reply: Reply = startReply(); // the clock behind the footer's numbers starts at Send (032)
      let failed: { code: string; error: FriendlyError } | null = null;
      try {
        for await (const ev of chatEvents(
          live.transport,
          live.secret,
          { model, messages, temperature: settings.temperature, ...thinkingFields(settings.thinking) },
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
    [live.transport, live.secret, model, settings, patch, dispatch, refreshMe, host, me.host.upstream.model_context],
  );

  function send(text: string): void {
    if (streaming || locked || readOnly || text.trim() === '') return;
    // The turn goes into the thread before anything is sent: if nothing comes back it is still
    // there, marked as undelivered by derivation (022 promise 2), with something to press.
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
    if (streaming || locked || readOnly) return;
    const idx = lastIndexOfRole(conv.messages, 'user');
    if (idx < 0) return;
    const replaced = conv.messages.slice(idx + 1).find((m) => m.content.trim() !== '')?.content;
    const asked = text === undefined ? (conv.messages[idx] as Message) : { ...(conv.messages[idx] as Message), content: text.trim() };
    const history: Message[] = [...conv.messages.slice(0, idx), asked];
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

  /**
   * A revoked invite's one move (020 promise 5): a fresh connect card, never Connect for this code.
   * Only the dead code is forgotten — a reload must not dial it again — and nothing else is: the
   * tunnel identity, the last host and every chat stay exactly where they are (022 promise 6).
   */
  function pasteNewCode(): void {
    forget(KEYS.invite);
    dispatch({ t: 'abort', error: null });
  }

  const lastUser = lastIndexOfRole(conv.messages, 'user');
  // The turns no request has carried through (022 promise 2): one derivation, read by every mark
  // and by the action's name. Non-empty means the last exchange failed, and Try again resends all
  // of them at once, because the history it sends holds every one.
  const lost = undelivered(conv.messages);
  const softened = carriedAfter(conv.messages);
  // One countdown for both Try agains (024 promise 3): the banner's and the thread's.
  const cooling = waiting > 0;
  // One action on the last exchange, named for what it will actually do. Reconnect is offered
  // exactly while this session has not reached the host since it last tried (a request that got
  // no answer, a /me that timed out): retrying over a session we have not proved alive is the
  // 30 s wait 014 promise 13 exists to remove, and the next /me that gets through changes the word.
  const action: ThreadAction | null =
    readOnly || locked
      ? null
      : !live.meOk
        ? { label: tr('app_reconnect'), run: () => onRedial(live) }
        : lost.size > 0
          ? { label: cooling ? tr('app_try_again_in', { duration: waitText(waiting) }) : tr('app_try_again'), run: regenerate, disabled: cooling }
          : { label: tr('app_regenerate'), run: regenerate };
  const degraded = state.name === 'degraded' ? degradedLine(state.reason, live) : null;
  const unknown = metersUnknown(live);
  const limits = { maxOutputTokens: me.limits.max_output_tokens, modelContext: me.host.upstream.model_context };

  return (
    <div lang={appLanguage() === 'zh' ? 'zh-Hans' : 'en'} className={`app ${appLanguage()} ${drawer ? 'drawer-open' : ''}`}>
      <aside className="sidebar">
        <div className="sidebar-head">
          <button className="secondary wide" onClick={startNew} disabled={readOnly}>
            {tr('app_new_chat')}
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
                <button className="conv-del" aria-label={tr('app_delete_chat', { title: c.title })} onClick={() => remove(c)}>
                  ×
                </button>
              )}
            </div>
          ))}
        </nav>
        <div className="sidebar-foot">
          <button
            className="ghost tiny"
            onClick={() => {
              rememberLeft(); // outlives the tab: the next visit shows the card and waits (022 promise 5)
              dispatch({ t: 'abort', error: null });
            }}
          >
            {tr('app_disconnect')}
          </button>
        </div>
      </aside>

      <div className="backdrop" onClick={() => setDrawer(false)} />

      <main className={`main ${unknown ? 'stale' : ''}`}>
        <header className="topbar">
          <button className="hamburger" aria-label={tr('app_conversations')} onClick={() => setDrawer(true)}>
            ☰
          </button>
          <div className="who">
            <strong>{host || 'This host'}</strong>
            <span className="dim">{modelLabel(model)}</span>
          </div>
          <div className="truth">
            {/* The pill's three states are the same three the line already says; the modifier only
                lets the stylesheet colour the dot (038). No new state, no new element, no new copy. */}
            <span className={`path ${pathState(live)}`}>{pathLine(live, now)}</span>
            <Meters live={live} used={contextCarried(conv.messages, settings, me.host.upstream.model_context)} onOpen={() => setLimitsSheet(true)} />
          </div>
          <button className="ghost tiny" onClick={() => setSheet(true)}>
            {tr('app_settings')}
          </button>
        </header>

        {degraded && (
          <p className={`degraded ${state.name === 'degraded' ? state.reason : ''}`} role="status">
            <span>{degraded}</span>
            {keyDead(live) && (
              <button className="ghost tiny" onClick={pasteNewCode}>
                {tr('app_paste_a_new_code')}
              </button>
            )}
          </p>
        )}
        {leader === false && (
          <p className="degraded follower" role="status">
            <span>{tr('app_this_chat_is_open_in_another_tab')}</span>
            <button className="ghost tiny" onClick={() => takeOver.current()}>
              {tr('app_use_this_tab_instead')}
            </button>
          </p>
        )}
        {logsPrompts(me) && (
          <p className="logging" role="status">
            {tr('app_this_host_records_prompts_and_replies_to_a_log')}</p>
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
                  undelivered={lost.has(m.id)}
                  carried={softened.has(m.id)}
                  readOnly={readOnly}
                  last={i >= lastUser && i >= conv.messages.length - 2}
                  action={action}
                  limits={limits}
                  saved={tokensSaved(conv.messages, i)}
                  onContinue={() => send(tr('app_continue_from_where_you_stopped'))}
                  onNewChat={startNew}
                  onResend={resend}
                />
              ))
            )}
          </div>
        </div>

        {undo && (
          <div className="toast" role="status">
            <span>{tr('app_chat_deleted')}</span>
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
              {tr('app_undo')}
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
                  {waiting > 0 ? tr('app_try_again_in', { duration: waitText(waiting) }) : tr('app_try_again')}
                </button>
              )}
              <button
                className="ghost tiny"
                onClick={() => {
                  setBanner(null);
                  setRetryUntil(0);
                }}
              >
                {tr('app_dismiss')}
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
              ? tr('app_send_will_work_again_the_moment_your_host_resumes')
              : locked || readOnly || touch
                ? null
                : tr('app_enter_sends_shift_enter_makes_a_new_line')
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
          thinks={settings.thinking !== 'default' || conv.messages.some((m) => Boolean(m.reasoning))}
          onChange={setSettings}
          onClose={() => setSheet(false)}
        />
      )}
      {limitsSheet && <LimitsSheet live={live} messages={conv.messages} onClose={() => setLimitsSheet(false)} />}
    </div>
  );
}

/** Which of the path pill's three states `pathLine` is describing, for the stylesheet (038). */
function pathState(l: Live): 'direct' | 'relayed' | 'reconnecting' {
  if (!l.pathOk || !l.meOk) return 'reconnecting';
  return l.path?.direct === true ? 'direct' : 'relayed';
}

/** A store with nothing in it still needs a chat to type into. */
function orNew(convs: Conversation[]): Conversation[] {
  return convs.length > 0 ? convs : [newConversation()];
}

function Meters({ live, used, onOpen }: { live: Live; used: number | null; onOpen: () => void }) {
  const context = contextMeter(used, live.me.host.upstream.model_context);
  const all: MeterView[] = context ? [...meters(live), context] : meters(live);
  return (
    <button className="meters" onClick={onOpen} aria-label={tr('app_what_these_limits_mean')}>
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
function LimitsSheet({ live, messages, onClose }: { live: Live; messages: readonly Message[]; onClose: () => void }) {
  const me = live.me;
  const { rpm, daily_tokens: daily } = me.limits;
  const context = me.host.upstream.model_context;
  const pace = chatSpeed(messages);
  return (
    <div className="sheet-wrap" onClick={onClose}>
      <div className="sheet" onClick={(e) => e.stopPropagation()}>
        <h3>{tr('app_your_limits')}</h3>
        <p>
          <strong>{tr('app_limits_rpm', { rpm })}</strong> {tr('app_your_host_caps_how_fast_one_invite_can_send')}</p>
        <p>
          <strong>{tr('app_limits_daily', { daily: compact(daily) })}</strong> {tr('app_a_token_is_roughly_three_quarters_of_a_word')}</p>
        {context > 0 && (
          <p>
            <strong>{tr('app_limits_context', { context: compact(context) })}</strong> {tr('app_the_model_s_memory_the_meter_is_what_the')}</p>
        )}
        {/* Speed, from where the reader sits (032): this chat's medians and what is inside them. The
            relay round trip is quoted only when there is one: direct mode has no hop. */}
        {pace.n > 0 && (
          <p>
            <strong>
              {pace.ttftMs !== undefined ? tr('app_first_token_time', { duration: msText(pace.ttftMs) }) : tr('app_every_reply_so_far_waited_for_a_slot_first')}
              {pace.tokPerS !== undefined ? tr('app_per_token_speed', { rate: rateText(pace.tokPerS), duration: msText(1000 / pace.tokPerS) }) : ''}.
            </strong>{' '}
            {tr(pace.n === 1 ? 'app_median_one_reply' : 'app_median_replies', { count: pace.n })}
            {live.mode === 'tunnel' && live.pathOk && live.path
              ? tr('app_relay_hop_in_timing', { rtt: Math.round(live.path.rttMs) })
              : ''}{' '}
            {tr('app_a_reply_that_waited_for_a_free_slot_says')}</p>
        )}
        <button className="primary small" onClick={onClose}>
          {tr('app_got_it')}
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
    tr('app_how_can_you_answer_me_if_you_are_running'),
    tr('app_write_a_haiku_about_borrowing_a_stranger_s_gpu'),
    tr('app_what_can_you_help_me_with'),
  ];
  return (
    <div className="empty">
      {/* A gateway need not have a name for itself; "You’re on ." is not a sentence. */}
      {host !== '' && <h2>{tr('app_you_are_on_host', { host })}</h2>}
      <p className="dim">
        {engineDown
          ? tr('app_model_unavailable', { model: modelLabel(model) || tr('app_the_model') })
          : model !== ''
            ? tr('app_model_listening', { model: modelLabel(model) })
            : tr('app_waiting_for_a_model')}
      </p>
      <p className="dim">{privacy(host, logging)}</p>
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
  // The field's height follows its content, never the other way round: measured from the text on
  // every change and on every viewport change, so a cleared field shrinks back and a rotated phone
  // re-fits. `scrollHeight` excludes the border and the box is border-box, so the border is added or
  // a one-line field is two pixels short and grows a scrollbar the moment you type. The scrollbar
  // exists only once the field has hit its cap (040).
  useLayoutEffect(() => {
    const fit = () => {
      const el = ref.current;
      if (!el) return;
      el.style.height = 'auto';
      const border = el.offsetHeight - el.clientHeight;
      const full = el.scrollHeight + border;
      el.style.height = `${Math.min(COMPOSER_MAX, full)}px`;
      el.style.overflowY = full > COMPOSER_MAX ? 'auto' : 'hidden';
    };
    fit();
    window.addEventListener('resize', fit);
    return () => window.removeEventListener('resize', fit);
  }, [ref, text]);
  return (
    <div className="composer">
      <div className="composer-box">
        <textarea
          ref={ref}
          value={text}
          rows={1}
          aria-label={tr('app_message')}
          placeholder={tr('app_message_the_host_s_model')}
          disabled={disabled}
          onChange={(e) => onText(e.target.value)}
          onKeyDown={(e) => {
            // On a touch keyboard Return is the only way to make a new line, so Send is the only
            // way to send (014 promise 6). Enter mid-composition commits an IME candidate and must
            // not send either (007 promise 9).
            if (touch || e.key !== 'Enter' || e.shiftKey || composing(e)) return;
            e.preventDefault();
            if (!streaming && !disabled && text.trim() !== '') onSend(text);
          }}
        />
        {streaming ? (
          <button className="primary small" onClick={onStop}>
            {tr('app_stop')}
          </button>
        ) : (
          <button
            className="primary small"
            disabled={disabled || text.trim() === ''}
            onClick={() => {
              onSend(text);
              ref.current?.focus();
            }}
          >
            {tr('app_send')}
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
  thinks,
  onChange,
  onClose,
}: {
  settings: Settings;
  models: string[];
  live: Live;
  /** The model has shown its thinking in this chat, or a choice is already in force (031 promise 2). */
  thinks: boolean;
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
        <h3>{tr('app_settings')}</h3>
        <label className="field">
          <span className="field-label">{tr('app_model')}</span>
          <select value={chosen} onChange={(e) => setDraft({ ...draft, model: e.target.value })}>
            {models.map((m) => (
              <option key={m} value={m}>
                {modelLabel(m)}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span className="field-label">{tr('app_system_prompt')}</span>
          <textarea
            rows={4}
            value={draft.systemPrompt}
            placeholder={tr('app_optional_sent_ahead_of_every_message_in_this_browser')}
            onChange={(e) => setDraft({ ...draft, systemPrompt: e.target.value })}
          />
        </label>
        <label className="field">
          <span className="field-label">{tr('app_temperature', { value: draft.temperature.toFixed(2) })}</span>
          <input
            type="range"
            min={0}
            max={2}
            step={0.05}
            value={draft.temperature}
            onChange={(e) => setDraft({ ...draft, temperature: Number(e.target.value) })}
          />
          <span className="field-hint">{tr('app_lower_is_more_predictable_higher_is_more_surprising')}</span>
        </label>
        {/* Truthful surface (031): a switch for something this model has never done is a claim, so
            until it has thought here the row only says what is in force. */}
        <label className="field">
          <span className="field-label">{tr('app_thinking')}{thinks ? '' : ' ' + tr('app_thinking_model_default')}</span>
          {thinks ? (
            <select value={draft.thinking} onChange={(e) => setDraft({ ...draft, thinking: e.target.value as Thinking })}>
              <option value="default">{tr('app_model_default')}</option>
              <option value="on">{tr('app_on_better_answers_on_hard_questions')}</option>
              <option value="off">{tr('app_off_faster_shorter_fewer_of_your_tokens')}</option>
            </select>
          ) : (
            <span className="field-hint">{tr('app_the_switch_appears_once_the_model_has_shown_its')}</span>
          )}
        </label>
        <p className="dim small-print">
          {privacy(hostName(me), logsPrompts(me))} <Text name="app_settings_invite" values={{ name: <code>{me.key.name}</code>, id: me.key.id }} />{' '}
          {tr('app_settings_limits', { rpm: me.limits.rpm, daily: compact(me.limits.daily_tokens), concurrent: me.limits.max_concurrent, output: compact(me.limits.max_output_tokens) })}{' '}
          {tr('app_settings_engine', { engine: me.host.upstream.kind })}
          {me.host.upstream.model_context > 0 ? tr('app_settings_context', { context: compact(me.host.upstream.model_context) }) : ''}
          {live.meOk && !me.host.upstream.healthy ? tr('app_not_answering_right_now') : ''}.{' '}
          <Text name="app_settings_model_id" values={{ model: <code>{chosen || tr('app_none')}</code> }} />
          {live.ephemeral
            ? tr('app_another_tab_of_this_browser_holds_the_saved_tunnel')
            : ''}
          {live.pathOk ? '' : tr('app_path_last_measured', { ago: ago(Date.now() - live.pathAt) })}
        </p>
        {/* The app's half of a bug report; the host's half is `infercat version`. */}
        <p className="dim small-print build">
          {tr('app_app_version')} {VERSION} · MIT · <a href={SOURCE_URL}>{tr('source')}</a>
        </p>
        <div className="sheet-actions">
          <button className="ghost" onClick={onClose}>
            {tr('app_cancel')}
          </button>
          <button
            className="primary small"
            onClick={() => {
              onChange(draft);
              onClose();
            }}
          >
            {tr('app_done')}
          </button>
        </div>
      </div>
    </div>
  );
}


function lastIndexOfRole(messages: Message[], role: Message['role']): number {
  for (let i = messages.length - 1; i >= 0; i--) if (messages[i]?.role === role) return i;
  return -1;
}
