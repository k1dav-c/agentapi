import {describe, expect, test} from "bun:test";
import {loadSession, mergeByKey} from "./session-cache";

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
