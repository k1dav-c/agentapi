import {describe, expect, test} from "bun:test";
import {activity, compareVersions, countedSpends, currentSpend, formatUSD, instanceVersion, olderThanHub, parseVersion, spendTitle, spendTotals, shortError, applyHubEvent, chatURL, countFilters, formatAgo, formatElapsed, matchesSearch, groupInstances, groupNames, hubRoot, instanceState, needsYou, sortInstances, stateLabel, usagePercent, versionTitle, versionTotals, type HubInstance, type Spend, type Summary} from "./hub";

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
  state: "idle",
  ...overrides,
});

describe("hub", () => {
  test("a row shows the state the hub worked out", () => {
    expect(instanceState(instance("a", {state: "needs-you"}))).toBe("needs-you");
    expect(stateLabel(instance("a", {state: "needs-you", summary: summary({attention: "login"})}))).toBe("Needs login");
    expect(stateLabel(instance("a", {state: "working"}))).toBe("Working");
    expect(stateLabel(instance("a", {state: "unread"}))).toBe("Finished");
    expect(stateLabel(instance("a", {state: "connecting", summary: null, connection: "connecting"}))).toBe("Connecting");
    expect(stateLabel(instance("a", {state: "stopped", connection: "stopped"}))).toBe("Stopped");
    expect(stateLabel(instance("a", {state: "stopped", summary: summary({attention: "exited"})}))).toBe("Agent exited");
  });

  test("a state from a newer hub is unknown, and waits at the end", () => {
    const newer = instance("a", {state: "rebooting"});
    expect(instanceState(newer)).toBe("unknown");
    expect(instanceState(instance("b", {state: "toString"}))).toBe("unknown");
    expect(stateLabel(newer)).toBe("Unknown");
    expect(activity(newer)).toEqual({text: "Reload the page to see what it does", tone: "muted"});
    expect(sortInstances([newer, instance("b", {state: "stopped", connection: "stopped"})]).map((i) => i.id)).toEqual(["b", "a"]);
    expect(countFilters([newer])).toEqual({all: 1, "needs-you": 0, working: 0, unread: 0, offline: 0});
  });

  test("rows that need you come first, then by name", () => {
    const rows = sortInstances([
      instance("idle", {name: "b"}),
      instance("gone", {state: "stopped", connection: "stopped"}),
      instance("asks", {state: "needs-you", summary: summary({attention: "request"})}),
      instance("busy", {state: "working", summary: summary({status: "running"})}),
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
    expect(chatURL("/chat", "https://hub.vpn:3290", "dev-1:3284")).toBe("/chat/embed?url=https%3A%2F%2Fhub.vpn%3A3290%2Fi%2Fdev-1%253A3284");
    expect(chatURL("/chat", "https://hub.vpn:3290", "dev-1:3284", true)).toBe(
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
      instance("c", {group: "Clients", state: "needs-you", summary: summary({attention: "request"})}),
      instance("d", {group: "Work", state: "working", summary: summary({status: "running"})}),
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
      instance("a", {state: "needs-you", summary: summary({attention: "request"})}),
      instance("b", {state: "working", summary: summary({status: "running"})}),
      instance("c", {state: "unread", summary: summary({unread: true})}),
      instance("d", {state: "unreachable", connection: "unreachable"}),
      instance("e", {state: "stopped", connection: "stopped"}),
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
    const asked = instance("a", {state: "needs-you", summary: summary({attention: "request", pending_requests: 1, request: {id: "r1", kind: "approval", title: "Run: make release", created_at: "2026-10-26T10:00:00Z"}})});
    expect(activity(asked)).toEqual({label: "Approval", text: "Run: make release", tone: "needs", since: "2026-10-26T10:00:00Z"});
    expect(activity(instance("b", {state: "needs-you", summary: summary({attention: "login"})}))).toMatchObject({label: "Login", text: "Log in to continue"});
    const working = instance("c", {state: "working", summary: summary({status: "running", task: {id: 1, status: "running", prompt: "Migrate invoices", started_at: "2026-10-26T10:00:00Z", errors: [], steered: []}})});
    expect(activity(working)).toEqual({label: "Working", text: "Migrate invoices", tone: "working", since: "2026-10-26T10:00:00Z"});
    expect(activity(instance("d", {state: "unreachable", connection: "unreachable", error: "connection refused"})).tone).toBe("fault");
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

  test("a muted instance waits out of the way until what it waits for changes", () => {
    const login = summary({attention: "login", login_required: {message: "Codex is not logged in."} as Summary["login_required"]});
    const muted = instance("a", {state: "muted", summary: login, muted: "login"});
    expect(stateLabel(muted)).toBe("Muted · Needs login");
    expect(activity(muted)).toMatchObject({label: "Muted", text: "Codex is not logged in.", tone: "muted"});
    expect(countFilters([muted])["needs-you"]).toBe(0);
    // Sorted after the idle ones.
    expect(sortInstances([muted, instance("b")]).map((i) => i.id)).toEqual(["b", "a"]);
  });

  const spending = (day_usd: number, week_usd: number, scope: string, updated_at = "2026-10-26T00:00:00Z") =>
    summary({spend: {day_usd, week_usd, scope, updated_at}});

  test("spend: totals of the instances that report one, and short amounts", () => {
    const totals = spendTotals([instance("a", {summary: spending(1.5, 10, "s1")}), instance("b", {summary: spending(100, 300, "s2")}), instance("c")]);
    expect(totals).toEqual({day: 101.5, week: 310, counted: 2});
    expect([0, 0.004, 4.2, 109.77, 452.31, 1234, 98_765, 1_500_000].map(formatUSD)).toEqual(["$0", "$0", "$4.20", "$110", "$452", "$1.2k", "$99k", "$1.5M"]);
  });

  test("spend: AgentAPIs that add up the same sessions count once, with the most recent figure", () => {
    const rows = [
      instance("a", {summary: spending(10, 50, "same", "2026-10-26T10:00:00Z")}),
      instance("b", {summary: spending(11, 51, "same", "2026-10-26T10:01:00Z")}),
      instance("c", {summary: spending(1, 2, "other")}),
    ];
    expect(spendTotals(rows)).toEqual({day: 12, week: 53, counted: 3});
    expect(countedSpends(rows).map(({instance}) => instance.id)).toEqual(["b", "c"]);
    // Older AgentAPIs say no scope: each counts on its own.
    const older = summary({spend: {day_usd: 2, week_usd: 4} as Spend});
    expect(spendTotals([instance("d", {summary: older}), instance("e", {summary: older})])).toEqual({day: 4, week: 8, counted: 2});
  });

  test("spend: an instance the hub doesn't follow now shows its last figure, which doesn't count", () => {
    const rows = [
      instance("a", {summary: spending(10, 50, "s1")}),
      instance("b", {state: "stopped", connection: "stopped", summary: spending(20, 60, "s2")}),
      instance("c", {state: "unreachable", connection: "unreachable", summary: spending(30, 70, "s3")}),
      instance("d", {state: "connecting", connection: "connecting", summary: spending(40, 80, "s4")}),
      // A stale figure doesn't stand in for a scope's current one either.
      instance("e", {state: "stopped", connection: "stopped", summary: spending(99, 99, "s1", "2026-10-27T00:00:00Z")}),
    ];
    expect(spendTotals(rows)).toEqual({day: 10, week: 50, counted: 1});
    expect(countedSpends(rows).map(({instance}) => instance.id)).toEqual(["a"]);
    expect(currentSpend(rows[1])).toBeUndefined();
    expect(spendTitle(20, 60, true)).toStartWith("Last known, not counted: $20 in the last 24 hours");
  });

  // An instance whose summary and events say it runs version, on host.
  const running = (id: string, version: string, overrides: Partial<HubInstance> = {}): HubInstance =>
    instance(id, {server_id: "s", agentapi_version: version, hostname: "dev-1", summary: summary({instance: {id, server_id: "s", agent_type: "claude", transport: "native", agentapi_version: version}}), ...overrides});

  test("versions: releases compare by number; development builds compare with nothing", () => {
    expect(parseVersion("2.1.0")).toEqual([2, 1, 0]);
    expect(parseVersion("v1.6.1")).toEqual([1, 6, 1]);
    for (const build of ["0.0.0-dev", "2.0.0-21-g61be34f", "2.0.0-21-g61be34f-dirty", "0.0.0", "2.1", "", undefined]) expect(parseVersion(build)).toBeUndefined();
    expect(compareVersions("1.8.1", "2.1.0")).toBeLessThan(0);
    expect(compareVersions("1.10.0", "1.9.3")).toBeGreaterThan(0);
    expect(compareVersions("2.1.0", "v2.1.0")).toBe(0);
    expect(compareVersions("0.0.0-dev", "2.1.0")).toBeUndefined();
    expect(compareVersions("2.1.0", "0.0.0-dev")).toBeUndefined();
  });

  test("versions: an instance's is its summary's, unless that summary is of an earlier run", () => {
    expect(instanceVersion(running("a", "2.0.0"))).toBe("2.0.0");
    // A summary is the instance's latest word.
    expect(instanceVersion(running("a", "2.0.0", {agentapi_version: "1.8.1"}))).toBe("2.0.0");
    // Restarted in place: the new run's event came, its first summary not yet.
    const restarted = running("a", "2.0.0", {server_id: "s2", agentapi_version: "2.1.0", connection: "connecting", state: "idle"});
    expect(instanceVersion(restarted)).toBe("2.1.0");
    expect(instanceVersion(instance("b", {agentapi_version: "1.6.1", summary: null, connection: "connecting", state: "connecting"}))).toBe("1.6.1");
    expect(instanceVersion(running("c", "1.8.1", {server_id: undefined, agentapi_version: undefined}))).toBe("1.8.1");
    expect(instanceVersion(instance("d"))).toBe("");
  });

  test("versions: older than the hub, only when both are releases and the hub follows it", () => {
    expect(olderThanHub(running("a", "1.6.1"), "2.1.0")).toBe(true);
    expect(olderThanHub(running("a", "2.1.0"), "2.1.0")).toBe(false);
    expect(olderThanHub(running("a", "2.2.0"), "2.1.0")).toBe(false);
    expect(olderThanHub(running("a", "0.0.0-dev"), "2.1.0")).toBe(false);
    expect(olderThanHub(running("a", "1.6.1"), "0.0.0-dev")).toBe(false);
    expect(olderThanHub(running("a", "1.6.1"), undefined)).toBe(false);
    expect(olderThanHub(running("a", "1.6.1", {connection: "stopped", state: "stopped"}), "2.1.0")).toBe(false);
    expect(versionTitle("1.6.1", "2.1.0")).toBe("AgentAPI v1.6.1, older than the hub (v2.1.0)");
    expect(versionTitle("0.0.0-dev", "2.1.0")).toBe("AgentAPI v0.0.0-dev, a development build");
    expect(versionTitle("1.6.1", "2.1.0", true)).toBe("Last known: AgentAPI v1.6.1");
  });

  test("versions: the overview counts each version of the instances the hub follows, and those older than the hub", () => {
    const rows = [
      running("a", "1.6.1", {hostname: "dev-2"}),
      running("b", "1.6.1", {hostname: "dev-1"}),
      running("c", "1.6.1", {hostname: "dev-2"}),
      running("d", "2.1.0"),
      running("e", "1.8.1", {hostname: "dev-3"}),
      running("f", "0.0.0-dev"),
      running("g", "1.10.0"),
      instance("h"),
      // Not followed now: its version is the last it ran.
      running("i", "1.2.0", {connection: "stopped", state: "stopped"}),
    ];
    const totals = versionTotals(rows, "2.1.0");
    expect(totals.versions.map((v) => [v.version, v.relation, v.instances, v.machines])).toEqual([
      ["2.1.0", "same", 1, ["dev-1"]],
      ["1.10.0", "older", 1, ["dev-1"]],
      ["1.8.1", "older", 1, ["dev-3"]],
      ["1.6.1", "older", 3, ["dev-1", "dev-2"]],
      ["0.0.0-dev", "unknown", 1, ["dev-1"]],
      ["", "unknown", 1, []],
    ]);
    expect(totals.older).toBe(5);
    expect(totals.counted).toBe(8);
    // A development build of the hub tells no instance older.
    expect(versionTotals(rows, "0.0.0-dev").older).toBe(0);
  });

  test("search finds the instances of a version", () => {
    const old = running("a", "1.6.1");
    expect(matchesSearch(old, "1.6.1")).toBe(true);
    expect(matchesSearch(old, "v1.6.1")).toBe(true);
    expect(matchesSearch(old, "1.8.1")).toBe(false);
  });
});
