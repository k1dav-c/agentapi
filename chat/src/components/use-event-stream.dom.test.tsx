import {afterEach, beforeEach, describe, expect, jest, test} from "bun:test";
import {act} from "react";
import {initialAgentStatus, type AgentStatus} from "@/lib/agent-status";
import {resetAuthState, setSignInHandler} from "@/lib/auth";
import type {ConversationAction} from "@/lib/conversation-state";
import type {CachedSession} from "@/lib/session-cache";
import {SyncTracker} from "@/lib/session-sync";
import {withDOM} from "@/test/dom";
import {FakeEventSource} from "@/test/fake-event-source";
import {json, restoreFetch, routeFetch} from "@/test/fetch";
import {renderHook, settle} from "@/test/render";
import {useEventStream} from "./use-event-stream";

withDOM();

const url = "http://hub.test/agent";

// The event sources the stream opened, the latest last.
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
  resetAuthState();
});

// The server's session: open, or one that needs the token this browser
// doesn't have yet.
function session(answers: {auth_required: boolean; authenticated: boolean}[]) {
  return routeFetch({"/agent/auth/session": () => json(answers.length > 1 ? answers.shift() : answers[0])});
}

const cached: CachedSession = {epoch: "e1", seq: 42, messages: [{id: 0, role: "user", content: "Hi", time: "2026-11-09T10:00:00Z"}], richMessages: [], requests: [], tasks: []};

// useEventStream with what the chat gives it, the cache from loadCache.
async function mountStream(loadCache: () => Promise<CachedSession | null>) {
  const actions: ConversationAction[] = [];
  const statusRef = {current: initialAgentStatus as AgentStatus};
  const args = {
    agentAPIUrl: url,
    syncRef: {current: new SyncTracker()},
    dispatch: (action: ConversationAction) => void actions.push(action),
    updateAgentStatus: (change: (status: AgentStatus) => AgentStatus) => void (statusRef.current = change(statusRef.current)),
    agentStatusRef: statusRef,
    refreshQueue: async () => {},
    applyQueue: () => {},
    queueMayHaveSent: () => {},
    onReplayed: () => {},
    loadCache,
  };
  const hook = await renderHook(useEventStream, args);
  unmount = hook.unmount;
  return {actions, hook};
}

// Lets the promises under way settle until the stream has as many sources.
async function untilSources(count: number) {
  for (let i = 0; i < 20 && sources.length < count; i++) await settle();
  expect(sources).toHaveLength(count);
}

describe("useEventStream's start", () => {
  test("a cache that comes in time shows first, and the stream resumes from it", async () => {
    jest.useFakeTimers();
    session([{auth_required: false, authenticated: false}]);
    let loaded!: (session: CachedSession | null) => void;
    const {actions} = await mountStream(() => new Promise((resolve) => (loaded = resolve)));
    await act(async () => void jest.advanceTimersByTime(399));
    expect(sources).toHaveLength(0);

    await act(async () => loaded(cached));
    await untilSources(1);
    expect(actions.map((action) => action.type)).toEqual(["restore"]);
    expect(sources[0].url).toBe(`${url}/events?sync=1&since=42&epoch=e1`);
    // The timeout after it starts nothing more.
    await act(async () => void jest.advanceTimersByTime(1));
    await settle();
    expect(sources).toHaveLength(1);
  });

  test("a cache slower than 400 ms is left out: the stream starts from the beginning", async () => {
    jest.useFakeTimers();
    session([{auth_required: false, authenticated: false}]);
    let loaded!: (session: CachedSession | null) => void;
    const {actions} = await mountStream(() => new Promise((resolve) => (loaded = resolve)));
    await act(async () => void jest.advanceTimersByTime(400));
    await untilSources(1);
    expect(sources[0].url).toBe(`${url}/events?sync=1`);

    // Late, it would show a transcript the stream doesn't resume from.
    await act(async () => loaded(cached));
    await settle();
    expect(actions).toEqual([]);
    expect(sources).toHaveLength(1);
  });

  test("no cache: the stream starts at once", async () => {
    session([{auth_required: false, authenticated: false}]);
    const {actions} = await mountStream(async () => null);
    await untilSources(1);
    expect(sources[0].url).toBe(`${url}/events?sync=1`);
    expect(actions).toEqual([]);
  });
});

describe("useEventStream after a 401", () => {
  test("a stream that fails for want of a session reconnects as soon as the user signed in", async () => {
    // Open at first; then the server asks for a token.
    session([
      {auth_required: false, authenticated: false},
      {auth_required: true, authenticated: false},
      {auth_required: true, authenticated: true},
    ]);
    const asked: string[] = [];
    setSignInHandler(async (baseURL) => {
      asked.push(baseURL);
      return true;
    });
    const {hook} = await mountStream(async () => null);
    await untilSources(1);
    expect(asked).toEqual([]);

    // EventSource hides the 401: the stream fails like any other.
    await act(async () => sources[0].fail());
    expect(hook.result.current.connectionStatus).toBe("reconnecting");
    await untilSources(2);
    expect(asked).toEqual([url]);
    expect(sources[0].closed).toBe(true);
    // At once, without the backoff.
    expect(hook.result.current.nextReconnectAt).toBeNull();
    await act(async () => sources[1].open());
    expect(hook.result.current.connectionStatus).toBe("connected");
  });

  test("a failure while signed in waits for the backoff", async () => {
    session([{auth_required: true, authenticated: true}]);
    const asked: string[] = [];
    setSignInHandler(async (baseURL) => {
      asked.push(baseURL);
      return true;
    });
    const {hook} = await mountStream(async () => null);
    await untilSources(1);
    await act(async () => sources[0].fail());
    for (let i = 0; i < 10; i++) await settle();
    expect(sources).toHaveLength(1);
    expect(asked).toEqual([]);
    expect(hook.result.current.nextReconnectAt).not.toBeNull();
  });
});
