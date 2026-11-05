import {describe, expect, test} from "bun:test";
import type {HubInstance} from "./hub";
import {frameInstance, frameMessage, fromFrame, instanceFromHub, onFrameMessage, openFromFrame, toFrame, type FromFrame} from "./hub-frame";

const ORIGIN = "http://hub.test";

const instance: HubInstance = {
  id: "dev-1:3284",
  name: "checkout-api",
  url: "http://dev-1:3284",
  hostname: "dev-1",
  agent_type: "claude",
  agentapi_version: "2.1.0",
  source: "webhook",
  last_seen: "2026-11-01T09:59:00Z",
  connection: "connected",
  state: "idle",
};

describe("frameMessage", () => {
  test("takes messages of the protocol from the same origin", () => {
    expect(frameMessage({origin: ORIGIN, data: fromFrame("ready")}, ORIGIN)).toEqual({source: "agentapi.hub", type: "ready"});
  });

  test("ignores other origins, other messages and what isn't an object", () => {
    expect(frameMessage({origin: "http://evil.test", data: fromFrame("ready")}, ORIGIN)).toBeUndefined();
    expect(frameMessage({origin: ORIGIN, data: {source: "someone", type: "ready"}}, ORIGIN)).toBeUndefined();
    expect(frameMessage({origin: ORIGIN, data: "ready"}, ORIGIN)).toBeUndefined();
    expect(frameMessage({origin: ORIGIN, data: null}, ORIGIN)).toBeUndefined();
  });
});

describe("the hub's side", () => {
  const ready = fromFrame("ready") as FromFrame;

  test("each ready of the same chat counts, so the hub tells it the instance again", () => {
    const first = onFrameMessage(undefined, ready, "a").framed;
    expect(first).toEqual({instance: "a", ready: 1});
    // The chat reloaded: a new state, which the hub posts its info again for.
    const again = onFrameMessage(first, ready, "a").framed;
    expect(again).toEqual({instance: "a", ready: 2});
    expect(again).not.toBe(first);
  });

  test("another instance's chat starts over", () => {
    expect(onFrameMessage({instance: "a", ready: 3}, ready, "b").framed).toEqual({instance: "b", ready: 1});
  });

  test("back and cancel-update are asks, which leave the frame as it is", () => {
    const framed = {instance: "a", ready: 1};
    expect(onFrameMessage(framed, fromFrame("back"), "a")).toEqual({framed, ask: "back"});
    expect(onFrameMessage(framed, fromFrame("cancel-update"), "a")).toEqual({framed, ask: "cancel-update"});
    expect(onFrameMessage(framed, {source: "agentapi.hub", type: "other"} as unknown as FromFrame, "a")).toEqual({framed});
  });

  test("the info it posts", () => {
    expect(frameInstance(instance, {hubVersion: "2.2.0", agent: "Claude Code", tab: "/chat/?url=x"})).toEqual({
      id: "dev-1:3284",
      name: "checkout-api",
      state: "idle",
      label: "Idle",
      agentType: "claude",
      agent: "Claude Code",
      hostname: "dev-1",
      version: "2.1.0",
      hubVersion: "2.2.0",
      lastKnown: false,
      update: undefined,
      tab: "/chat/?url=x",
    });
    const stopped = frameInstance({...instance, connection: "stopped", state: "stopped", hostname: ""}, {hubVersion: "", agent: "", tab: "t"});
    expect(stopped.lastKnown).toBe(true);
    expect(stopped.hostname).toBeUndefined();
    expect(stopped.agent).toBeUndefined();
    expect(stopped.hubVersion).toBeUndefined();
    expect(stopped.machine).toBeUndefined();
  });

  test("the agents of its machine, for the bar's switch, when there is another", () => {
    const agents = [
      {id: "dev-1:3284", name: "checkout-api", state: "idle" as const, label: "Idle", agentType: "claude", agent: "Claude Code"},
      {id: "dev-1:3285", name: "checkout-api", state: "needs-you" as const, label: "Needs your answer", agentType: "codex", agent: "Codex"},
    ];
    expect(frameInstance(instance, {hubVersion: "", agent: "", tab: "t", machine: agents}).machine).toEqual(agents);
    expect(frameInstance(instance, {hubVersion: "", agent: "", tab: "t", machine: agents.slice(0, 1)}).machine).toBeUndefined();
  });

  test("open is an ask for another agent, with its ID", () => {
    const framed = {instance: "a", ready: 1};
    expect(onFrameMessage(framed, openFromFrame("b"), "a")).toEqual({framed, open: "b"});
    expect(onFrameMessage(framed, {source: "agentapi.hub", type: "open", id: 7} as unknown as FromFrame, "a")).toEqual({framed});
    expect(onFrameMessage(framed, {source: "agentapi.hub", type: "open", id: ""} as FromFrame, "a")).toEqual({framed});
  });
});

describe("the chat's side (FrameBar)", () => {
  const parent = {};
  const told = toFrame({id: "dev-1:3284", name: "checkout-api", state: "idle", label: "Idle", tab: "t"});

  test("takes the instance from the page that frames it", () => {
    expect(instanceFromHub({origin: ORIGIN, data: told, source: parent as MessageEventSource}, parent, ORIGIN)).toEqual(told.instance);
  });

  test("ignores another window, another origin and the chat's own messages", () => {
    expect(instanceFromHub({origin: ORIGIN, data: told, source: {} as MessageEventSource}, parent, ORIGIN)).toBeUndefined();
    expect(instanceFromHub({origin: "http://evil.test", data: told, source: parent as MessageEventSource}, parent, ORIGIN)).toBeUndefined();
    expect(instanceFromHub({origin: ORIGIN, data: fromFrame("ready"), source: parent as MessageEventSource}, parent, ORIGIN)).toBeUndefined();
  });
});
