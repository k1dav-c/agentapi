import {describe, expect, test} from "bun:test";
import {loadSession, mergeByKey, parseCachedSession} from "./session-cache";

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
