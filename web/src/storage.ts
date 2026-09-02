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
  return m.role !== 'assistant' || (m.status !== 'interrupted' && m.status !== 'no_answer');
}

export interface Message {
  id: string;
  role: 'user' | 'assistant';
  content: string;
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
  /**
   * The pending turn (014 promise 1): a user message that was sent and never answered. It stays in
   * the thread, marked, with a Try again — so a host that was asleep costs the reader their wait,
   * never their words.
   */
  pending?: boolean;
}

export interface Conversation {
  id: string;
  title: string;
  createdAt: number;
  updatedAt: number;
  messages: Message[];
}

export interface Settings {
  model: string | null;
  systemPrompt: string;
  temperature: number;
}

export const DEFAULT_SETTINGS: Settings = { model: null, systemPrompt: '', temperature: 0.7 };

export const KEYS = {
  invite: 'bn.invite',
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
}

/**
 * A stable, non-secret name for "this host, this invite": the tunnel address (public — it is the
 * address, not the secret) and the key id, hashed to keep the storage key short. Two hosts, or the
 * same host with two invites, never see each other's conversations or settings.
 */
export function hostScope(addr: string, keyId: string): string {
  let h = 0x811c9dc5;
  for (const ch of `${addr} ${keyId}`) {
    h ^= ch.codePointAt(0) ?? 0;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h.toString(36);
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
  return { id: newId(), title: 'Untitled chat', createdAt: now, updatedAt: now, messages: [] };
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
 * What comes back is exactly what is on disk, with one repair: a "Not delivered" mark under a turn
 * that has its answer is cleared (settlePending). A reply still streaming is left as it is — the
 * tab writing it may be alive; only the leader, on taking the store, calls reopenChats().
 */
export function loadChats(scope: string): Conversation[] {
  const keys = scopedKeys(scope);
  migrateChats(scope);
  const out: Conversation[] = [];
  for (const id of load<string[]>(keys.index, [])) {
    const c = load<Conversation | null>(keys.conv(id), null);
    if (c && Array.isArray(c.messages)) out.push({ ...c, messages: settlePending(c.messages) });
  }
  return out;
}

/**
 * The pending mark is per turn (020 promise 1): send() marks exactly the turn it sends, and this
 * is the one rule that clears it — a user turn whose next message is a delivered reply was
 * delivered. Applied after every stream ends and on every load, so nothing persisted can say
 * "Not delivered" under an answer, whichever release wrote it.
 */
export function settlePending(messages: Message[]): Message[] {
  if (!messages.some((m, i) => m.pending === true && delivered(messages[i + 1]))) return messages;
  return messages.map((m, i) => (m.pending === true && delivered(messages[i + 1]) ? { ...m, pending: false } : m));
}

/** A reply that answered its turn: the host produced something and said so, or the reader stopped it. */
export function delivered(m: Message | undefined): boolean {
  return m?.role === 'assistant' && (m.status === 'complete' || m.status === 'stopped');
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
  return convs.map((c) =>
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
                  note: 'This reply was still arriving when its tab was closed or reloaded — what is above is only part of it.',
                }
              : m,
          ),
        }
      : c,
  );
}

function unfinished(m: Message): boolean {
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
