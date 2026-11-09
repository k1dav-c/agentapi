import {afterEach, beforeEach, describe, expect, test} from "bun:test";
import {hubSettings, readHubSetting, writeHubSetting, type HubSettingName, type HubSettings} from "./hub-settings";

// What a page of an earlier release stored, by key, as it wrote it.
const stored: Record<string, string> = {
  "agentapi.hub.collapsed": JSON.stringify([" pinned", "Client work"]),
  "agentapi.hub.notify": JSON.stringify(true),
  "agentapi.hub.agents": JSON.stringify(["codex", "claude"]),
  "agentapi.hub.machine-tabs": JSON.stringify([
    ["Group\nhost dev-1", "dev-1:3285"],
    [" recent\nhost dev-2", "dev-2:3284", 1762000000000],
  ]),
  "agentapi.hub.list-order": "recent",
  "agentapi.hub.by-machine": "true",
  "agentapi.hub.list-width": "420",
  "agentapi.hub.issues-repo": "acme/widget",
  "agentapi.hub.issues-agent": "codex",
};

const read: HubSettings = {
  collapsed: new Set([" pinned", "Client work"]),
  notify: true,
  agents: new Set(["claude", "codex"]),
  tabPicks: new Map([
    ["Group\nhost dev-1", {id: "dev-1:3285", at: undefined}],
    [" recent\nhost dev-2", {id: "dev-2:3284", at: 1762000000000}],
  ]),
  listOrder: "recent",
  byMachine: true,
  listWidth: 420,
  issuesRepo: "acme/widget",
  issuesAgent: "codex",
};

const defaults: HubSettings = {
  collapsed: new Set(),
  notify: false,
  agents: new Set(),
  tabPicks: new Map(),
  listOrder: "groups",
  byMachine: false,
  listWidth: 360,
  issuesRepo: "",
  issuesAgent: "",
};

const names = Object.keys(hubSettings) as HubSettingName[];

describe("the hub's settings in the browser", () => {
  let items: Map<string, string>;
  let saved: Storage;
  beforeEach(() => {
    items = new Map();
    saved = globalThis.localStorage;
    globalThis.localStorage = {getItem: (key: string) => items.get(key) ?? null, setItem: (key: string, value: string) => void items.set(key, value)} as Storage;
  });
  afterEach(() => {
    globalThis.localStorage = saved;
  });

  test("keep their keys", () => {
    expect(names.map((name) => hubSettings[name].key).sort()).toEqual(Object.keys(stored).sort());
  });

  test("read what an earlier release stored", () => {
    for (const [key, value] of Object.entries(stored)) items.set(key, value);
    for (const name of names) expect(readHubSetting(name)).toEqual(read[name]);
  });

  test("write it as an earlier release wrote it", () => {
    for (const name of names) writeHubSetting(name, read[name] as never);
    // The agents in the order picked; the reader takes any order.
    expect(Object.fromEntries(items)).toEqual({...stored, "agentapi.hub.agents": JSON.stringify(["claude", "codex"])});
  });

  test("read nothing, or what they can't read, as their defaults", () => {
    for (const name of names) expect(readHubSetting(name)).toEqual(defaults[name]);
    for (const name of names) items.set(hubSettings[name].key, "{not json");
    for (const name of names) expect(readHubSetting(name)).toEqual(defaults[name]);
    for (const name of names) items.set(hubSettings[name].key, JSON.stringify({an: "object"}));
    for (const name of names) expect(readHubSetting(name)).toEqual(defaults[name]);
  });

  test("live in memory where storage is unavailable", () => {
    globalThis.localStorage = {
      getItem: () => {
        throw new Error("denied");
      },
      setItem: () => {
        throw new Error("denied");
      },
    } as unknown as Storage;
    for (const name of names) {
      expect(readHubSetting(name)).toEqual(defaults[name]);
      expect(() => writeHubSetting(name, read[name] as never)).not.toThrow();
    }
  });
});
