// Client-side cache of the conversation, so reopening the chat shows the
// transcript at once and only asks the server for what changed since.
//
// IndexedDB rather than localStorage: long sessions exceed localStorage's
// ~5 MB quota, and localStorage reads block the page while they parse.

import {normalizeRichMessage, type Message, type RichMessage, type RichMessagePayload} from "./conversation";
import {normalizeRequest, type NativeRequest, type RequestPayload} from "./native-requests";

export interface CachedSession {
  // Server conversation state the cache belongs to (session_sync.epoch).
  epoch: string;
  // Highest change sequence applied; sent back as /events?since=.
  seq: number;
  messages: Message[];
  richMessages: RichMessage[];
  // Requests the agent made (native transport). Missing in caches written
  // before requests existed.
  requests?: NativeRequest[];
  // How the server drives the agent ("pty", "acp", "native"), so the cached
  // transcript renders for the right transport before the server says.
  transport?: string;
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
export async function loadSession(key: string): Promise<(CachedSession & {requests: NativeRequest[]}) | null> {
  try {
    if (typeof indexedDB === "undefined") return null;
    return parseCachedSession(await withStore<unknown>("readonly", (store) => store.get(key)));
  } catch {
    return null;
  }
}

// parseCachedSession checks what the cache held, and normalizes it as the
// events are normalized when they arrive: a cache written by an earlier
// version can hold a rich message with null content, which the transcript
// can't show. A cache written before requests existed loads with none.
export function parseCachedSession(value: unknown): (CachedSession & {requests: NativeRequest[]}) | null {
  if (!value || typeof value !== "object") return null;
  const session = value as Partial<Record<keyof CachedSession, unknown>>;
  if (typeof session.epoch !== "string" || typeof session.seq !== "number" || !Array.isArray(session.messages)) return null;
  return {
    ...(value as CachedSession),
    messages: items<Message>(session.messages),
    richMessages: items<RichMessagePayload>(session.richMessages).map(normalizeRichMessage),
    requests: items<RequestPayload>(session.requests).map(normalizeRequest),
  };
}

// The objects in list, or none when it isn't one.
function items<T>(list: unknown): T[] {
  return Array.isArray(list) ? list.filter((item): item is T => typeof item === "object" && item !== null) : [];
}

export async function saveSession(key: string, session: CachedSession): Promise<void> {
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
