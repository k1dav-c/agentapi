import {describe, expect, test} from "bun:test";
import type {HubInstance} from "./hub";
import {hubInstance} from "./hub-fixtures";
import {favoritesToFrame, frameInstance, fromFrame, messageFromFrame, messageFromHub, openFromFrame, parseFromFrame, parseToFrame, shownToFrame, toFrame, type FrameInstance} from "./hub-frame";

const ORIGIN = "http://hub.test";

const instance: HubInstance = hubInstance("dev-1:3284", {
  name: "checkout-api",
  hostname: "dev-1",
  agent_type: "claude",
  agentapi_version: "2.1.0",
  last_seen: "2026-11-01T09:59:00Z",
});

describe("the chat's messages to the hub", () => {
  const frame = {};
  const from = (data: unknown, source: unknown = frame, origin = ORIGIN) => messageFromFrame({origin, data, source: source as MessageEventSource}, frame, ORIGIN);

  test("each ask, and open with the agent's ID", () => {
    for (const ask of ["ready", "back", "cancel-update", "manage-favorites"] as const) expect(from(fromFrame(ask))).toEqual({source: "agentapi.hub", type: ask});
    expect(from(openFromFrame("b"))).toEqual({source: "agentapi.hub", type: "open", id: "b"});
  });

  test("only from the frame's chat, of the same origin, in the protocol", () => {
    expect(from(fromFrame("ready"), {})).toBeUndefined();
    expect(from(fromFrame("ready"), frame, "http://evil.test")).toBeUndefined();
    expect(messageFromFrame({origin: ORIGIN, data: fromFrame("ready"), source: null}, null, ORIGIN)).toBeUndefined();
    for (const data of [{source: "someone", type: "ready"}, {source: "agentapi.hub", type: "other"}, {source: "agentapi.hub", type: "open", id: 7}, {source: "agentapi.hub", type: "open", id: ""}, "ready", null, []]) {
      expect(parseFromFrame(data)).toBeUndefined();
    }
  });
});

describe("the hub's side", () => {
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

});

describe("the hub's messages to the chat (FrameBar)", () => {
  const parent = {};
  const from = (data: unknown, source: unknown = parent, origin = ORIGIN) => messageFromHub({origin, data, source: source as MessageEventSource}, parent, ORIGIN);
  const told = frameInstance(instance, {hubVersion: "2.2.0", agent: "Claude Code", tab: "t"});

  test("the instance, as the hub tells it", () => {
    expect(from(toFrame(told))).toEqual(toFrame(told));
    const full: FrameInstance = {
      ...told,
      lastKnown: true,
      update: {allowed: true, state: "failed", keeps_conversation: true, keeps_session: true, error: "no network"},
      machine: [
        {id: "a", name: "home", state: "working", label: "Working", ran: true, agentType: "claude", agent: "Claude Code"},
        {id: "b", name: "home", state: "idle", label: "Idle"},
      ],
    };
    expect(from(toFrame(full))).toEqual(toFrame(full));
  });

  test("only from the page that frames it, of the same origin, in the protocol", () => {
    expect(from(toFrame(told), {})).toBeUndefined();
    expect(from(toFrame(told), parent, "http://evil.test")).toBeUndefined();
    expect(from(fromFrame("ready"))).toBeUndefined();
    expect(from({source: "someone", type: "instance", instance: told})).toBeUndefined();
  });

  test("an instance without its ID, name, label or tab is none; the rest is checked field by field", () => {
    for (const missing of ["id", "name", "label", "tab"]) {
      const rest: Record<string, unknown> = {...told};
      delete rest[missing];
      expect(parseToFrame({source: "agentapi.hub", type: "instance", instance: rest})).toBeUndefined();
    }
    const odd = parseToFrame({
      source: "agentapi.hub",
      type: "instance",
      instance: {id: "a", name: "a", label: "Rebooting", tab: "t", state: "rebooting", hostname: 7, lastKnown: "yes", update: {state: 1}, machine: [{id: "x"}, "y"], extra: true},
    });
    expect(odd).toEqual(toFrame({id: "a", name: "a", state: "unknown", label: "Rebooting", tab: "t"}));
  });
});

describe("favorites", () => {
  const parent = {};
  const from = (data: unknown, source: unknown = parent, origin = ORIGIN) => messageFromHub({origin, data, source: source as MessageEventSource}, parent, ORIGIN);

  test("the chat takes them from the page that frames it, leaving out what isn't one", () => {
    const told = favoritesToFrame([
      {id: "a", title: "Review", text: "Review the diff."},
      {id: "b", text: "Write the tests first."},
    ]);
    expect(from(told)).toEqual(told);
    expect(from({source: "agentapi.hub", type: "favorites", favorites: [{id: "a", text: "x"}, {id: 2, text: "y"}, {id: "c"}, null, {id: "a", text: "twice"}]})).toEqual(favoritesToFrame([{id: "a", text: "x"}]));
    expect(from({source: "agentapi.hub", type: "favorites", favorites: []})).toEqual(favoritesToFrame([]));
  });

  test("ignores another window, another origin and a list that isn't one", () => {
    const told = favoritesToFrame([{id: "a", text: "x"}]);
    expect(from(told, {})).toBeUndefined();
    expect(from(told, parent, "http://evil.test")).toBeUndefined();
    expect(from({source: "agentapi.hub", type: "favorites", favorites: "x"})).toBeUndefined();
  });
});

describe("shown", () => {
  const parent = {};
  const from = (data: unknown, source: unknown = parent) => messageFromHub({origin: ORIGIN, data, source: source as MessageEventSource}, parent, ORIGIN);

  test("the chat learns whether the hub shows it, from the page that frames it only", () => {
    expect(from(shownToFrame(false))).toEqual({source: "agentapi.hub", type: "shown", shown: false});
    expect(from(shownToFrame(true))).toEqual({source: "agentapi.hub", type: "shown", shown: true});
    expect(from(shownToFrame(false), {})).toBeUndefined();
    for (const shown of ["false", 0, null, undefined]) expect(from({source: "agentapi.hub", type: "shown", shown})).toBeUndefined();
  });
});
