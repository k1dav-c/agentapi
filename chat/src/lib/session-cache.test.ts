import {describe, expect, test} from "bun:test";
import {
  CACHE_TTL_MS,
  MAX_CACHED_SERVERS,
  SessionCache,
  cachesToEvict,
  loadSession,
  mergeByKey,
  parseCachedSession,
  type CacheMeta,
  type CacheStorage,
  type CachedSession,
} from "./session-cache";
import type {Message, RichMessage} from "./conversation";
import type {ServerTask} from "./server-tasks";

describe("mergeByKey", () => {
  const key = (item: {id?: number}) => (item.id === undefined ? undefined : String(item.id));

  test("replaces existing entries in place and appends new ones in order", () => {
    const previous = [{id: 1, v: "a"}, {id: 2, v: "b"}];
    const merged = mergeByKey(previous, [{id: 2, v: "B"}, {id: 3, v: "c"}, {id: 1, v: "A"}, {id: 3, v: "C"}], key);
    expect(merged).toEqual([{id: 1, v: "A"}, {id: 2, v: "B"}, {id: 3, v: "C"}]);
    expect(previous).toEqual([{id: 1, v: "a"}, {id: 2, v: "b"}]);
  });

  test("keeps keyless entries such as unsent drafts", () => {
    const previous = [{id: 1, v: "a"}, {v: "draft"}];
    expect(mergeByKey(previous, [{id: 2, v: "b"}], key)).toEqual([{id: 1, v: "a"}, {v: "draft"}, {id: 2, v: "b"}]);
  });

  test("handles a long replay in linear time", () => {
    const updates = Array.from({length: 20000}, (_, i) => ({id: i % 5000, v: String(i)}));
    const started = performance.now();
    const merged = mergeByKey([], updates, key);
    expect(merged).toHaveLength(5000);
    expect(merged[0]).toEqual({id: 0, v: "15000"});
    expect(performance.now() - started).toBeLessThan(200);
  });
});

describe("loadSession", () => {
  test("returns null instead of throwing when IndexedDB is unavailable", async () => {
    expect(await loadSession("http://localhost:3284")).toBeNull();
  });
});

describe("parseCachedSession", () => {
  test("normalizes what an earlier version cached", () => {
    // A cache that holds a rich message with null content showed a blank
    // page on every reload.
    const cached = parseCachedSession({
      epoch: "e1",
      seq: 12,
      messages: [{id: 0, role: "user", content: "hi", time: "2026-10-22T10:00:00Z"}],
      richMessages: [{message_id: "m1", role: "assistant", content: null, timestamp: "2026-10-22T10:00:01Z"}],
      savedAt: 1,
    });
    expect(cached?.richMessages[0].content).toEqual([]);
    expect(cached?.requests).toEqual([]);
    expect(cached?.seq).toBe(12);
  });

  test("rejects what isn't a cached session", () => {
    expect(parseCachedSession(null)).toBeNull();
    expect(parseCachedSession(undefined)).toBeNull();
    expect(parseCachedSession({epoch: "e1", messages: []})).toBeNull();
    expect(parseCachedSession({epoch: 1, seq: 1, messages: []})).toBeNull();
  });

  test("drops entries that aren't objects", () => {
    const cached = parseCachedSession({epoch: "e1", seq: 1, messages: [null, {id: 0, role: "user", content: "a"}], richMessages: "x", requests: [null], savedAt: 1});
    expect(cached?.messages).toHaveLength(1);
    expect(cached?.richMessages).toEqual([]);
    expect(cached?.requests).toEqual([]);
  });
});

// A storage in memory, as IndexedDB keeps it: a meta per server, and a
// record per message by its place. It counts the records written.
function memoryStorage() {
  const metas = new Map<string, CacheMeta>();
  const records = new Map<string, unknown>();
  const key = (scope: string, kind: string, index: number) => JSON.stringify([scope, kind, index]);
  let written = 0;
  const clone = <T,>(value: T): T => structuredClone(value);
  const storage: CacheStorage = {
    async read(scope) {
      const meta = metas.get(scope);
      if (!meta) return null;
      const list = (kind: string) => {
        const found: unknown[] = [];
        for (let i = 0; records.has(key(scope, kind, i)); i++) found.push(clone(records.get(key(scope, kind, i))));
        return found;
      };
      return {meta: clone(meta), records: {messages: list("messages"), richMessages: list("richMessages"), requests: list("requests"), tasks: list("tasks")}};
    },
    async update(scope, change) {
      const write = change(metas.has(scope) ? clone(metas.get(scope)!) : undefined);
      if (!write) return;
      for (const [k] of records) {
        const [s, kind, index] = JSON.parse(k) as [string, keyof CacheMeta["counts"], number];
        if (s === scope && index >= write.meta.counts[kind]) records.delete(k);
      }
      for (const {kind, index, value} of write.put) records.set(key(scope, kind, index), clone(value));
      written += write.put.length;
      metas.set(scope, clone(write.meta));
    },
    async list() {
      return [...metas].map(([scope, meta]) => ({scope, meta: clone(meta)}));
    },
    async remove(scopes) {
      for (const scope of scopes) {
        metas.delete(scope);
        for (const [k] of records) if ((JSON.parse(k) as string[])[0] === scope) records.delete(k);
      }
    },
  };
  return {storage, metas, records, written: () => written};
}

const message = (id: number, content: string): Message => ({id, role: id % 2 ? "agent" : "user", content, time: "2026-10-22T10:00:00Z"});
const rich = (id: string, text: string): RichMessage => ({message_id: id, role: "assistant", timestamp: "2026-10-22T10:00:01Z", content: [{type: "text", text}]});
const session = (messages: Message[], richMessages: RichMessage[], seq: number, tasks: ServerTask[] = []): CachedSession => ({epoch: "e1", seq, messages, richMessages, requests: [], tasks, transport: "native"});
const serverTask = (id: number, status: ServerTask["status"], seq: number): ServerTask => ({id, status, prompt: `task ${id}`, started_at: "2026-10-22T10:00:00Z", errors: [], steered: [], seq});
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("SessionCache", () => {
  test("loads what it saved", async () => {
    const {storage} = memoryStorage();
    const messages = [message(0, "task"), message(1, "reply")];
    const richMessages = [rich("m1", "thinking it over")];
    await new SessionCache(storage).save("s", session(messages, richMessages, 4));
    expect(await new SessionCache(storage).load("s")).toEqual(session(messages, richMessages, 4));
  });

  test("writes only the messages that changed, and nothing when none did", async () => {
    const memory = memoryStorage();
    const cache = new SessionCache(memory.storage);
    const messages = Array.from({length: 50}, (_, i) => message(i, `message ${i}`));
    const richMessages = Array.from({length: 200}, (_, i) => rich(`m${i}`, `step ${i}`));
    await cache.save("s", session(messages, richMessages, 10));
    expect(memory.written()).toBe(250);
    // The last reply grows, and a rich message comes.
    const grown = [...messages.slice(0, -1), message(49, "message 49, longer")];
    const more = [...richMessages, rich("m200", "new step")];
    await cache.save("s", session(grown, more, 12));
    expect(memory.written()).toBe(252);
    await cache.save("s", session(grown, more, 12));
    expect(memory.written()).toBe(252);
    // Only the watermark moved: the meta is written, no message.
    await cache.save("s", session(grown, more, 13));
    expect(memory.written()).toBe(252);
    expect(memory.metas.get("s")?.seq).toBe(13);
    expect(await new SessionCache(memory.storage).load("s")).toEqual(session(grown, more, 13));
  });

  test("a shorter conversation drops the records past its end", async () => {
    const memory = memoryStorage();
    const cache = new SessionCache(memory.storage);
    await cache.save("s", session([message(0, "a"), message(1, "b"), message(2, "c")], [rich("m1", "x")], 3));
    await cache.save("s", {...session([message(0, "fresh")], [], 1), epoch: "e2"});
    expect(memory.records.size).toBe(1);
    expect(await new SessionCache(memory.storage).load("s")).toEqual({...session([message(0, "fresh")], [], 1), epoch: "e2"});
  });

  test("after another page wrote, a page writes everything again", async () => {
    // Two tabs on the same server: the records may hold what the other one
    // wrote, older or newer.
    const memory = memoryStorage();
    const first = new SessionCache(memory.storage);
    const second = new SessionCache(memory.storage);
    const messages = [message(0, "task"), message(1, "reply")];
    await first.save("s", session(messages, [], 2));
    await second.save("s", session([message(0, "task"), message(1, "other reply")], [], 3));
    const written = memory.written();
    const next = [...messages, message(2, "next")];
    await first.save("s", session(next, [], 4));
    expect(memory.written()).toBe(written + 3);
    expect(await new SessionCache(memory.storage).load("s")).toEqual(session(next, [], 4));
  });

  test("a load is where saving goes on from", async () => {
    const memory = memoryStorage();
    const messages = [message(0, "task"), message(1, "reply")];
    await new SessionCache(memory.storage).save("s", session(messages, [], 2));
    const cache = new SessionCache(memory.storage);
    const loaded = (await cache.load("s"))!;
    const written = memory.written();
    await cache.save("s", session([...loaded.messages, message(2, "new")], loaded.richMessages, 3));
    expect(memory.written()).toBe(written + 1);
  });

  test("keeps the server's tasks with the conversation", async () => {
    const memory = memoryStorage();
    const cache = new SessionCache(memory.storage);
    const messages = [message(0, "task")];
    await cache.save("s", session(messages, [], 3, [serverTask(0, "running", 2)]));
    const written = memory.written();
    // The task ended: only its record is written again.
    const tasks = [serverTask(0, "completed", 3)];
    await cache.save("s", session(messages, [], 3, tasks));
    expect(memory.written()).toBe(written + 1);
    expect((await new SessionCache(memory.storage).load("s"))?.tasks).toEqual(tasks);
  });

  test("a cache from before the tasks were kept isn't loaded", async () => {
    // Its watermark covers task updates it doesn't hold, which a resumed
    // connection wouldn't send again.
    const memory = memoryStorage();
    await new SessionCache(memory.storage).save("s", session([message(0, "task")], [], 3));
    const meta = memory.metas.get("s")!;
    memory.metas.set("s", {...meta, counts: {messages: 1, richMessages: 0, requests: 0} as CacheMeta["counts"]});
    expect(await new SessionCache(memory.storage).load("s")).toBeNull();
  });

  test("a load normalizes what an earlier version kept", async () => {
    const memory = memoryStorage();
    await new SessionCache(memory.storage).save("s", session([message(0, "task")], [{...rich("m1", ""), content: null} as unknown as RichMessage], 1));
    expect((await new SessionCache(memory.storage).load("s"))?.richMessages[0].content).toEqual([]);
  });

  test("a load marks the cache used, and lets go of old ones and those beyond the cap", async () => {
    const memory = memoryStorage();
    let now = 1_000_000_000_000;
    const cache = new SessionCache(memory.storage, () => now);
    for (let i = 0; i < MAX_CACHED_SERVERS + 3; i++) {
      now += 1000;
      await cache.save(`server-${i}`, session([message(0, `task ${i}`)], [], 1));
    }
    now += CACHE_TTL_MS / 2;
    await cache.load("server-0");
    await flush();
    await flush();
    const kept = [...memory.metas.keys()];
    expect(kept).toHaveLength(MAX_CACHED_SERVERS);
    // The one loaded stays; the least recently used before it went.
    expect(kept).toContain("server-0");
    expect(kept).not.toContain("server-1");
    expect(memory.metas.get("server-0")?.usedAt).toBe(now);
    now += CACHE_TTL_MS;
    await cache.load("server-0");
    await flush();
    await flush();
    expect([...memory.metas.keys()]).toEqual(["server-0"]);
    expect(memory.records.size).toBe(1);
  });

  test("clearing forgets a server's cache", async () => {
    const memory = memoryStorage();
    const cache = new SessionCache(memory.storage);
    await cache.save("s", session([message(0, "task")], [], 1));
    await cache.clear("s");
    expect(await cache.load("s")).toBeNull();
    expect(memory.records.size).toBe(0);
  });
});

describe("cachesToEvict", () => {
  const now = 2_000_000_000_000;
  test("lets go of caches not used for the TTL", () => {
    const caches = [
      {scope: "fresh", usedAt: now - 1000},
      {scope: "old", usedAt: now - CACHE_TTL_MS - 1},
      {scope: "broken", usedAt: Number.NaN},
    ];
    expect(cachesToEvict(caches, now, "fresh")).toEqual(["old", "broken"]);
  });

  test("keeps the most recently used up to the cap, the open server first", () => {
    const caches = Array.from({length: MAX_CACHED_SERVERS + 2}, (_, i) => ({scope: `s${i}`, usedAt: now - i * 1000}));
    // s0 is the newest; the open one is the oldest.
    const open = `s${MAX_CACHED_SERVERS + 1}`;
    expect(cachesToEvict(caches, now, open)).toEqual([`s${MAX_CACHED_SERVERS - 1}`, `s${MAX_CACHED_SERVERS}`]);
    expect(cachesToEvict(caches.slice(0, MAX_CACHED_SERVERS), now, "s0")).toEqual([]);
  });
});
