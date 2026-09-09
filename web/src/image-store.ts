import { dataURL, type ImageData, type ImageMeta, type PreparedImage } from './images';

export const IMAGE_STORE_BYTES = 20 * 1024 * 1024;
interface StoredTurn { key: string; scope: string; messageId: string; at: number; images: { id: string; blob: Blob }[] }
function openStore(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open('infercat-images', 1);
    req.onupgradeneeded = () => req.result.createObjectStore('turns', { keyPath: 'key' }).createIndex('scope', 'scope');
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
    req.onblocked = () => reject(new Error('Image store blocked'));
  });
}
/** The oldest sent turns go first; reads never refresh their age. */
export function evictedTurns(turns: readonly { key: string; at: number; bytes: number }[], cap = IMAGE_STORE_BYTES): string[] {
  let bytes = turns.reduce((n, t) => n + t.bytes, 0);
  const remove: string[] = [];
  for (const t of [...turns].sort((a, b) => a.at - b.at || a.key.localeCompare(b.key))) {
    if (bytes <= cap) break;
    remove.push(t.key); bytes -= t.bytes;
  }
  return remove;
}
/** One transaction serializes both the replacement and the per-host eviction across tabs. */
export async function storeImages(scope: string, messageId: string, images: readonly PreparedImage[]): Promise<void> {
  let db: IDBDatabase | undefined;
  try {
    db = await openStore();
    await new Promise<void>((resolve, reject) => {
      const tx = db!.transaction('turns', 'readwrite');
      const store = tx.objectStore('turns');
      const key = `${scope}:${messageId}`;
      const next: StoredTurn = { key, scope, messageId, at: Date.now(), images: images.map(({ id, blob }) => ({ id, blob })) };
      const req = store.index('scope').getAll(scope);
      req.onsuccess = () => {
        const turns = (req.result as StoredTurn[]).filter((t) => t.key !== key);
        // Transactions serialize insertion order even when two writes share a clock tick.
        next.at = Math.max(next.at, ...turns.map((t) => t.at + 1));
        if (images.length) { turns.push(next); store.put(next); } else store.delete(key);
        for (const old of evictedTurns(turns.map((t) => ({ key: t.key, at: t.at, bytes: t.images.reduce((n, i) => n + i.blob.size, 0) })))) store.delete(old);
      };
      tx.oncomplete = () => resolve();
      tx.onabort = () => reject(tx.error);
      tx.onerror = () => reject(tx.error);
    });
  } catch { /* Storage is optional: current prepared bytes can still be sent; a reload shows placeholders. */ }
  finally { db?.close(); }
}
export async function readImages(scope: string, messages: readonly { id: string; images?: readonly ImageMeta[] }[]): Promise<ImageData> {
  if (!messages.some((m) => m.images?.length)) return {};
  let db: IDBDatabase | undefined;
  try {
    db = await openStore();
    const turns = await new Promise<StoredTurn[]>((resolve, reject) => {
      const req = db!.transaction('turns').objectStore('turns').index('scope').getAll(scope);
      req.onsuccess = () => resolve(req.result as StoredTurn[]);
      req.onerror = () => reject(req.error);
    });
    const wanted = new Set(messages.flatMap((m) => m.images?.map((i) => i.id) ?? []));
    const entries = await Promise.all(turns.flatMap((t) => t.images.filter((i) => wanted.has(i.id))).map(async (i) => [i.id, await dataURL(i.blob)] as const));
    return Object.fromEntries(entries);
  } catch { return {}; }
  finally { db?.close(); }
}
/** Reuse already downscaled bytes when editing a turn; never re-encode a JPEG. */
export async function preparedFrom(images: readonly ImageMeta[], data: ImageData): Promise<PreparedImage[]> {
  return Promise.all(images.filter((i) => data[i.id]).map(async (i) => ({ ...i, data: data[i.id]!, blob: await (await fetch(data[i.id]!)).blob() })));
}
