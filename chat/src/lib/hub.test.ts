import {describe, expect, test} from "bun:test";
import {applyHubEvent, chatURL, groupInstances, groupNames, hubRoot, instanceState, needsYou, sortInstances, stateLabel, usagePercent, type HubInstance, type Summary} from "./hub";

const summary = (overrides: Partial<Summary> = {}): Summary =>
  ({
    instance: {id: "x", server_id: "s", agent_type: "claude", transport: "native"},
    status: "stable",
    lifecycle: "ready",
    attention: "none",
    title: "",
    pending_requests: 0,
    queued_messages: 0,
    tasks: 0,
    unread: false,
    seen_at: "2026-10-26T00:00:00Z",
    ...overrides,
  }) as Summary;

const instance = (id: string, overrides: Partial<HubInstance> = {}): HubInstance => ({
  id,
  url: `http://${id}`,
  source: "webhook",
  last_seen: "2026-10-26T00:00:00Z",
  connection: "connected",
  summary: summary(),
  ...overrides,
});

describe("hub", () => {
  test("an instance's state, the most pressing first", () => {
    expect(instanceState(instance("a", {summary: summary({attention: "request"})}))).toBe("needs-you");
    expect(stateLabel(instance("a", {summary: summary({attention: "login"})}))).toBe("Needs login");
    expect(instanceState(instance("a", {summary: summary({status: "running"})}))).toBe("working");
    expect(instanceState(instance("a", {summary: summary({unread: true})}))).toBe("unread");
    expect(instanceState(instance("a"))).toBe("idle");
    expect(instanceState(instance("a", {summary: null, connection: "connecting"}))).toBe("connecting");
    expect(instanceState(instance("a", {connection: "unreachable"}))).toBe("unreachable");
    expect(instanceState(instance("a", {connection: "stopped"}))).toBe("stopped");
    expect(stateLabel(instance("a", {summary: summary({attention: "exited"})}))).toBe("Agent exited");
  });

  test("rows that need you come first, then by name", () => {
    const rows = sortInstances([
      instance("idle", {name: "b"}),
      instance("gone", {connection: "stopped"}),
      instance("asks", {summary: summary({attention: "request"})}),
      instance("busy", {summary: summary({status: "running"})}),
      instance("idle2", {name: "a"}),
    ]);
    expect(rows.map((row) => row.id)).toEqual(["asks", "busy", "idle2", "idle", "gone"]);
    expect(needsYou(rows)).toBe(1);
  });

  test("events replace, change and remove instances", () => {
    let map = applyHubEvent(new Map(), {type: "instances", instances: [instance("a"), instance("b")]});
    map = applyHubEvent(map, {type: "instance", instance: instance("a", {name: "renamed"})});
    map = applyHubEvent(map, {type: "removed", id: "b"});
    expect([...map.values()].map((i) => i.name ?? i.id)).toEqual(["renamed"]);
  });

  test("the chat opens an instance through the hub", () => {
    expect(hubRoot("/chat", "https://hub.vpn:3290")).toBe("https://hub.vpn:3290");
    expect(hubRoot("/apps/hub/chat", "https://coder.example.com")).toBe("https://coder.example.com/apps/hub");
    expect(chatURL("/chat", "https://hub.vpn:3290", "dev-1:3284")).toBe(
      "/chat/embed?url=https%3A%2F%2Fhub.vpn%3A3290%2Fi%2Fdev-1%253A3284",
    );
  });

  test("the fullest usage limit", () => {
    expect(usagePercent(summary())).toBeUndefined();
    expect(
      usagePercent(summary({usage: {rate_limits: [{id: "five_hour", label: "5h", used_percent: 40}, {id: "seven_day", label: "7d", used_percent: 72}]}})),
    ).toBe(72);
  });

  test("rows split into their groups, by name, with the ungrouped last", () => {
    const groups = groupInstances([
      instance("a", {group: "Work"}),
      instance("b"),
      instance("c", {group: "Clients", summary: summary({attention: "request"})}),
      instance("d", {group: "Work", summary: summary({status: "running"})}),
    ]);
    expect(groups.map((g) => [g.name, g.instances.map((i) => i.id), g.needsYou])).toEqual([
      ["Clients", ["c"], 1],
      ["Work", ["d", "a"], 0],
      ["", ["b"], 0],
    ]);
    expect(groupNames([instance("a", {group: "Work"}), instance("b", {group: "Clients"}), instance("c", {group: "Work"})])).toEqual(["Clients", "Work"]);
    expect(groupInstances([instance("a"), instance("b")]).map((g) => g.name)).toEqual([""]);
  });
});
