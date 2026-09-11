import type { ImageJob } from './imageJobs';
import type { RunRecord } from './api';
import type { AttachedFile, AttachmentRef } from './attachments';
import type { ImageMeta } from './images';
import { tr } from './i18n/text';
// Conversations, messages, settings, and the localStorage they survive in. Every access is behind
// try/catch: a browser with storage disabled loses history but must still chat.

/**
 * How a reply ended, decided by the host and never by the absence of more tokens:
 * `complete` the host said it was done · `stopped` the reader pressed Stop ·
 * `interrupted` the stream broke or errored part way · `no_answer` it ended cleanly with no answer
 * in it (thinking only, or nothing at all). Undefined means still streaming.
 */
export type MessageStatus = 'complete' | 'stopped' | 'interrupted' | 'no_answer';

/** A turn that did not produce an answer is not context for the next one. */
export function isAnswer(m: Message): boolean {
  if (m.kind === 'run') return m.run?.state === 'done';
  return m.role !== 'assistant' || (m.status !== 'interrupted' && m.status !== 'no_answer');
}

export interface ChatItem extends MessageFields { kind?: 'message'; hostRun?: { keyId: string; requestId: string; ids: string[]; records: RunRecord[] } }
export interface RunItem extends MessageFields {
  kind: 'run'; role: 'assistant';
  runKind?: 'image';
  job?: ImageJob;
  run?: RunRecord;
  remoteId?: string;
  clientRequestId?: string;
  keyId?: string;
  submission?: 'pending' | 'uncertain' | 'refused';
}
export type Message = ChatItem | RunItem;
interface MessageFields {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  images?: ImageMeta[];
  files?: AttachedFile[];
  attachmentOrder?: AttachmentRef[];
  /** Number of images actually carried by this reply’s request. */
  imageCount?: number;
  /** reasoning_content deltas, kept separate so the Thinking block can collapse. */
  reasoning?: string;
  model?: string;
  tokens?: { in: number; out: number };
  status?: MessageStatus;
  /** One short line under the message explaining a status that is not `complete`. */
  note?: string;
  /** The host's own diagnostic. Never primary copy: it lives behind a Details disclosure. */
  details?: string;
  /** Nothing has come back yet and it has been long enough to say so (assistant, mid-stream). */
  waiting?: boolean;
  /** The host has the request and every slot is taken: in line, not absent (assistant, mid-stream; 018). */
  queued?: boolean;
  /** The reply stopped at the invite's output cap, not because it had finished (promise 14). */
  capped?: boolean;
  /** The answer this one replaced, kept rather than thrown away when a turn is edited (promise 16). */
  previous?: string;
  /** The thinking switch this reply was asked for, when it was not the model's default (031). */
  thinking?: 'on' | 'off';
  /** When this reply was asked for and when its tokens arrived, by this device's clock (032). */
  timing?: Timing;
}

/**
 * Milliseconds since the epoch on the reader's device: when the request was sent, when the first
 * and the latest token (thinking or answer) arrived, and — when the host said the request was in
 * line for a slot — the last time it said so. Raw moments rather than derived numbers, so what is
 * said about them can change without rewriting a chat.
 */
export interface Timing {
  sent: number;
  first?: number;
  last?: number;
  queued?: number;
}

export interface Conversation {
  id: string;
  title: string;
  createdAt: number;
  updatedAt: number;
  messages: Message[];
}

/** Whether the model is asked to think (031): its own default, on, or off. */
export type Thinking = 'default' | 'on' | 'off';

export interface Settings {
  model: string | null;
  systemPrompt: string;
  temperature: number;
  thinking: Thinking;
}

export const DEFAULT_SETTINGS: Settings = { model: null, systemPrompt: '', temperature: 0.7, thinking: 'default' };

export const KEYS = {
  invite: 'bn.invite',
  language: 'bn.language',
  privateKey: 'bn.privateKey',
  /** What the connect screen can say about the last host before it has reconnected to it. */
  lastHost: 'bn.lastHost',
  // Never read directly: conversations and settings belong to one host, so they live under
  // `<key>.<scope>` (see hostScope). The bare names are the pre-scope layout, cleared once.
  conversations: 'bn.conversations',
  settings: 'bn.settings',
  me: 'bn.me',
} as const;

/**
 * Where one host's conversations and settings live. `scope` comes from hostScope().
 *
 * One conversation is one key (014 promise 4). The old single-array layout meant every write
 * rewrote the whole history, so two tabs open on the same host silently overwrote each other's
 * last exchange: whoever saved second won with a list that never had the other's turn in it. With
 * a key per conversation a second tab can only ever clobber the *same* conversation, and the
 * `storage` event (chatsChanged) lets it re-read before it writes.
 */
export function scopedKeys(scope: string): {
  index: string;
  settings: string;
  /** The last /me any tab of this browser got for this host, so two tabs' meters never disagree (020 promise 6). */
  me: string;
  conv: (id: string) => string;
} {
  return {
    index: `${KEYS.conversations}.${scope}`,
    settings: `${KEYS.settings}.${scope}`,
    me: `${KEYS.me}.${scope}`,
    conv: (id: string) => `${KEYS.conversations}.${scope}.${id}`,
  };
}

/** What the connect screen remembers about the host this browser last used. Never the secret. */
export interface LastHost {
  name: string;
  scope: string;
  /** The reader pressed Disconnect: the next visit shows the card and waits (022 promise 5). */
  left?: boolean;
}

/**
 * Disconnect is a choice, and a reload must not undo it (022 promise 5): the remembered code only
 * dials by itself when the last thing the reader did with it was connect. Written on Disconnect;
 * a successful verify writes the record afresh, without the flag.
 */
export function rememberLeft(): void {
  const h = load<LastHost | null>(KEYS.lastHost, null);
  if (h) save(KEYS.lastHost, { ...h, left: true });
}

/** Whether a code this browser holds dials on arrival: a link is consent; a return visit is, unless the reader left. */
export function dialsOnArrival(last: LastHost | null, viaLink: boolean): boolean {
  return viaLink || last?.left !== true;
}

/**
 * A stable, non-secret name for "this host": the tunnel address (public — it is the address, not
 * the secret), hashed to keep the storage key short. Two hosts never see each other's conversations
 * or settings; a new code from the same host — a revoke and a re-issue, a rotation — opens the same
 * drawer (024 promise 1, ruling: the host scope replaces the invite scope).
 */
export function hostScope(addr: string): string {
  return fnv(addr);
}

/** Where a build before 024 kept this host's chats: the address and the key id together. */
function inviteScope(addr: string, keyId: string): string {
  return fnv(`${addr} ${keyId}`);
}

function fnv(text: string): string {
  let h = 0x811c9dc5;
  for (const ch of text) {
    h ^= ch.codePointAt(0) ?? 0;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h.toString(36);
}

/**
 * Moves what an earlier build kept under the invite scope into the host scope, once (024 promise
 * 1): the conversations (merged by id, the invite scope's order first, nothing duplicated), the
 * settings and the shared /me when the host scope has none. The invite-scoped keys are then gone,
 * so this runs exactly once per invite. Safe to call on every mount.
 */
export function adoptInviteScope(addr: string, keyId: string): void {
  const from = scopedKeys(inviteScope(addr, keyId));
  const to = scopedKeys(hostScope(addr));
  const ids = load<string[]>(from.index, []);
  if (ids.length === 0 && load(from.settings, null) === null) return;
  const have = new Set(load<string[]>(to.index, []));
  const moved: string[] = [];
  for (const id of ids) {
    const c = load<Conversation | null>(from.conv(id), null);
    if (c && !have.has(id)) {
      save(to.conv(id), c);
      moved.push(id);
    }
    forget(from.conv(id));
  }
  if (moved.length > 0) save(to.index, [...moved, ...load<string[]>(to.index, [])].slice(0, 50));
  if (load(to.settings, null) === null) {
    const settings = load(from.settings, null);
    if (settings !== null) save(to.settings, settings);
  }
  forget(from.index, from.settings, from.me);
}

/** The pre-scope keys held one host's history under a global name; it is nobody's now. */
export function dropLegacyHistory(): void {
  forget(KEYS.conversations, KEYS.settings);
}

/** A model the previous host shared is not a model this one has. */
export function modelFor(stored: string | null, models: string[]): string | null {
  return stored !== null && models.includes(stored) ? stored : null;
}

export function load<T>(key: string, fallback: T): T {
  try {
    const raw = globalThis.localStorage?.getItem(key);
    return raw === null || raw === undefined ? fallback : (JSON.parse(raw) as T);
  } catch {
    return fallback;
  }
}

export function save(key: string, value: unknown): void {
  try {
    globalThis.localStorage?.setItem(key, JSON.stringify(value));
  } catch {
    /* private mode, quota, or storage disabled: history is a convenience, not the product */
  }
}

export function forget(...keys: string[]): void {
  for (const key of keys) {
    try {
      globalThis.localStorage?.removeItem(key);
    } catch {
      /* nothing to do */
    }
  }
}

export function newId(): string {
  const c = globalThis.crypto;
  if (c && 'randomUUID' in c) return c.randomUUID().slice(0, 8);
  return Math.random().toString(36).slice(2, 10);
}

export function newConversation(): Conversation {
  const now = Date.now();
  // Not "New chat": that is the button that makes one, and a list where every row is named after
  // the button is a list that says nothing (014 promise 17).
  return { id: newId(), title: tr('app_untitled_chat'), createdAt: now, updatedAt: now, messages: [] };
}

/** The first user message becomes the sidebar title. */
export function titleFrom(text: string): string {
  const line = text.trim().split('\n')[0] ?? '';
  return line.length > 40 ? `${line.slice(0, 40).trimEnd()}…` : line || 'Untitled chat';
}

/** Drops empty conversations and caps history so localStorage never becomes the bottleneck. */
export function prune(convs: Conversation[], keep = 50): Conversation[] {
  return convs.filter((c) => c.messages.length > 0).slice(0, keep);
}

// ---- one conversation, one key (014 promise 4) ------------------------------------------------

/**
 * Reads this host's conversations, newest first. The index holds the order and nothing else that
 * matters, so a conversation another tab wrote after our index was read is still found (it is in
 * the index the moment it exists) and one another tab deleted simply is not there.
 *
 * What comes back is exactly what is on disk. A reply still streaming is left as it is — the tab
 * writing it may be alive; only the leader, on taking the store, calls reopenChats().
 */
export function loadChats(scope: string): Conversation[] {
  const keys = scopedKeys(scope);
  migrateChats(scope);
  const out: Conversation[] = [];
  for (const id of load<string[]>(keys.index, [])) {
    const c = load<Conversation | null>(keys.conv(id), null);
    if (c && Array.isArray(c.messages)) out.push(c);
  }
  return out;
}

/**
 * The user turns no successful request has carried (022 promise 2). Every request sends the whole
 * thread before it, so a turn is delivered the moment any later reply is — its own, a Try again, or
 * the reply to a later message whose history holds it (the model that recalls ZEBRA was told
 * ZEBRA). One derivation over the thread and no flag on the turn, so what is shown, what is sent
 * and what is persisted cannot disagree, whichever release wrote the transcript.
 */
export function undelivered(messages: readonly Message[]): Set<string> {
  const out = new Set<string>();
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i] as Message;
    if (delivered(m)) break;
    if (m.role === 'user') out.add(m.id);
  }
  return out;
}

/**
 * The failed replies whose turn a later request carried anyway (024 promise 3): the same
 * derivation that clears the bubble's mark, read for the row under it — "Not sent" stops being the
 * last word once the next question has carried the turn.
 */
export function carriedAfter(messages: readonly Message[]): Set<string> {
  const lost = undelivered(messages);
  const out = new Set<string>();
  let turn: Message | null = null;
  for (const m of messages) {
    if (m.role === 'user') turn = m;
    else if (m.status === 'interrupted' && turn && !lost.has(turn.id)) out.add(m.id);
  }
  return out;
}

/**
 * A request that got through: the host ended the reply itself (with an answer in it or not — a
 * model that only thought was still asked), or the reader stopped it. Only a reply the transport
 * or the host cut short is not a delivery.
 */
export function delivered(m: Message | undefined): boolean {
  if (m?.kind === 'run') return Boolean(m.run || m.job);
  return m?.role === 'assistant' && m.status !== undefined && m.status !== 'interrupted';
}

/**
 * Writes one conversation and makes sure the index names it. The index is re-read here rather than
 * held in memory: another tab may have added a chat since this tab loaded, and an index written
 * from a stale copy is exactly how the last exchange used to disappear.
 */
export function saveChat(scope: string, conv: Conversation): void {
  const keys = scopedKeys(scope);
  if (conv.messages.length === 0) return; // an empty draft is not history
  save(keys.conv(conv.id), conv);
  const ids = load<string[]>(keys.index, []).filter((id) => id !== conv.id);
  ids.unshift(conv.id);
  const kept = ids.slice(0, 50);
  save(keys.index, kept);
  for (const id of ids.slice(50)) forget(keys.conv(id));
}

export function deleteChat(scope: string, id: string): void {
  const keys = scopedKeys(scope);
  save(keys.index, load<string[]>(keys.index, []).filter((x) => x !== id));
  forget(keys.conv(id));
}

/** How many chats this browser holds for a host — for the welcome-back screen (014 promise 9). */
export function countChats(scope: string): number {
  return load<string[]>(scopedKeys(scope).index, []).length;
}

/**
 * Merges what is on disk into what this tab has in memory, per conversation, newest write wins.
 * Called when another tab fires a `storage` event for this host, so this tab re-reads before its
 * next write instead of overwriting a turn it never saw.
 */
export function mergeChats(scope: string, mine: Conversation[]): Conversation[] {
  const stored = loadChats(scope);
  const byId = new Map(mine.map((c) => [c.id, c]));
  const out: Conversation[] = [];
  for (const theirs of stored) {
    const ours = byId.get(theirs.id);
    out.push(ours && ours.updatedAt >= theirs.updatedAt ? ours : theirs);
    byId.delete(theirs.id);
  }
  // Anything only this tab has (a chat being written right now) keeps its place at the front.
  return [...byId.values(), ...out];
}

/** True when a `storage` event was about this host's chats. */
export function chatsChanged(scope: string, key: string | null): boolean {
  return key !== null && key.startsWith(`${KEYS.conversations}.${scope}`);
}

/**
 * A reply still marked `streaming` in storage when this tab takes the store was cut off by the tab
 * writing it going away — a reload, a closed tab, a hand-over — not by the host (folded in from
 * 013). It keeps every token it had and says what happened; it never comes back as a reply that
 * looks finished, and it is not sent as context.
 */
export function reopenChats(convs: Conversation[]): Conversation[] {
  const recovered = convs.map((c) => ({ ...c, messages: c.messages.map((m) => m.kind === 'run' && m.submission === 'pending' ? { ...m, submission: 'uncertain' as const } : m) }));
  return recovered.map((c) =>
    c.messages.some(unfinished)
      ? {
          ...c,
          messages: c.messages.map((m) =>
            unfinished(m)
              ? {
                  ...m,
                  waiting: false,
                  queued: false,
                  status: 'interrupted' as const,
                  note: tr('app_this_reply_was_still_arriving_when_its_tab_was'),
                }
              : m,
          ),
        }
      : c,
  );
}

function unfinished(m: Message): boolean {
  if (m.kind === 'run') return false;
  return m.role === 'assistant' && m.status === undefined;
}

// ---- one tab writes (020 promise 6) --------------------------------------------------------------

/**
 * The conversation store has one writer per host: the tab holding this Web Lock. It is the same
 * election the tunnel identity uses (transport/index.ts), with two more moves — a follower waits in
 * line, so it becomes the leader the moment the leader's tab closes, and "Use this tab instead"
 * takes the lock by force. `onRole(true)` is called each time this tab holds the lock, `onRole(false)`
 * each time it does not, including when it is taken away; the first call is the answer to "am I
 * the leader now". Without Web Locks (an old browser, a file: URL) there is one tab and it leads.
 */
export function electStore(scope: string, onRole: (leader: boolean) => void): { takeOver(): void; release(): void } {
  const name = `bn.store.${scope}`;
  const locks = (globalThis.navigator as Navigator | undefined)?.locks;
  if (!locks) {
    onRole(true);
    return { takeOver: () => {}, release: () => {} };
  }
  let released = false;
  let free: () => void = () => {};
  const holding = new Promise<void>((resolve) => (free = resolve));
  // Holding the lock means the callback's promise is still pending; release() settles it.
  const hold = (lock: Lock | null): Promise<void> | undefined => {
    if (lock === null) return undefined;
    if (released) return Promise.resolve();
    onRole(true);
    return holding;
  };
  const lost = (): void => {
    if (!released) onRole(false);
  };
  void locks
    .request(name, { ifAvailable: true }, (lock) => {
      if (lock === null) {
        onRole(false);
        // Next in line: when the leader's tab goes, this one leads without anyone asking.
        void locks.request(name, hold).catch(lost);
      }
      return hold(lock);
    })
    .catch(lost);
  return {
    takeOver: () => {
      void locks.request(name, { steal: true }, hold).catch(lost);
    },
    release: () => {
      released = true;
      free();
    },
  };
}

/** The pre-014 layout kept one host's whole history in a single key. Split it once, then drop it. */
function migrateChats(scope: string): void {
  const keys = scopedKeys(scope);
  const old = load<Conversation[] | null>(keys.index, null);
  if (!Array.isArray(old) || old.length === 0 || typeof old[0] === 'string') return;
  save(keys.index, []); // the index is ids from here on; saveChat must not read the old shape
  for (const c of [...old].reverse()) if (c?.id) saveChat(scope, c);
}
