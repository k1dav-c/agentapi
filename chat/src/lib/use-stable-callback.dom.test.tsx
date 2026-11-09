import {afterEach, describe, expect, test} from "bun:test";
import {withDOM} from "@/test/dom";
import {renderHook} from "@/test/render";
import {useLatest, useStableCallback} from "./use-stable-callback";

withDOM();

let unmount: (() => Promise<void>) | undefined;
afterEach(async () => {
  await unmount?.();
  unmount = undefined;
});

describe("useLatest and useStableCallback", () => {
  test("the same ref and function from render to render, with the latest value", async () => {
    const hook = await renderHook(({value}: {value: string}) => ({ref: useLatest(value), call: useStableCallback(() => value)}), {value: "a"});
    unmount = hook.unmount;
    const {ref, call} = hook.result.current;
    expect(ref.current).toBe("a");
    expect(call()).toBe("a");
    await hook.rerender({value: "b"});
    expect(hook.result.current.ref).toBe(ref);
    expect(hook.result.current.call).toBe(call);
    expect(ref.current).toBe("b");
    expect(call()).toBe("b");
  });
});
