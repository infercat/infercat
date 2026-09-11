import { offersHostTools } from '../chatTools';
import ChatToolSteps from './ChatToolSteps';
import { hostImages } from '../api';
import { useImageJobs } from './useImageJobs';
import { ImageJobRow, ImagesSheet } from './ImageJobs';
import { imageGone, imagePrompts, imageLimit } from '../imageJobs';
import RunItemView from './RunItem';
import { useRuns } from './useRuns';
import { fitsRunInput } from '../runs';
import { VoiceRecorder, VoicePlayer, transcribe, requestSpeech, voiceTime, voiceWords, type RecordingState, type SpeechState } from '../voice';
import { useInstall, noteCompletedReply, acceptInstall, dismissInstall } from '../install';
import { encodeInvite } from '../invite';
import { ACCEPT, admit, attachmentFields, retainedFileBytes, rejectionNotice, type AttachmentNotice, type Attachment, type DisplayAttachment, type ClipboardData } from '../attachments';
import { AttachedImages, type ReadingAttachment } from './Images';
import { imageCopy, fitsRequest, type ImageData } from '../images';
import { readImages, storeImages, preparedFrom } from '../image-store';
import { Text } from '../i18n/RichText';
import { tr, privacy, appLanguage } from '../i18n/text';
import { Fragment, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import {
  chatEvents,
  describeError,
  getMe,
  getModels,
  hostName,
  hostAudio,
  GatewayError,
  logsPrompts,
  ME_TIMEOUT_MS,
  modelLabel,
  modelVision,
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
import { carried, carriedImageCount, chatSpeed, contextCarried, msText, rateText, reduceReply, saidInBanner, startReply, thinkingFields, tokensSaved, type Reply } from '../stream';
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
  const install = useInstall();
  const [iosInstall, setIOSInstall] = useState(false);
  const closeIOS = useCallback(() => setIOSInstall(false), []);
  const showInstall = useCallback(() => { if (acceptInstall() === 'ios') { setSheet(false); setIOSInstall(true); } }, []);

  const [listed, setListed] = useState<string[]>([]);
  // Merged over the defaults: a build that did not know a setting stored none of it.
  const [settings, setSettings] = useState<Settings>(() => ({ ...DEFAULT_SETTINGS, ...load(keys.settings, DEFAULT_SETTINGS) }));
  const [convs, setConvs] = useState<Conversation[]>(() => orNew(loadChats(scope)));
  useEffect(() => { if (convs.some((c) => c.messages.some((m) => m.role === 'assistant' && m.status === 'complete'))) noteCompletedReply(); }, [convs]);
  const [currentId, setCurrentId] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [waitingForHeaders, setWaitingForHeaders] = useState(false);
  const [draft, setDraft] = useState('');
  const [imageMode, setImageMode] = useState(false), [imagesOpen, setImagesOpen] = useState(false);
  const closeImages = useCallback(() => setImagesOpen(false), []);
  const [attached, setAttached] = useState<Attachment[]>([]);
  const [imageData, setImageData] = useState<ImageData>({});
  const [loadedImageRefs, setLoadedImageRefs] = useState('');
  const [missingChats, setMissingChats] = useState<Set<string>>(() => new Set());
  const [imageNotice, setImageNotice] = useState<AttachmentNotice | null>(null);
  const [editingTurnId, setEditingTurnId] = useState<string | null>(null);
  const [banner, setBanner] = useState<(FriendlyError & { voice?: true }) | null>(null);
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
  const vision = live.meOk && modelVision(me, model) === true;
  const imageRefs = JSON.stringify(conv.messages.filter((m) => m.images?.length).map(({ id, images }) => ({ id, images })));
  useEffect(() => {
    let active = true;
    void readImages(scope, JSON.parse(imageRefs)).then((data) => { if (active) { setImageData(data); setLoadedImageRefs(imageRefs); } });
    return () => { active = false; };
  }, [scope, imageRefs]);
  const waiting = Math.max(0, retryUntil - now);
  // Nothing can be sent while the invite is off, or from a tab that does not own the store.
  const locked = live.key !== 'active';
  const sendBlocked = reconnecting || live.offline === true;
  const currentLive = useRef(live); currentLive.current = live;
  const readOnly = leader !== true;
  const speechModel = hostAudio(me, 'speech');
  const [speechState, setSpeechState] = useState<SpeechState>({ kind: 'idle' });
  const voiceFault = useRef<(error: unknown) => void>(() => {});
  const [player] = useState(() => new VoicePlayer((next) => { setSpeechState(next); if (next.kind === 'error') voiceFault.current(next.error); }));
  useEffect(() => {
    const leave = () => player.stop();
    window.addEventListener('pagehide', leave);
    return () => { window.removeEventListener('pagehide', leave); player.stop(); };
  }, [player, scope, conv.id, speechModel]);
  useEffect(() => {
    if ((sendBlocked || locked || readOnly || streaming || !speechModel) && (player.state.kind === 'making' || player.state.kind === 'playing')) player.stop();
  }, [sendBlocked, locked, readOnly, streaming, speechModel, player]);


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
        if (currentLive.current.offline || currentLive.current.transport !== live.transport) return false;
        dispatch({ t: 'meOk', me: next });
        save(keys.me, next);
        return next.host.upstream.healthy;
      })
      .catch((err: unknown) => {
        // The machine decides: an invite code changes the key's state, anything else keeps the
        // snapshot but stops presenting it as current.
        if (!currentLive.current.offline && currentLive.current.transport === live.transport) dispatch({ t: 'meError', error: describeError(err, host) });
        return false;
      });
  }, [live.transport, live.secret, dispatch, host, keys.me]);
  const imageWork = useImageJobs(live, !readOnly && !reconnecting, setConvs, dispatch);
  const agentRuns = useRuns(live, !readOnly && !reconnecting, convs, setConvs, dispatch, refreshMe, imageWork.refresh);
  const runSaved = useRef(new Map<string, Conversation>());
  useEffect(() => {
    if (readOnly) return;
    for (const c of convs) if (c.messages.some((m) => m.kind === 'run') && runSaved.current.get(c.id) !== c) { persist(c); runSaved.current.set(c.id, c); }
  }, [convs, readOnly, persist]);
  voiceFault.current = (error) => {
    const friendly = describeError(error, host);
    if (error instanceof GatewayError) dispatch({ t: 'streamError', code: error.code, error: friendly });
    if (friendly.retryAfterS !== undefined) {
      setBanner({ ...friendly, voice: true }); setRetryUntil(Date.now() + friendly.retryAfterS * 1000); setNow(Date.now());
    }
    void refreshMe();
  };
  function listenTo(id: string, text: string): void {
    if (sendBlocked || locked || readOnly || streaming || !speechModel || !text) return;
    void player.play(id, scope, speechModel, text, (signal) => requestSpeech(live.transport, live.secret, text, signal)).then(() => { void refreshMe(); });
  }
  async function recordVoice(clip: Blob, signal: AbortSignal): Promise<string> {
    try { return await transcribe(live.transport, live.secret, clip, appLanguage(), signal); }
    catch (error) { if (!signal.aborted) voiceFault.current(error); throw error; }
    finally { if (!signal.aborted) void refreshMe(); }
  }


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
    if (reconnecting || live.offline) return; // the session on screen is the one being replaced: nothing to ask it
    const tick = () => {
      void live.transport
        .ping()
        .then((p) => dispatch(p ? { t: 'pingOk', path: p, at: Date.now() } : { t: 'pingFail' }))
        .catch(() => dispatch({ t: 'pingFail' }));
      void refreshMe();
    };
    const timer = setInterval(tick, POLL_MS);
    return () => clearInterval(timer);
  }, [live.transport, live.offline, dispatch, refreshMe, reconnecting]);

  // The moment a cooldown reaches zero the numbers it was about have changed: ask, rather than
  // showing "0 messages left" above an enabled Try again (promise 4).
  const cooled = retryUntil > 0 && now >= retryUntil;
  useEffect(() => {
    if (cooled && !reconnecting && !live.offline) void refreshMe();
  }, [cooled, refreshMe, reconnecting, live.offline]);

  // /v1/models is a fallback, not a routine (014 promise 5): /me already carries the models this
  // invite may use, so asking again spends one of the friend's own requests for an answer we
  // already have. Only a host that reported none — LM Studio with nothing loaded — is worth asking.
  useEffect(() => {
    if (me.host.models.length > 0 || reconnecting || live.offline) return;
    void getModels(live.transport, live.secret)
      .then((list) => list.length > 0 && setListed(list))
      .catch(() => {});
  }, [live.transport, live.secret, live.offline, reconnecting, me.host.models.length]);

  // One clock, for the things on screen that are about elapsed time: the retry countdown and the
  // age of a stale path measurement. Re-derived from Date.now() on every tick and whenever the tab
  // comes back, so a backgrounded tab never resumes with a countdown that kept its own time.
  useEffect(() => {
    if (retryUntil === 0 && live.pathOk && live.meOk && !hostImages(me)?.model) return;
    const tick = () => setNow(Date.now());
    tick();
    const timer = setInterval(tick, 1000);
    document.addEventListener('visibilitychange', tick);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', tick);
    };
  }, [retryUntil, live.pathOk, live.meOk, hostImages(me)?.model]);

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
    async (convId: string, history: Message[], previous?: string, fresh: ImageData = {}, retryId?: string) => {
      if (model === '') {
        setStreaming(false);
        setBanner({
          title: tr('app_no_model_available'),
          detail: tr('app_this_host_has_not_shared_a_model_with_your'),
        });
        return;
      }
      const ac = new AbortController();
      abort.current = ac;
      setStreaming(true);
      const stored = await readImages(scope, history);
      if (ac.signal.aborted) { setStreaming(false); abort.current = null; return; }
      const data = { ...stored, ...fresh };
      setImageData(data);
      const replyId = newId();
      // One derivation of what goes to the host (024): the meter reads the same function. A turn
      // too long for the model's memory on its own is left out, and this reply says so.
      const ctx = me.host.upstream.model_context;
      const { messages, leftOut } = carried(history, settings, ctx, history[history.length - 1], data);
      const imageCount = carriedImageCount(messages);
      if (history.some((m) => !leftOut.includes(m) && m.images?.some((i) => !data[i.id]))) {
        setMissingChats((seen) => new Set(seen).add(convId));
      }
      const toolChat = !me.agent && offersHostTools(me);
      const request = { ...(toolChat ? { host_tools: [...me.host_tools!], conversation: convId, client_request_id: replyId } : {}), model, messages, temperature: settings.temperature, ...thinkingFields(settings.thinking) };
      if (me.agent && history.some((m) => !leftOut.includes(m) && m.images?.some((image) => !data[image.id]))) { setImageNotice(rejectionNotice({ reason: 'missing_image', message: tr('app_image_missing') })); setStreaming(false); abort.current = null; return; }
      if (!fitsRequest(request)) {
        setImageNotice(rejectionNotice({ reason: 'body_too_large', message: tr('app_over_message_size', { size: '4 MB' }) }));
        setStreaming(false); abort.current = null; return;
      }
      if (me.agent) {
        setStreaming(false); abort.current = null;
        if (!fitsRunInput(request)) { setImageNotice(rejectionNotice({ reason: 'body_too_large', message: tr('app_over_message_size', { size: '1 MiB' }) })); return; }
        await agentRuns.submit(convId, request, history, retryId); return;
      }
      const note =
        leftOut.length === 0
          ? undefined
          : (leftOut.length === 1 ? tr('app_earlier_message_omitted', { context: compact(ctx), host: host || tr('app_the_host_lowercase') }) : tr('app_earlier_messages_omitted', { count: leftOut.length, context: compact(ctx), host: host || tr('app_the_host_lowercase') }));
      const appendReply = (c: Conversation): Conversation => ({
        ...c,
        updatedAt: Date.now(),
        messages: [...history, { id: replyId, role: 'assistant', content: '', model, imageCount, ...(toolChat ? { hostRun: { keyId: me.key.id, requestId: replyId, ids: [], records: [] } } : {}), ...(previous ? { previous } : {}), ...(note ? { note } : {}), ...(settings.thinking !== 'default' ? { thinking: settings.thinking } : {}) }],
      });
      if (toolChat) { const c = convs.find(c => c.id === convId); if (c) persist(appendReply(c)); }
      patch(convId, appendReply);
      setBanner(null);
      setRetryUntil(0);
      let reply: Reply = startReply(); // the clock behind the footer's numbers starts at Send (032)
      let failed: { code: string; error: FriendlyError } | null = null;
      try {
        for await (const ev of chatEvents(
          live.transport,
          live.secret,
          request,
          ac.signal,
          undefined,
          host,
          refreshMe,
          setWaitingForHeaders,
        )) {
          if (ev.kind === 'run') { patch(convId, c => ({ ...c, messages: c.messages.map(m => m.id === replyId && m.kind !== 'run' && m.hostRun ? { ...m, hostRun: { ...m.hostRun, ids: [...new Set([...m.hostRun.ids, ev.id])] } } : m) })); }
          if (ev.kind === 'error') failed = { code: ev.code, error: ev.error };
          reply = reduceReply(reply, ev);
          if (ev.kind === 'error' && ev.code === 'images_not_supported') {
            reply = { ...reply, note: imageCopy('app_engine_rejected_with_images', imageCount, { host }), details: ev.error.hostSaid ?? ev.error.detail };
          }
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
    [live.transport, live.secret, model, settings, patch, dispatch, refreshMe, host, me.host.upstream.model_context, me.agent, me.host_tools, hostImages(me)?.model, me.key.id, scope, agentRuns, convs, persist],
  );

  async function send(text: string): Promise<void> {
    if (streaming || locked || readOnly || sendBlocked || (text.trim() === '' && !attached.length)) return;
    if (imageMode && hostImages(me)?.model) {
      if (attached.length) { setImageNotice(rejectionNotice({ reason: 'body_too_large', message: tr('app_job_attachments') })); return; }
      if (await imageWork.submit(conv, imagePrompts(text))) { setDraft((value) => value === text ? '' : value); setImageMode(false); }
      return;
    }
    if (attached.some((a) => a.kind === 'image') && !vision) { setImageNotice(rejectionNotice({ reason: 'no_vision', message: tr('app_model_cant_see_images', { model: modelLabel(model) }) })); return; }
    const sending = attached.flatMap((a) => a.kind === 'image' ? [a.image] : []);
    const fresh = Object.fromEntries(sending.map((i) => [i.id, i.data]));
    // The turn goes into the thread before anything is sent: if nothing comes back it is still
    // there, marked as undelivered by derivation (022 promise 2), with something to press.
    const message: Message = { id: newId(), role: 'user', content: text.trim(), ...attachmentFields(attached) };
    const history = [...conv.messages, message];
    if (!fitsRequest({ model, messages: carried(history, settings, me.host.upstream.model_context, message, { ...imageData, ...fresh }).messages, temperature: settings.temperature, ...thinkingFields(settings.thinking) })) {
      setImageNotice(rejectionNotice({ reason: 'body_too_large', message: tr('app_over_message_size', { size: '4 MB' }) })); return;
    }
    if (me.agent && !fitsRunInput({ model, messages: carried(history, settings, me.host.upstream.model_context, message, { ...imageData, ...fresh }).messages, temperature: settings.temperature, ...thinkingFields(settings.thinking) })) {
      setImageNotice(rejectionNotice({ reason: 'body_too_large', message: tr('app_over_message_size', { size: '1 MiB' }) })); return;
    }
    setStreaming(true);
    const next: Conversation = {
      ...conv,
      title: conv.messages.length === 0 ? titleFrom(message.content) : conv.title,
      updatedAt: Date.now(),
      messages: history,
    };
    setConvs((prev) => prev.map((c) => (c.id === next.id ? next : c)));
    persist(next); // before any I/O: a reload from here still has the reader's words (013)
    setDraft('');
    setAttached([]);
    setImageNotice(null);
    pinned.current = true;
    if (sending.length) await storeImages(scope, message.id, sending);
    if (!leaderRef.current) { setStreaming(false); return; }
    void run(conv.id, history, undefined, fresh);
  }

  /**
   * Both ways of asking again drop the answer that is on screen. It is not thrown away: the reply
   * that replaces it carries it as `previous`, behind a disclosure, so a reader who preferred the
   * old one has not lost it to a click (014 promise 16).
   */
  async function replaceAnswer(text?: string, attachments?: DisplayAttachment[]): Promise<void> {
    if (streaming || locked || readOnly || sendBlocked) return;
    const idx = lastIndexOfRole(conv.messages, 'user');
    if (idx < 0) return;
    if (conv.messages.slice(idx + 1).some((m) => m.kind === 'run' && m.runKind === 'image')) {
      if (attachments?.length) { setImageNotice(rejectionNotice({ reason: 'body_too_large', message: tr('app_job_attachments') })); return; }
      await imageWork.submit(conv, imagePrompts(text ?? conv.messages[idx]!.content)); return;
    }
    const replaced = conv.messages.slice(idx + 1).find((m) => m.content.trim() !== '')?.content;
    const original = conv.messages[idx] as Message;
    const asked = { ...original, ...(me.agent ? { id: newId() } : {}), ...(text === undefined ? {} : { content: text.trim() }), ...(attachments ? attachmentFields(attachments) : {}) };
    const history: Message[] = [...conv.messages.slice(0, idx), asked];
    if (attachments) {
      setStreaming(true);
      await storeImages(scope, asked.id, await preparedFrom(attachments.flatMap((a) => a.kind === 'image' ? [a.image] : []), imageData));
    }
    patch(conv.id, (c) => ({
      ...c,
      // An edited first message is what this chat is now about; a title from the old one is stale.
      title: idx === 0 ? titleFrom(asked.content) : c.title,
      messages: me.agent ? [...c.messages, asked] : history,
    }));
    void run(conv.id, history, replaced);
  }

  const regenerate = () => replaceAnswer();
  const resend = (text: string, attachments: DisplayAttachment[]) => { void replaceAnswer(text, attachments); };

  function startNew(): void {
    const next = newConversation();
    setConvs((prev) => [next, ...prev.filter((c) => c.messages.length > 0)]);
    setCurrentId(next.id);
    setEditingTurnId(null);
    setAttached([]); setDraft(''); setImageNotice(null);
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
  const cooling = waiting > 0 && !banner?.voice;
  // One action on the last exchange, named for what it will actually do. Reconnect is offered
  // exactly while this session has not reached the host since it last tried (a request that got
  // no answer, a /me that timed out): retrying over a session we have not proved alive is the
  // 30 s wait 014 promise 13 exists to remove, and the next /me that gets through changes the word.
  const action: ThreadAction | null =
    readOnly || locked || sendBlocked
      ? null
      : !live.meOk
        ? { label: tr('app_reconnect'), run: () => onRedial(live) }
        : lost.size > 0
          ? { label: cooling ? tr('app_try_again_in', { duration: waitText(waiting) }) : tr('app_try_again'), run: regenerate, disabled: cooling }
          : { label: tr('app_regenerate'), run: regenerate };
  const degraded = state.name === 'degraded' && !live.offline ? degradedLine(state.reason, live) : null;
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
          {hostImages(me)?.model && <div className="conv harvest"><button className="conv-open" onClick={() => { setImagesOpen(true); setDrawer(false); }}>{tr('app_job_list')} <span className="count">{imageWork.jobs.filter((j) => j.output && !imageGone(j)).length}</span></button></div>}
          {convs.map((c) => (
            <div key={c.id} className={`conv ${c.id === conv.id ? 'current' : ''}`}>
              <button
                className="conv-open"
                onClick={() => {
                  setCurrentId(c.id);
                  setEditingTurnId(null);
                  setAttached([]); setDraft(''); setImageNotice(null);
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
            <span className={`path ${sendBlocked ? 'reconnecting' : pathState(live)}`}>{live.offline ? tr('app_path_no_network') : reconnecting ? tr('app_path_reconnecting', { host }) : pathLine(live, now)}</span>
            <Meters images={carriedImageCount(carried(conv.messages, settings, me.host.upstream.model_context, undefined, imageData).messages)} live={live} used={live.meOk ? contextCarried(conv.messages, settings, me.host.upstream.model_context) : null} onOpen={() => setLimitsSheet(true)} />
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
            <span>{tr(install.platform.standalone ? 'app_this_chat_is_open_in_another_window' : 'app_this_chat_is_open_in_another_tab')}</span>
            <button className="ghost tiny" onClick={() => takeOver.current()}>
              {tr(install.platform.standalone ? 'app_use_this_window_instead' : 'app_use_this_tab_instead')}
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
                imageRetentionDays={hostImages(me)?.retention_days}
                engineDown={live.meOk && !me.host.upstream.healthy}
                onPick={send}
              />
            ) : (
              conv.messages.map((m, i) => m.kind === 'run' && m.runKind === 'image' ? (
                <ImageJobRow key={m.id} item={m} live={live} connected={agentRuns.connected && !sendBlocked} disabled={readOnly || locked || sendBlocked} pending={imageWork.pending.has(m.job?.id ?? '') || imageWork.pending.has('submit')} onCancel={() => { if (m.job) void imageWork.act(m.job); }} onRetry={() => { if (m.job) void imageWork.submit(conv, [m.job.input.prompt]); }} />
              ) : m.kind === 'run' ? (
                <RunItemView key={m.id} item={m} live={live} connected={agentRuns.connected && !sendBlocked && Boolean(m.run && agentRuns.observed.has(m.run.id))} disabled={readOnly || locked || sendBlocked} pending={agentRuns.pending.has(m.id)}
                  onCancel={() => { void agentRuns.act(m); }} onAnswer={(id, allow) => { void agentRuns.act(m, { id, allow }); }}
                  onRetry={() => { const at = lastIndexOfRole(conv.messages.slice(0, i), 'user'); if (at >= 0) void run(conv.id, conv.messages.slice(0, at + 1), undefined, {}, m.id); }} />
              ) : (
                <Fragment key={m.id}><div id={`turn-${m.id}`}><MessageView
                  message={m}
                  speech={speechModel ? { state: speechState, onListen: listenTo, onStop: () => player.stop() } : undefined}
                  imageData={imageData}
                  onEditing={setEditingTurnId}
                  imagesLoaded={loadedImageRefs === imageRefs}
                  host={host}
                  live={streaming && !conv.messages.slice(i + 1).some(m => m.kind !== 'run' && m.role === 'assistant')}
                  busy={streaming}
                  answering={conv.messages[i + 1]?.role === 'assistant' && (conv.messages[i + 1]?.kind === 'run' || conv.messages[i + 1]?.status === undefined)}
                  undelivered={lost.has(m.id)}
                  carried={softened.has(m.id)}
                  readOnly={readOnly}
                  sendBlocked={sendBlocked || locked || readOnly || streaming}
                  last={i >= lastUser && i >= conv.messages.length - 2}
                  action={action}
                  limits={limits}
                  saved={tokensSaved(conv.messages, i)}
                  onContinue={() => send(tr('app_continue_from_where_you_stopped'))}
                  onNewChat={startNew}
                  onResend={resend}
                /></div>{m.hostRun?.records.map(record => <ChatToolSteps key={record.id} record={record} live={live} host={host} connected={agentRuns.connected} />)}</Fragment>
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

        {install.suggest && !undo && !banner && !degraded && !readOnly && (
          <div className="toast install" role="status"><span>{tr('app_install_ask')}</span>
            <span className="toast-actions"><button className="ghost tiny" onClick={showInstall}>{tr('app_install_add')}</button>
              <button className="ghost tiny" onClick={dismissInstall}>{tr('app_install_not_now')}</button></span>
          </div>
        )}
        {banner && (
          <div className="banner" role="alert">
            <div>
              <strong>{banner.title}</strong> <span className="dim">{banner.detail}</span>
            </div>
            <div className="banner-actions">
              {banner.voice && waiting > 0 && <span className="dim tiny">{tr('app_try_again_in', { duration: waitText(waiting) })}</span>}
              {!banner.voice && banner.retryAfterS !== undefined && (
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

        {missingChats.has(conv.id) && <p className="image-notice" role="status">{tr('app_image_missing')}</p>}
        {imageWork.notice && <p className="attached-line bad image-notice" role="status">{imageWork.notice}</p>}
        <Composer
          imageMode={hostImages(me)?.model ? { on: imageMode, toggle: () => { setImageMode(!imageMode); setImageNotice(null); imageWork.setNotice(''); } } : undefined}
          key={conv.id}
          voice={hostAudio(me, 'transcriptions') ? { host, upload: recordVoice } : undefined}
          attachments={attached}
          onAttachments={setAttached}
          modelContext={me.host.upstream.model_context}
          storedBytes={retainedFileBytes(conv.messages, editingTurnId)}
          vision={vision}
          model={modelLabel(model)}
          notice={imageNotice}
          onNotice={setImageNotice}
          accepts={(attachments) => {
            const asking: Message = { id: 'draft', role: 'user', content: draft, ...attachmentFields(attachments) };
            const data = { ...imageData, ...Object.fromEntries(attachments.flatMap((a) => a.kind === 'image' ? [[a.image.id, a.image.data]] : [])) };
            return fitsRequest({ model, messages: carried([...conv.messages, asking], settings, me.host.upstream.model_context, asking, data).messages, temperature: settings.temperature, ...thinkingFields(settings.thinking) });
          }}
          ref={composer}
          text={draft}
          onText={setDraft}
          streaming={streaming}
          status={streaming && waitingForHeaders ? tr('app_waiting_for_model_load', { host: host || tr('app_the_host_lowercase'), model: modelLabel(model) }) : null}
          touch={touch}
          disabled={locked || readOnly}
          sendBlocked={sendBlocked || imageWork.pending.has('submit')}
          hint={
            live.offline ? tr('app_offline_hint') : live.key === 'paused'
              ? tr('app_send_will_work_again_the_moment_your_host_resumes')
              : locked || readOnly || sendBlocked || touch
                ? null
                : tr(ACCEPT(false) ? (vision ? 'app_hint_enter_sends_attach' : 'app_hint_enter_sends_files') : vision ? 'app_hint_enter_sends_images' : 'app_enter_sends_shift_enter_makes_a_new_line')
          }
          onSend={send}
          onStop={() => abort.current?.abort()}
        />
      </main>

      {imagesOpen && hostImages(me)?.model && <ImagesSheet jobs={imageWork.jobs} live={live} connected={agentRuns.connected && !sendBlocked} disabled={readOnly || locked || sendBlocked} pending={imageWork.pending} onClose={closeImages}
        onEdit={(text) => { closeImages(); setDraft(text); setImageMode(true); composer.current?.focus(); }}
        conversationTitle={(job) => convs.find((c) => c.messages.some((m) => m.kind === 'run' && m.job?.id === job.id))?.title ?? tr('app_untitled_chat')}
        onConversation={(job) => { const c = convs.find((c) => c.messages.some((m) => m.kind === 'run' && m.job?.id === job.id)); if (c) { setCurrentId(c.id); closeImages(); requestAnimationFrame(() => { const m = c.messages.find(m => m.kind !== 'run' && m.hostRun && (m.hostRun.ids.includes(job.input.parent_run_id ?? '') || m.hostRun.requestId === job.input.client_request_id)); document.getElementById(`turn-${m?.id ?? ''}`)?.scrollIntoView({ block: 'center' }); }); } else closeImages(); }}
        onCancel={(job) => { void imageWork.act(job); }} onDiscard={(job) => { void imageWork.act(job, true); }} />}
      {sheet && (
        <SettingsSheet
          settings={settings}
          onInstall={showInstall}
          models={models}
          live={live}
          thinks={settings.thinking !== 'default' || conv.messages.some((m) => Boolean(m.reasoning))}
          onChange={setSettings}
          onClose={() => setSheet(false)}
        />
      )}
      {iosInstall && <IOSInstallSheet invite={encodeInvite(live.addr, live.secret, live.invitePrefix)} onClose={closeIOS} />}
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

function Meters({ live, used, images, onOpen }: { live: Live; used: number | null; images: number; onOpen: () => void }) {
  const context = contextMeter(used, live.me.host.upstream.model_context);
  if (context && images) context.label = imageCopy(context.unknown ? 'app_meter_context_unknown_images' : 'app_meter_context_images', images, { used: compact(used ?? 0), limit: compact(live.me.host.upstream.model_context) });
  const all: MeterView[] = context ? [...meters(live), context] : meters(live);
  if (live.me.limits.daily_images !== undefined) { const limit = imageLimit(live.me.limits.daily_images, 20); all.push({ label: tr(limit < 0 ? 'app_job_meter_uncapped' : 'app_job_meter', { used: live.me.usage.today_images ?? 0, limit }), value: limit < 0 ? 0 : (live.me.usage.today_images ?? 0) / limit, unknown: !live.meOk }); }
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
        <p>{tr('app_limits_images')}</p>
        {ACCEPT(false) && <p>{tr('app_limits_files')}</p>}
        {(hostAudio(me, 'transcriptions') || hostAudio(me, 'speech')) && <p>{tr('app_limits_voice', { host: hostName(me) || tr('app_the_host_lowercase') })}</p>}
        {(me.agent || hostImages(me)?.model) && <p>{tr('app_limits_runs', { host: hostName(me) || tr('app_the_host_lowercase') })}</p>}
        {hostImages(me)?.model && <p>{tr('app_privacy_images', { days: hostImages(me)!.retention_days })}</p>}
        {hostImages(me)?.model && <p>{tr('app_limits_jobs', { host: hostName(me), days: hostImages(me)!.retention_days })}</p>}
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
  imageRetentionDays,
  engineDown,
  onPick,
}: {
  host: string;
  model: string;
  logging: boolean;
  imageRetentionDays?: number;
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
      <p className="dim">{privacy(host, logging, imageRetentionDays)}</p>
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
  imageMode,
  voice,
  attachments, onAttachments, vision, model, modelContext, storedBytes, notice, onNotice, accepts,
  text,
  onText,
  streaming,
  status,
  touch,
  disabled,
  sendBlocked = false,
  hint,
  onSend,
  onStop,
}: {
  ref: React.RefObject<HTMLTextAreaElement | null>;
  imageMode?: { on: boolean; toggle: () => void };
  voice?: { host: string; upload: (clip: Blob, signal: AbortSignal) => Promise<string> };
  attachments: Attachment[];
  onAttachments: (attachments: Attachment[]) => void;
  modelContext: number;
  storedBytes: number;
  vision: boolean;
  model: string;
  notice: AttachmentNotice | null;
  onNotice: (notice: AttachmentNotice | null) => void;
  accepts: (attachments: Attachment[]) => boolean;
  text: string;
  onText: (t: string) => void;
  streaming: boolean;
  status: string | null;
  touch: boolean;
  /** The invite is off, or this tab does not own the store: nothing can be sent from here. */
  disabled: boolean;
  /** Wait to send while keeping the draft editable (redial, or another temporary wait). */
  sendBlocked?: boolean;
  hint: string | null;
  onSend: (t: string) => void;
  onStop: () => void;
}) {

  const [recording, setRecording] = useState<RecordingState>({ kind: 'idle' });
  const latestVoice = useRef({ voice, text, onText, touch, disabled, sendBlocked, streaming });
  latestVoice.current = { voice, text, onText, touch, disabled, sendBlocked, streaming };
  const [recorder] = useState(() => new VoiceRecorder((clip, signal) => {
    const current = latestVoice.current;
    if (!current.voice || current.disabled || current.sendBlocked || current.streaming) return Promise.reject(new DOMException('Stopped', 'AbortError'));
    return current.voice.upload(clip, signal);
  }, (next) => {
    const current = latestVoice.current;
    if (next.kind === 'done' && (current.disabled || current.sendBlocked || current.streaming || !current.voice)) { setRecording({ kind: 'idle' }); return; }
    setRecording(next);
    if (next.kind === 'done') {
      current.onText(current.text + (current.text && next.text && !/\s$/.test(current.text) ? ' ' : '') + next.text);
      requestAnimationFrame(() => {
        const field = ref.current; if (!field) return;
        if (!current.touch) field.focus();
        field.setSelectionRange(field.value.length, field.value.length); field.scrollTop = field.scrollHeight;
      });
    }
  }));
  const hasVoice = Boolean(voice);
  useEffect(() => {
    const leave = () => recorder.release();
    const visibility = () => { if (document.hidden) leave(); };
    window.addEventListener('pagehide', leave); document.addEventListener('visibilitychange', visibility);
    return () => { window.removeEventListener('pagehide', leave); document.removeEventListener('visibilitychange', visibility); recorder.release(); };
  }, [recorder]);
  useEffect(() => {
    if (disabled || !hasVoice) recorder.release();
    else if ((sendBlocked || streaming) && ['requesting', 'recording', 'transcribing'].includes(recorder.state.kind)) recorder.cancel();
  }, [disabled, sendBlocked, streaming, hasVoice, recorder]);
  const toggleRecording = () => {
    if (disabled || sendBlocked || streaming || document.hidden) return;
    recorder.toggle();
  };
  const editText = (value: string) => { recorder.clear(); onText(value); };
  const sendText = () => { recorder.cancel(); onSend(text); };
  const voiceHint = voice && (recording.kind === 'blocked' ? tr('app_mic_blocked') : recording.kind === 'missing' ? tr('app_no_microphone') : null);
  const picker = useRef<HTMLInputElement>(null);
  const mounted = useRef(true);
  const queue = useRef(Promise.resolve());
  const current = useRef(attachments); current.current = attachments;
  const jobs = useRef(new Map<string, AbortController>());
  const [reading, setReading] = useState<ReadingAttachment[]>([]);
  const [over, setOver] = useState(false);
  useEffect(() => { mounted.current = true; const active = jobs.current; return () => { mounted.current = false; for (const ac of active.values()) ac.abort(); }; }, []);
  const update = (next: Attachment[]) => { current.current = next; onAttachments(next); };
  function attach(input: File[] | ClipboardData, fallback?: () => void) {
    if (disabled || streaming) return;
    const files = Array.isArray(input) ? input : Array.from(input.files);
    const inputs = files.length ? files.map((file) => ({ input: [file] as File[] | ClipboardData, name: file.name })) : [{ input, name: tr('f_paste') }];
    onNotice(null);
    for (const item of inputs) {
      const id = crypto.randomUUID(), controller = new AbortController();
      jobs.current.set(id, controller);
      setReading((prev) => [...prev, { id, name: item.name, controller }]);
      queue.current = queue.current.then(async () => {
        try {
          controller.signal.throwIfAborted();
          const added = await admit(item.input, controller.signal, { vision, model, modelContext, attachments: current.current, draft: text, storedBytes });
          if (!mounted.current || controller.signal.aborted) return;
          if (!added.length) { fallback?.(); return; }
          const next = [...current.current, ...added];
          if (!accepts(next)) { onNotice(rejectionNotice({ reason: 'body_too_large', message: tr('app_over_message_size', { size: '4 MB' }) })); return; }
          update(next);
        } catch (error) { if (mounted.current && !controller.signal.aborted) onNotice(rejectionNotice(error)); }
        finally { jobs.current.delete(id); if (mounted.current) setReading((prev) => prev.filter((r) => r.id !== id)); }
      });
    }
  }
  const data = Object.fromEntries(attachments.flatMap((a) => a.kind === 'image' ? [[a.image.id, a.image.data]] : []));
  const accept = ACCEPT(vision);
  const danger = notice?.danger === true;
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
    <div className="composer"
      onPaste={(e) => {
        const clipboard = e.clipboardData, pasted = clipboard.getData('text/plain');
        const files = Array.from(clipboard.files), start = ref.current?.selectionStart ?? text.length, end = ref.current?.selectionEnd ?? start;
        if (!files.length && (imageMode?.on || pasted.length < 4000)) return;
        e.preventDefault();
        const input = { files: clipboard.files, getData: () => pasted };
        attach(input, () => { editText(text.slice(0, start) + pasted + text.slice(end)); });
      }}
      onDragOver={(e) => { if (e.dataTransfer.types.includes('Files')) { e.preventDefault(); setOver(true); } }}
      onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setOver(false); }}
      onDrop={(e) => { e.preventDefault(); setOver(false); const files = Array.from(e.dataTransfer.files); if (files.length) void attach(files); }}>
      <AttachedImages attachments={attachments} data={data} reading={reading} onCancel={(id) => { jobs.current.get(id)?.abort(); setReading((prev) => prev.filter((r) => r.id !== id)); }} onRemove={(id) => { update(current.current.filter((a) => a.id !== id)); onNotice(null); }} />
      {imageMode?.on && <p className="attached-line">{tr(imagePrompts(text).length === 1 ? 'app_job_prompts_line_one' : 'app_job_prompts_line', { prompts: imagePrompts(text).length, images: imagePrompts(text).length })}</p>}
      {danger && <p className="attached-line bad image-notice" role="status">{notice?.message}</p>}
      {voice && <RecordingLine state={recording} host={voice.host} onCancel={() => recorder.cancel()} />}
      <div className={`composer-box ${over ? 'over' : ''}`}>
        <input ref={picker} type="file" accept={accept} multiple hidden onChange={(e) => { void attach(Array.from(e.target.files ?? [])); e.target.value = ''; }} />
        {accept !== '' && <button className="attach" aria-label={tr(ACCEPT(false) ? (vision ? 'app_attach' : 'app_attach_file') : 'app_attach_an_image')} onClick={() => { if (!disabled && !streaming) picker.current?.click(); }}>+</button>}
        {imageMode && <button className={`attach make ${imageMode.on ? 'on' : ''}`} aria-label={tr('app_job_make')} aria-pressed={imageMode.on} onClick={() => { if (!disabled && !streaming) imageMode.toggle(); }}><svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><path d="M2 2h16v16H2zM3 15l5-5 3 3 2-2 4 5" /><circle cx="13" cy="6" r="1.5" /></svg></button>}
        <textarea
          ref={ref}
          value={text}
          rows={1}
          aria-label={tr('app_message')}
          placeholder={tr(imageMode?.on ? 'app_job_placeholder' : voice ? 'app_type_or_speak' : 'app_message_the_host_s_model')}
          disabled={disabled}
          onChange={(e) => { editText(e.target.value); onNotice(null); }}
          onKeyDown={(e) => {
            // On a touch keyboard Return is the only way to make a new line, so Send is the only
            // way to send (014 promise 6). Enter mid-composition commits an IME candidate and must
            // not send either (007 promise 9).
            if (touch || e.key !== 'Enter' || e.shiftKey || composing(e)) return;
            e.preventDefault();
            if (!streaming && reading.length === 0 && !disabled && !sendBlocked && (text.trim() !== '' || attachments.length > 0)) sendText();
          }}
        />
        {voice && <button className={`attach mic ${recording.kind === 'recording' ? 'on' : ''}`} aria-pressed={recording.kind === 'recording'} aria-label={tr(recording.kind === 'recording' || recording.kind === 'requesting' ? 'app_mic_stop' : 'app_mic')}
          disabled={disabled || sendBlocked || streaming || recording.kind === 'transcribing'}
          onPointerDown={(event) => { if (event.button === 0) toggleRecording(); }}
          onPointerCancel={() => recorder.cancel()}
          onClick={(event) => { if (event.detail === 0) toggleRecording(); }}><svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="square" aria-hidden="true"><rect x="7" y="2" width="6" height="10" rx="3" /><path d="M4 9.5a6 6 0 0 0 12 0M10 15.5V18M7 18h6" /></svg></button>}
        {streaming ? (
          <button className="primary small" onClick={onStop}>
            {tr('app_stop')}
          </button>
        ) : (
          <button
            className="primary small"
            disabled={disabled || sendBlocked || reading.length > 0 || (text.trim() === '' && attachments.length === 0)}
            onClick={() => {
              sendText();
              ref.current?.focus();
            }}
          >
            {tr('app_send')}
          </button>
        )}
      </div>
      {(voiceHint || status || over || (!danger && notice?.message) || hint) && <p className={`hint ${notice && !danger ? 'image-notice' : ''}`} role={status || notice ? 'status' : undefined}>{voiceHint || status || (over ? tr('app_drop_to_attach') : (!danger && notice?.message) || hint)}</p>}
    </div>
  );
}

function RecordingLine({ state, host, onCancel }: { state: RecordingState; host: string; onCancel: () => void }) {
  if (state.kind === 'idle' || state.kind === 'blocked' || state.kind === 'missing') return null;
  const who = host || tr('app_the_host_lowercase');
  if (state.kind === 'requesting') return <div className="spoken" role="status"><span>{tr('app_voice_waiting')}</span><button className="ghost tiny" onClick={onCancel}>{tr('app_voice_cancel')}</button></div>;
  if (state.kind === 'error') return <p className="spoken bad" role="status">{tr('app_voice_failed', { host: who, seconds: Math.round(state.seconds), title: describeError(state.error, host).title })}</p>;
  if (state.kind === 'done') {
    const count = voiceWords(state.text);
    return <p className="spoken" role="status">{tr(count === 1 ? 'app_voice_transcribed_one' : 'app_voice_transcribed', { host: who, seconds: Math.round(state.seconds), count: count.toLocaleString('en-US') })}</p>;
  }
  return <div className="spoken" role="status"><i className="live" aria-hidden="true" />
    {state.kind === 'transcribing' ? <span>{tr('app_voice_transcribing', { host: who, seconds: Math.round(state.seconds) })}</span> : <>
      <span className="t">{voiceTime(state.kind === 'recording' ? state.seconds : 0)}</span>
      <svg className="waveform" viewBox="0 0 96 16" role="img" aria-label={tr('app_voice_waveform')}>
        <path d="M0 8H96" opacity=".3" />
        <path d={(state.kind === 'recording' ? state.waveform : []).map((level, i) => `M${i * 1.6 + .8} ${8 - level * 7}v${level * 14}`).join(' ')} />
      </svg></>}
    <button className="ghost tiny" onClick={onCancel}>{tr('app_voice_cancel')}</button></div>;
}

function SettingsSheet({
  settings,
  onInstall,
  models,
  live,
  thinks,
  onChange,
  onClose,
}: {
  settings: Settings;
  onInstall: () => void;
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
          {privacy(hostName(me), logsPrompts(me), hostImages(me)?.retention_days)} <Text name="app_settings_invite" values={{ name: <code>{me.key.name}</code>, id: me.key.id }} />{' '}
          {tr('app_settings_limits', { rpm: me.limits.rpm, daily: compact(me.limits.daily_tokens), concurrent: me.limits.max_concurrent, output: compact(me.limits.max_output_tokens) })}{' '}
          {tr('app_settings_engine', { engine: me.host.upstream.kind })}
          {me.host.upstream.model_context > 0 ? tr('app_settings_context', { context: compact(me.host.upstream.model_context) }) : ''}
          {hostImages(me)?.model && tr('app_settings_images', { model: hostImages(me)!.model })}
          {live.meOk && modelVision(me, chosen) === true ? tr('app_settings_vision') : ''}
          {live.meOk && !me.host.upstream.healthy ? tr('app_not_answering_right_now') : ''}.{' '}
          {hostAudio(me, 'transcriptions') && <>{tr('app_settings_hears', { model: hostAudio(me, 'transcriptions')! })}{' '}</>}
          {hostAudio(me, 'speech') && <>{tr('app_settings_speaks', { model: hostAudio(me, 'speech')! })}{' '}</>}
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
        <InstallSettings onInstall={onInstall} />
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

function InstallSettings({ onInstall }: { onInstall: () => void }) {
  const { path } = useInstall();
  if (!path) return null;
  const keys = {
    phone: 'app_install_settings_phone', ios: 'app_install_settings_phone', desktop: 'app_install_settings_desktop',
    safari: 'app_install_settings_safari_desktop', 'installed-phone': 'app_install_installed_phone', 'installed-desktop': 'app_install_installed_desktop',
  } as const;
  const action = path === 'phone' || path === 'ios' ? 'app_install_settings_action_phone' : path === 'desktop' ? 'app_install_settings_action_desktop' : null;
  return <p className="small-print install-settings">{tr(keys[path])}{action && <> <button className="ghost tiny act" onClick={onInstall}>{tr(action)}</button></>}</p>;
}
function IOSInstallSheet({ invite, onClose }: { invite: string; onClose: () => void }) {
  const ref = useRef<HTMLElement>(null);
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    ref.current?.focus();
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
      if (event.key === 'Tab') {
        const buttons = ref.current?.querySelectorAll('button');
        const first = buttons?.[0], last = buttons?.[buttons.length - 1];
        if (event.shiftKey && (document.activeElement === first || document.activeElement === ref.current)) { event.preventDefault(); last?.focus(); }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
      }
    };
    document.addEventListener('keydown', key);
    return () => { document.removeEventListener('keydown', key); previous?.focus(); };
  }, [onClose]);
  const text = (key: 'app_ios_step_2' | 'app_ios_step_3') => tr(key).split(/(\*\*[^*]+\*\*|⎙)/).map((part, i) => part === '⎙'
    ? <svg className="share" key={i} viewBox="0 0 14 16" aria-hidden="true"><path d="M4 6H1.5v8.5h11V6H10M7 10V1M4 4l3-3 3 3" fill="none" stroke="currentColor" strokeWidth="1.3" /></svg>
    : part.startsWith('**') ? <strong key={i}>{part.slice(2, -2)}</strong> : <span key={i}>{part}</span>);
  return <div className="sheet-wrap" onClick={onClose}><section ref={ref} className="sheet" role="dialog" aria-modal="true" aria-label={tr('app_ios_sheet_title')} tabIndex={-1} onClick={(e) => e.stopPropagation()}>
    <h3>{tr('app_ios_sheet_title')}</h3><p className="lead-line">{tr('app_ios_sheet_lead')}</p>
    <ol className="how">
      <li><span className="n">01</span><span>{tr('app_ios_step_1')}</span><span className="step-action"><button className="secondary small" onClick={() => { void navigator.clipboard?.writeText(invite).then(() => setCopied(true)).catch(() => setCopied(false)); }}>{tr(copied ? 'app_copied' : 'app_ios_copy_invite')}</button></span></li>
      <li><span className="n">02</span><span>{text('app_ios_step_2')}<br />{tr('app_ios_step_2_older')}</span></li>
      <li><span className="n">03</span><span>{text('app_ios_step_3')}</span></li>
    </ol><div className="sheet-actions"><button className="primary small" onClick={onClose}>{tr('app_got_it')}</button></div>
  </section></div>;
}
