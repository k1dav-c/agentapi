import {afterEach, describe, expect, jest, test} from "bun:test";
import {act} from "react";
import type {ChatAPI} from "@/lib/chat-api";
import type {QueuedMessage} from "@/lib/queue";
import {withDOM} from "@/test/dom";
import {renderHook, settle} from "@/test/render";
import {useQueue} from "./use-queue";

withDOM();

const queued: QueuedMessage = {id: 1, content: "next", time: "2026-11-09T10:00:00Z"};

// A ChatAPI whose GET /queue answers the queue given, counting the reads.
function fakeAPI(queue: QueuedMessage[] = []) {
  const api = {reads: 0, getQueue: async () => (api.reads++, queue)};
  return api;
}

let unmount: (() => Promise<void>) | undefined;
afterEach(async () => {
  await unmount?.();
  unmount = undefined;
  jest.useRealTimers();
});

const minutes = (n: number) => act(async () => void jest.advanceTimersByTime(n * 60_000));

describe("useQueue's reads", () => {
  test("a server that may send no queue events is read every minute until one comes", async () => {
    jest.useFakeTimers();
    const api = fakeAPI([queued]);
    const hook = await renderHook(({version}: {version: string}) => useQueue(api as unknown as ChatAPI, version), {version: "0.27.0"});
    unmount = hook.unmount;
    await minutes(2);
    await settle();
    expect(api.reads).toBe(2);
    expect(hook.result.current.queuedMessages).toEqual([queued]);

    await act(async () => hook.result.current.applyQueue([]));
    await minutes(3);
    expect(api.reads).toBe(2);
  });

  test("a server that sends queue events isn't read on a timer, its queue idle or not", async () => {
    jest.useFakeTimers();
    const api = fakeAPI();
    const hook = await renderHook(({version}: {version: string}) => useQueue(api as unknown as ChatAPI, version), {version: "3.16.1"});
    unmount = hook.unmount;
    await minutes(5);
    expect(api.reads).toBe(0);
    // Still read on every connection, as the stream asks.
    await act(() => hook.result.current.refreshQueue());
    expect(api.reads).toBe(1);
  });
});
