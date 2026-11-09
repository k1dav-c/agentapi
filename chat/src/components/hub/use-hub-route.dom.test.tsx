import {describe, expect, test} from "bun:test";
import {act} from "react";
import {withDOM} from "@/test/dom";
import {renderHook} from "@/test/render";
import {useHubRoute} from "./use-hub-route";

withDOM();

// go moves in the history, and waits for the browser's popstate, which
// comes a moment later.
const go = (delta: number) =>
  act(
    () =>
      new Promise<void>((resolve) => {
        window.addEventListener("popstate", () => setTimeout(resolve, 0), {once: true});
        history.go(delta);
      }),
  );

describe("useHubRoute", () => {
  test("follows the browser's history: Back from an agent opened from Issues is Issues", async () => {
    const hook = await renderHook(() => useHubRoute(), {});
    expect(hook.result.current.issues).toBe(false);
    await act(async () => hook.result.current.openIssues());
    await act(async () => hook.result.current.select("pve~a"));
    expect(location.hash).toBe("#pve~a");
    expect(hook.result.current).toMatchObject({agent: "pve~a", issues: true});

    await go(-1);
    expect(hook.result.current).toMatchObject({agent: undefined, issues: true});
    await act(
      () =>
        new Promise<void>((resolve) => {
          window.addEventListener("popstate", () => setTimeout(resolve, 0), {once: true});
          hook.result.current.closeIssues();
        }),
    );
    expect(hook.result.current).toMatchObject({agent: undefined, issues: false});
    await hook.unmount();
  });
});
