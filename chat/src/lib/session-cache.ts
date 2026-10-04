// Client-side cache of the conversation, so reopening the chat shows the
// transcript at once and only asks the server for what changed since.
//
// IndexedDB rather than localStorage: long sessions exceed localStorage's
// ~5 MB quota, and localStorage reads block the page while they parse.

export interface CachedSession<M, R> {
  // Server conversation state the cache belongs to (session_sync.epoch).
  epoch: string;
  // Highest change sequence applied; sent back as /events?since=.
  seq: number;
  messages: M[];
  richMessages: R[];
  savedAt: number;
}

const DB_NAME = "agentapi-chat";
const STORE = "sessions";

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1);
    request.onupgradeneeded = () => request.result.createObjectStore(STORE);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

function withStore<T>(mode: IDBTransactionMode, run: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return openDb().then((db) => new Promise<T>((resolve, reject) => {
    const tx = db.transaction(STORE, mode);
    const request = run(tx.objectStore(STORE));
    tx.oncomplete = () => { db.close(); resolve(request.result); };
    tx.onabort = tx.onerror = () => { db.close(); reject(tx.error); };
  }));
}

// Loads the cached session for key, or null when there is none or storage is
// unavailable (private mode, blocked site data). Never throws.
export async function loadSession<M, R>(key: string): Promise<CachedSession<M, R> | null> {
  try {
    if (typeof indexedDB === "undefined") return null;
    const value = await withStore<CachedSession<M, R> | undefined>("readonly", (store) => store.get(key));
    return value && typeof value.epoch === "string" && Array.isArray(value.messages) ? value : null;
  } catch {
    return null;
  }
}

export async function saveSession<M, R>(key: string, session: CachedSession<M, R>): Promise<void> {
  try {
    if (typeof indexedDB === "undefined") return;
    await withStore("readwrite", (store) => store.put(session, key));
  } catch {
    // The cache only speeds up the next load; losing it is harmless.
  }
}

export async function clearSession(key: string): Promise<void> {
  try {
    if (typeof indexedDB === "undefined") return;
    await withStore("readwrite", (store) => store.delete(key));
  } catch {
    // See saveSession.
  }
}

// Applies a batch of upserts keyed by keyOf in one pass: existing entries are
// replaced in place, new ones appended in arrival order. Replaying a long
// session is thousands of updates; doing a copy and a linear search per
// update made reloads quadratic.
export function mergeByKey<T>(previous: T[], updates: Iterable<T>, keyOf: (item: T) => string | undefined): T[] {
  const next = previous.slice();
  const index = new Map<string, number>();
  next.forEach((item, i) => {
    const key = keyOf(item);
    if (key !== undefined) index.set(key, i);
  });
  for (const item of updates) {
    const key = keyOf(item);
    const at = key === undefined ? undefined : index.get(key);
    if (at === undefined) {
      if (key !== undefined) index.set(key, next.length);
      next.push(item);
    } else {
      next[at] = item;
    }
  }
  return next;
}
