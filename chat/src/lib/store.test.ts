import {describe, expect, test} from "bun:test";
import {Store} from "./store";

describe("Store", () => {
  test("tells its listeners about a new value, not about the same one", () => {
    const store = new Store({n: 1});
    let calls = 0;
    const unsubscribe = store.subscribe(() => calls++);
    const value = store.get();
    store.set(value);
    expect(calls).toBe(0);
    store.set({n: 2});
    expect(calls).toBe(1);
    expect(store.get().n).toBe(2);
    unsubscribe();
    store.set({n: 3});
    expect(calls).toBe(1);
  });
});
