import {afterEach, beforeEach, describe, expect, jest, test} from "bun:test";
import {act} from "react";
import {withDOM} from "@/test/dom";
import {FakeEventSource} from "@/test/fake-event-source";
import {json, restoreFetch, routeFetch} from "@/test/fetch";
import {renderHook, settle} from "@/test/render";
import {useHubStream} from "./use-hub-stream";

withDOM();

// The event sources the hook opened, the latest last.
let sources: FakeEventSource[] = [];
const realEventSource = globalThis.EventSource;

beforeEach(() => {
  sources = [];
  globalThis.EventSource = class extends FakeEventSource {
    constructor(url: string, init?: EventSourceInit) {
      super(url, init);
      sources.push(this);
    }
  } as unknown as typeof EventSource;
});

let unmount: (() => Promise<void>) | undefined;
afterEach(async () => {
  await unmount?.();
  unmount = undefined;
  globalThis.EventSource = realEventSource;
  jest.useRealTimers();
  restoreFetch();
});

// GET /api/session answers each of answers in turn, the last from then on:
// a status, or a hub that doesn't answer (null).
function sessions(...answers: (number | null)[]) {
  return routeFetch({
    "GET /hub/api/session": () => {
      const answer = answers.length > 1 ? answers.shift()! : answers[0];
      if (answer === null) throw new TypeError("Failed to fetch");
      return answer === 200 ? json({authenticated: true}) : json({error: "no"}, answer);
    },
  });
}

async function until(done: () => boolean) {
  for (let i = 0; i < 20 && !done(); i++) await settle();
  expect(done()).toBe(true);
}

async function mountSignedIn(...later: (number | null)[]) {
  const calls = sessions(200, ...later);
  const hook = await renderHook(({root}: {root: string}) => useHubStream(root), {root: "/hub"});
  unmount = hook.unmount;
  await until(() => sources.length === 1);
  expect(hook.result.current.session).toBe("signed-in");
  expect(sources[0].url).toBe("/hub/api/events");
  expect(sources[0].init?.withCredentials).toBe(true);
  await act(async () => sources[0].open());
  return {hook, calls};
}

describe("useHubStream when its stream fails", () => {
  test("a session that ended signs the page out", async () => {
    const {hook, calls} = await mountSignedIn(401);
    // Open, it reconnects at once; the reconnect fails too.
    await act(async () => sources[0].fail());
    await act(async () => sources[1].fail());
    await until(() => hook.result.current.session === "signed-out");
    expect(hook.result.current.unreachable).toBe(false);
    expect(calls.filter((call) => call.url.endsWith("/api/session"))).toHaveLength(2);
    // Signed out, the stream closes.
    expect(sources.every((source) => source.closed)).toBe(true);
  });

  test("a hub that doesn't answer is unreachable, still signed in, until the stream opens again", async () => {
    jest.useFakeTimers();
    const {hook} = await mountSignedIn(null);
    await act(async () => sources[0].fail());
    await act(async () => sources[1].fail());
    await until(() => hook.result.current.unreachable);
    expect(hook.result.current.session).toBe("signed-in");

    // The stream tries again after its backoff, and opens.
    await act(async () => void jest.advanceTimersByTime(5_000));
    expect(sources).toHaveLength(3);
    await act(async () => sources[2].open());
    expect(hook.result.current.unreachable).toBe(false);
  });

  test("a proxy that answers for the hub (a 502) is unreachable too", async () => {
    const {hook} = await mountSignedIn(502);
    await act(async () => sources[0].fail());
    await act(async () => sources[1].fail());
    await until(() => hook.result.current.unreachable);
    expect(hook.result.current.session).toBe("signed-in");
  });
});
