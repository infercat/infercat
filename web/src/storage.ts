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
  // Never read directly: conversations and settings belong to one host, so they live under
  // `<key>.<scope>` (see hostScope). The bare names are the pre-scope layout, cleared once.
  conversations: 'bn.conversations',
  settings: 'bn.settings',
} as const;

/** Where one host's conversations and settings live. `scope` comes from hostScope(). */
export function scopedKeys(scope: string): { conversations: string; settings: string } {
  return { conversations: `${KEYS.conversations}.${scope}`, settings: `${KEYS.settings}.${scope}` };
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
  return { id: newId(), title: 'New chat', createdAt: now, updatedAt: now, messages: [] };
}

/** The first user message becomes the sidebar title. */
export function titleFrom(text: string): string {
  const line = text.trim().split('\n')[0] ?? '';
  return line.length > 40 ? `${line.slice(0, 40).trimEnd()}…` : line || 'New chat';
}

/** Drops empty conversations and caps history so localStorage never becomes the bottleneck. */
export function prune(convs: Conversation[], keep = 50): Conversation[] {
  return convs.filter((c) => c.messages.length > 0).slice(0, keep);
}
