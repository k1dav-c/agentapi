import {afterEach, describe, expect, test} from "bun:test";
import {act} from "react";
import {withDOM} from "@/test/dom";
import {json, restoreFetch, routeFetch} from "@/test/fetch";
import {renderHook, settle} from "@/test/render";
import {useHubGitHub} from "./use-hub-github";
import {useDispatch} from "./issues/use-dispatch";

withDOM();

afterEach(() => restoreFetch());

describe("the settings' GitHub and the Issues page's, one", () => {
  test("a disconnect in the settings shows in Issues, read once, past a read under way", async () => {
    let connected = true;
    let reads = 0;
    let release!: () => void;
    const slow = new Promise<void>((resolve) => (release = resolve));
    routeFetch({
      "/hub-shared/api/github": async () => {
        reads++;
        const status = connected ? {connected: true, slug: "app", installations: []} : {connected: false, installations: []};
        // The read as the page shows again: slow, and from before the disconnect.
        if (reads === 3) {
          await slow;
          return json({connected: true, slug: "app", installations: []});
        }
        return json(status);
      },
      "DELETE /hub-shared/api/github": () => {
        connected = false;
        return new Response(null, {status: 204});
      },
    });
    const settings = await renderHook(() => useHubGitHub("/hub-shared"), {});
    const issues = await renderHook(() => useDispatch("/hub-shared", {hosts: [], jobs: []}), {});
    await settle();
    expect(reads).toBe(2);

    await act(async () => void window.dispatchEvent(new Event("focus")));
    await act(async () => void (await settings.result.current.disconnect()));
    release();
    await settle();
    expect(reads).toBe(4);
    for (const status of [settings.result.current.status, issues.result.current.github]) {
      expect(status).toEqual({state: "ready", value: {connected: false, installations: []}});
    }
    await settings.unmount();
    await issues.unmount();
  });
});
