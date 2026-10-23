// Client-side cache of the conversation, so reopening the chat shows the
// transcript at once and only asks the server for what changed since.
//
// IndexedDB rather than localStorage: long sessions exceed localStorage's
// ~5 MB quota, and localStorage reads block the page while they parse.
//
// Each message is a record of its own, by its place in its list, and a
// save writes only the records whose message changed since the last save
// (the chat keeps a message's object until it changes): a streaming turn
// rewrites its last messages, not the whole transcript. A cache not used
// for CACHE_TTL_MS goes, and so do the least recently used beyond
// MAX_CACHED_SERVERS; each server has one cached conversation, which a new
// one (another epoch) replaces.

import {normalizeRichMessage, type Message, type RichMessage, type RichMessagePayload} from "./conversation";
import {normalizeRequest, type NativeRequest, type RequestPayload} from "./native-requests";
import {normalizeTask, type ServerTask} from "./server-tasks";
import type {components} from "./api-schema";

export interface CachedSession {
  // Server conversation state the cache belongs to (session_sync.epoch).
  epoch: string;
  // Highest change sequence applied; sent back as /events?since=.
  seq: number;
  messages: Message[];
  richMessages: RichMessage[];
  // Requests the agent made (native transport).
  requests: NativeRequest[];
  // The tasks as the server derives them; none from servers that don't.
  tasks: ServerTask[];
  // How the server drives the agent ("pty", "acp", "native"), so the cached
  // transcript renders for the right transport before the server says.
  transport?: string;
}

// How long a cache is kept after it was last used (loaded or saved).
export const CACHE_TTL_MS = 14 * 24 * 60 * 60 * 1000;
// How many servers' conversations are kept, the most recently used.
export const MAX_CACHED_SERVERS = 10;

const KINDS = ["messages", "richMessages", "requests", "tasks"] as const;
type Kind = (typeof KINDS)[number];
type Lists = Record<Kind, readonly unknown[]>;

// What is kept of a server's cache besides its records.
export interface CacheMeta {
  epoch: string;
  seq: number;
  transport?: string;
  // When the cache was last loaded or saved.
  usedAt: number;
  // How many records of each kind there are.
  counts: Record<Kind, number>;
  // Who wrote the records last (a page, and its write): a page that didn't
  // can't know what the records hold, and writes them all.
  writer: string;
}

// A change to a server's cache: its meta, and the records to put. Records
// past meta.counts go.
export interface CacheWrite {
  meta: CacheMeta;
  put: {kind: Kind; index: number; value: unknown}[];
}

// Where the caches are kept: IndexedDB, which tests replace.
export interface CacheStorage {
  read(scope: string): Promise<{meta: CacheMeta; records: Lists} | null>;
  // Applies what change returns for the meta it finds, at once (in one
  // transaction); null changes nothing.
  update(scope: string, change: (meta: CacheMeta | undefined) => CacheWrite | null): Promise<void>;
  list(): Promise<{scope: string; meta: CacheMeta}[]>;
  remove(scopes: string[]): Promise<void>;
}

// What a page last read or wrote for a server: its lists, by object, and
// its meta.
interface Saved {
  lists: Lists;
  meta: CacheMeta;
}

// SessionCache keeps the conversations of servers in a storage.
export class SessionCache {
  private saved = new Map<string, Saved>();
  private writes = 0;
  private readonly page = Math.random().toString(36).slice(2);

  constructor(
    private readonly storage: CacheStorage,
    private readonly now: () => number = Date.now,
  ) {}

  // Loads the cached session of scope, or null when there is none. It also
  // marks the cache used, and lets go of those of other servers that expired
  // or are the least recently used beyond the cap.
  async load(scope: string): Promise<CachedSession | null> {
    const stored = await this.storage.read(scope);
    const session = stored && parseCachedSession({...stored.meta, ...stored.records});
    if (!stored || !session) return null;
    const {meta, records} = stored;
    // A cache written before the chat kept a kind (the tasks) can't say
    // what of it the watermark covers: the next connection gets it all.
    if (KINDS.some((kind) => typeof meta.counts[kind] !== "number" || records[kind].length !== meta.counts[kind])) return null;
    // What the page holds is what the records hold, unless some had to go.
    if (KINDS.every((kind) => session[kind].length === records[kind].length)) {
      this.saved.set(scope, {lists: listsOf(session), meta});
    }
    const usedAt = this.now();
    void this.storage
      .update(scope, (current) => (current ? {meta: {...current, usedAt}, put: []} : null))
      .then(() => this.evict(scope))
      .catch(() => undefined);
    return session;
  }

  // Saves session for scope: only the records whose message isn't the
  // object this page last saved or loaded there, and nothing when nothing
  // changed. A page that didn't write the records last writes them all.
  async save(scope: string, session: CachedSession): Promise<void> {
    const lists = listsOf(session);
    const saved = this.saved.get(scope);
    if (
      saved &&
      saved.meta.epoch === session.epoch &&
      saved.meta.seq === session.seq &&
      saved.meta.transport === session.transport &&
      KINDS.every((kind) => sameItems(saved.lists[kind], lists[kind]))
    ) {
      return;
    }
    const meta: CacheMeta = {
      epoch: session.epoch,
      seq: session.seq,
      transport: session.transport,
      usedAt: this.now(),
      counts: {messages: lists.messages.length, richMessages: lists.richMessages.length, requests: lists.requests.length, tasks: lists.tasks.length},
      writer: `${this.page}:${++this.writes}`,
    };
    await this.storage.update(scope, (current) => {
      const all = !saved || current?.writer !== saved.meta.writer;
      const put: CacheWrite["put"] = [];
      for (const kind of KINDS) {
        lists[kind].forEach((value, index) => {
          if (all || value !== saved.lists[kind][index]) put.push({kind, index, value});
        });
      }
      return {meta, put};
    });
    this.saved.set(scope, {lists, meta});
  }

  async clear(scope: string): Promise<void> {
    this.saved.delete(scope);
    await this.storage.remove([scope]);
  }

  private async evict(current: string): Promise<void> {
    const gone = cachesToEvict((await this.storage.list()).map(({scope, meta}) => ({scope, usedAt: meta.usedAt})), this.now(), current);
    if (gone.length === 0) return;
    for (const scope of gone) this.saved.delete(scope);
    await this.storage.remove(gone);
  }
}

// cachesToEvict returns the caches to let go of: those not used for
// CACHE_TTL_MS, and the least recently used beyond MAX_CACHED_SERVERS. The
// cache of current, the server open now, stays.
export function cachesToEvict(caches: {scope: string; usedAt: number}[], now: number, current: string): string[] {
  const fresh = (usedAt: number) => now - usedAt < CACHE_TTL_MS;
  const others = caches.filter(({scope}) => scope !== current);
  const kept = others.filter(({usedAt}) => fresh(usedAt)).sort((a, b) => b.usedAt - a.usedAt);
  const room = MAX_CACHED_SERVERS - (others.length < caches.length ? 1 : 0);
  return [...others.filter(({usedAt}) => !fresh(usedAt)), ...kept.slice(Math.max(0, room))].map(({scope}) => scope);
}

// parseCachedSession checks what the cache held, and normalizes it as the
// events are normalized when they arrive: a cache can hold a rich message
// with null content, which the transcript can't show.
export function parseCachedSession(value: unknown): CachedSession | null {
  if (!value || typeof value !== "object") return null;
  const session = value as Partial<Record<keyof CachedSession, unknown>>;
  if (typeof session.epoch !== "string" || typeof session.seq !== "number" || !Array.isArray(session.messages)) return null;
  return {
    epoch: session.epoch,
    seq: session.seq,
    messages: items<Message>(session.messages),
    richMessages: items<RichMessagePayload>(session.richMessages).map(normalizeRichMessage),
    requests: items<RequestPayload>(session.requests).map(normalizeRequest),
    tasks: items<components["schemas"]["Task"]>(session.tasks).map(normalizeTask),
    transport: typeof session.transport === "string" ? session.transport : undefined,
  };
}

function listsOf(session: CachedSession): Lists {
  return {messages: session.messages, richMessages: session.richMessages, requests: session.requests, tasks: session.tasks};
}

// The objects in list, or none when it isn't one.
function items<T>(list: unknown): T[] {
  return Array.isArray(list) ? list.filter((item): item is T => typeof item === "object" && item !== null) : [];
}

function sameItems(a: readonly unknown[], b: readonly unknown[]): boolean {
  return a.length === b.length && a.every((item, i) => item === b[i]);
}

const DB_NAME = "agentapi-chat";
// Version 1 kept a server's whole conversation in one record of
// "sessions", written again on every save.
const DB_VERSION = 2;
const META = "sessions";
const RECORDS = "records";

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      // The caches of version 1 go: the next connection reads the
      // conversation from the server.
      if (db.objectStoreNames.contains(META)) db.deleteObjectStore(META);
      db.createObjectStore(META);
      db.createObjectStore(RECORDS);
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

// Runs run in a transaction over both stores, and resolves with what it
// passes to done once the transaction completed.
function transact<T>(mode: IDBTransactionMode, run: (meta: IDBObjectStore, records: IDBObjectStore, done: (value: T) => void) => void): Promise<T> {
  return openDb().then((db) => new Promise<T>((resolve, reject) => {
    const tx = db.transaction([META, RECORDS], mode);
    let result: T;
    run(tx.objectStore(META), tx.objectStore(RECORDS), (value) => (result = value));
    tx.oncomplete = () => {
      db.close();
      resolve(result);
    };
    tx.onabort = tx.onerror = () => {
      db.close();
      reject(tx.error);
    };
  }));
}

// The records of scope: of one kind from a place on, or all of them.
const kindRange = (scope: string, kind: Kind, from = 0) => IDBKeyRange.bound([scope, kind, from], [scope, kind, Infinity]);
const scopeRange = (scope: string) => IDBKeyRange.bound([scope], [scope, []]);

const indexedDBStorage: CacheStorage = {
  read: (scope) =>
    transact("readonly", (meta, records, done) => {
      const lists: Partial<Lists> = {};
      meta.get(scope).onsuccess = (event) => {
        const found = (event.target as IDBRequest<CacheMeta | undefined>).result;
        if (!found) return done(null);
        for (const kind of KINDS) {
          records.getAll(kindRange(scope, kind)).onsuccess = (got) => {
            lists[kind] = (got.target as IDBRequest<unknown[]>).result;
            if (KINDS.every((each) => lists[each])) done({meta: found, records: lists as Lists});
          };
        }
      };
    }),
  update: (scope, change) =>
    transact<void>("readwrite", (meta, records) => {
      meta.get(scope).onsuccess = (event) => {
        const write = change((event.target as IDBRequest<CacheMeta | undefined>).result);
        if (!write) return;
        for (const kind of KINDS) records.delete(kindRange(scope, kind, write.meta.counts[kind]));
        for (const {kind, index, value} of write.put) records.put(value, [scope, kind, index]);
        meta.put(write.meta, scope);
      };
    }),
  list: () =>
    transact("readonly", (meta, _records, done) => {
      const keys = meta.getAllKeys();
      const values = meta.getAll();
      values.onsuccess = () => done(values.result.map((value, i) => ({scope: String(keys.result[i]), meta: value as CacheMeta})));
    }),
  remove: (scopes) =>
    transact<void>("readwrite", (meta, records) => {
      for (const scope of scopes) {
        meta.delete(scope);
        records.delete(scopeRange(scope));
      }
    }),
};

const browserCache = new SessionCache(indexedDBStorage);

// Loads the cached session for key, or null when there is none or storage is
// unavailable (private mode, blocked site data). Never throws.
export async function loadSession(key: string): Promise<CachedSession | null> {
  try {
    if (typeof indexedDB === "undefined") return null;
    return await browserCache.load(key);
  } catch {
    return null;
  }
}

export async function saveSession(key: string, session: CachedSession): Promise<void> {
  try {
    if (typeof indexedDB === "undefined") return;
    await browserCache.save(key, session);
  } catch {
    // The cache only speeds up the next load; losing it is harmless.
  }
}

export async function clearSession(key: string): Promise<void> {
  try {
    if (typeof indexedDB === "undefined") return;
    await browserCache.clear(key);
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
