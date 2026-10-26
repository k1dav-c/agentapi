import {describe, expect, test} from "bun:test";
import {activity, shortError, applyHubEvent, chatURL, countFilters, formatAgo, formatElapsed, matchesSearch, groupInstances, groupNames, hubRoot, instanceState, needsYou, sortInstances, stateLabel, usagePercent, type HubInstance, type Summary} from "./hub";

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
      "/chat/embed?url=https%3A%2F%2Fhub.vpn%3A3290%2Fi%2Fdev-1%253A3284&hub=1",
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

  test("filters count what needs you, works, finished unseen and can't be reached", () => {
    const counts = countFilters([
      instance("a", {summary: summary({attention: "request"})}),
      instance("b", {summary: summary({status: "running"})}),
      instance("c", {summary: summary({unread: true})}),
      instance("d", {connection: "unreachable"}),
      instance("e", {connection: "stopped"}),
      instance("f"),
    ]);
    expect(counts).toEqual({all: 6, "needs-you": 1, working: 1, unread: 1, offline: 2});
  });

  test("search matches every word against name, group, title and machine", () => {
    const i = instance("dev-1:3284", {name: "checkout-api", group: "Client work", hostname: "dev-1", summary: summary({title: "Deploy the release"})});
    expect(matchesSearch(i, "checkout client")).toBe(true);
    expect(matchesSearch(i, "RELEASE dev-1")).toBe(true);
    expect(matchesSearch(i, "checkout billing")).toBe(false);
    expect(matchesSearch(i, "  ")).toBe(true);
  });

  test("a row says what the instance waits for or does, and since when", () => {
    const asked = instance("a", {summary: summary({attention: "request", pending_requests: 1, request: {id: "r1", kind: "approval", title: "Run: make release", created_at: "2026-10-26T10:00:00Z"}})});
    expect(activity(asked)).toEqual({label: "Approval", text: "Run: make release", tone: "needs", since: "2026-10-26T10:00:00Z"});
    expect(activity(instance("b", {summary: summary({attention: "login"})}))).toMatchObject({label: "Login", text: "Log in to continue"});
    const working = instance("c", {summary: summary({status: "running", task: {id: 1, status: "running", prompt: "Migrate invoices", started_at: "2026-10-26T10:00:00Z", errors: [], steered: []}})});
    expect(activity(working)).toEqual({label: "Working", text: "Migrate invoices", tone: "working", since: "2026-10-26T10:00:00Z"});
    expect(activity(instance("d", {connection: "unreachable", error: "connection refused"})).tone).toBe("fault");
    expect(activity(instance("e")).text).toBe("No task yet");
  });

  test("ages and running times are short", () => {
    const now = Date.parse("2026-10-26T10:00:00Z");
    expect(formatAgo("2026-10-26T09:59:55Z", now)).toBe("now");
    expect(formatAgo("2026-10-26T09:59:15Z", now)).toBe("45s");
    expect(formatAgo("2026-10-26T09:48:00Z", now)).toBe("12m");
    expect(formatAgo("2026-10-26T07:00:00Z", now)).toBe("3h");
    expect(formatAgo("2026-10-24T10:00:00Z", now)).toBe("2d");
    expect(formatAgo(undefined, now)).toBe("");
    expect(formatElapsed("2026-10-26T09:57:46Z", now)).toBe("2m 14s");
    expect(formatElapsed("2026-10-26T08:55:00Z", now)).toBe("1h 05m");
  });

  test("connection errors keep the part that says what happened", () => {
    expect(shortError('unreachable: Get "http://127.0.0.1:3308/events?only=summary": dial tcp 127.0.0.1:3308: connect: connection refused')).toBe("Connection refused");
    expect(shortError("the instance needs an API token (--instance-token)")).toBe("the instance needs an API token (--instance-token)");
    expect(shortError(undefined)).toBe("");
  });
});
