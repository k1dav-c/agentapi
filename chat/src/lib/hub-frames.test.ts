import {describe, expect, test} from "bun:test";
import {frameOrder, frameReady, keptFrames, keptReadies} from "./hub-frames";

const has = (...ids: string[]) => (id: string) => ids.includes(id);

describe("the frames the hub keeps", () => {
  test("the open agent first, then the ones opened before it", () => {
    let kept = keptFrames([], "a", has("a", "b", "c"), 4);
    expect(kept).toEqual(["a"]);
    kept = keptFrames(kept, "b", has("a", "b", "c"), 4);
    expect(kept).toEqual(["b", "a"]);
    kept = keptFrames(kept, "c", has("a", "b", "c"), 4);
    expect(kept).toEqual(["c", "b", "a"]);
  });

  test("opening a kept one again moves it to the front, keeping the others", () => {
    expect(keptFrames(["c", "b", "a"], "a", has("a", "b", "c"), 4)).toEqual(["a", "c", "b"]);
  });

  test("past max, the one opened longest ago goes", () => {
    const all = has("a", "b", "c", "d", "e");
    expect(keptFrames(["d", "c", "b", "a"], "e", all, 4)).toEqual(["e", "d", "c", "b"]);
    // A phone keeps fewer; a smaller max drops the oldest at once.
    expect(keptFrames(["d", "c", "b", "a"], "c", all, 2)).toEqual(["c", "d"]);
    expect(keptFrames(["d", "c", "b", "a"], undefined, all, 2)).toEqual(["d", "c"]);
    // The open one always stays.
    expect(keptFrames(["b"], "a", all, 0)).toEqual(["a"]);
  });

  test("an agent the hub forgot goes, open or not", () => {
    expect(keptFrames(["c", "b", "a"], "c", has("c", "a"), 4)).toEqual(["c", "a"]);
    expect(keptFrames(["c", "b", "a"], "d", has("b", "a"), 4)).toEqual(["b", "a"]);
  });

  test("without an open agent the frames stay, hidden", () => {
    const kept = ["b", "a"];
    expect(keptFrames(kept, undefined, has("a", "b"), 4)).toBe(kept);
  });

  test("the same list when nothing changed", () => {
    const kept = ["b", "a"];
    expect(keptFrames(kept, "b", has("a", "b"), 4)).toBe(kept);
    expect(keptFrames(kept, "a", has("a", "b"), 4)).not.toBe(kept);
  });
});

describe("the readies of the kept frames", () => {
  test("each ready of a kept chat counts, so the hub tells it again", () => {
    const first = frameReady(new Map(), "a", ["a"]);
    expect([...first]).toEqual([["a", 1]]);
    const again = frameReady(first, "a", ["a"]);
    expect([...again]).toEqual([["a", 2]]);
    expect(frameReady(again, "b", ["b", "a"]).get("a")).toBe(2);
  });

  test("a frame no longer kept counts nothing, and loses its count", () => {
    const readies = new Map([
      ["a", 2],
      ["b", 1],
    ]);
    expect(frameReady(readies, "c", ["a", "b"])).toBe(readies);
    expect([...keptReadies(readies, ["b"])]).toEqual([["b", 1]]);
    expect(keptReadies(readies, ["b", "a"])).toBe(readies);
  });
});

describe("where the frames stand in the page", () => {
  test("a new one goes last, and none moves when another shows", () => {
    let order = frameOrder([], ["a"]);
    expect(order).toEqual(["a"]);
    order = frameOrder(order, ["b", "a"]);
    expect(order).toEqual(["a", "b"]);
    // a opened again: first in the list, still first in the page.
    expect(frameOrder(order, ["a", "b"])).toBe(order);
  });

  test("one that goes leaves the others in their places", () => {
    expect(frameOrder(["a", "b", "c", "d"], ["e", "d", "b", "c"])).toEqual(["b", "c", "d", "e"]);
    expect(frameOrder(["a", "b", "c"], ["c", "a"])).toEqual(["a", "c"]);
  });
});
