import {afterEach, describe, expect, test} from "bun:test";
import {act} from "react";
import {withDOM} from "@/test/dom";
import {renderHook, settle} from "@/test/render";
import {useLoaded} from "./use-loaded";

withDOM();

let unmount: (() => Promise<void>) | undefined;
afterEach(async () => {
  await unmount?.();
  unmount = undefined;
});

describe("useLoaded", () => {
  test("a mutation's answer wins over a GET that was already under way", async () => {
    let answerGet!: (options: string[]) => void;
    let answerPut!: (options: string[]) => void;
    const get = new Promise<string[]>((resolve) => (answerGet = resolve));
    const put = new Promise<string[]>((resolve) => (answerPut = resolve));
    const hook = await renderHook(() => useLoaded(() => get), {});
    unmount = hook.unmount;

    await act(async () => hook.result.current[1]());
    expect(hook.result.current[0]).toEqual({state: "loading", data: undefined});
    let applied!: Promise<void>;
    await act(async () => {
      // As SessionSettings does when PUT /settings answers its new options.
      applied = (async () => hook.result.current[2](await put))();
    });
    await act(async () => {
      answerPut(["new-from-put"]);
      await applied;
    });
    expect(hook.result.current[0]).toEqual({state: "ready", data: ["new-from-put"]});

    await act(async () => answerGet(["old-from-get"]));
    await settle();
    expect(hook.result.current[0]).toEqual({state: "ready", data: ["new-from-put"]});
  });
});
