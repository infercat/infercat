// Conversations, messages, settings, and the localStorage they survive in. Every access is behind
// try/catch: a browser with storage disabled loses history but must still chat.

export interface Message {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  /** reasoning_content deltas, kept separate so the Thinking block can collapse. */
  reasoning?: string;
  model?: string;
  tokens?: { in: number; out: number };
  /** Set when the reply failed; the bubble shows this instead of empty content. */
  error?: string;
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
  conversations: 'bn.conversations',
  settings: 'bn.settings',
} as const;

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
