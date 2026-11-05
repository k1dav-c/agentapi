import {describe, expect, test} from "bun:test";
import {activity, agentLabel, agentOf, agentsIn, countAgents, matchesAgents, nothingMatches, parseAgents, shownAgents, toggleAgent, compareVersions, countedSpends, countedTokens, currentSpend, formatTokens, formatUSD, tokenTotals, tokensTitle, instanceVersion, olderThanHub, parseVersion, spendTitle, spendTotals, shortError, applyHubEvent, chatURL, countFilters, formatAgo, formatElapsed, matchesSearch, activeAgent, groupByMachine, groupInstances, sameMachine, tabOrder, groupNames, hubRoot, machineOf, machineSubtotals, sharedVersion, instanceState, needsYou, sortInstances, stateLabel, usagePercent, versionTitle, versionTotals, formatWork, workSeconds, workTitle, workTotals, type HubInstance, type Spend, type Summary} from "./hub";

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

  // A spend that says its tokens: each kind n, n*10 in the week.
  const tokens = (n: number) => ({input: n, output: 2 * n, cache_write: 3 * n, cache_read: 4 * n, total: 10 * n});
  const using = (day: number, scope: string, updated_at = "2026-10-26T00:00:00Z") =>
    summary({spend: {day_usd: day, week_usd: 10 * day, scope, updated_at, day_tokens: tokens(day), week_tokens: tokens(10 * day)}});

  test("tokens: totals of each kind, each scope once, of the instances that report them", () => {
    const rows = [
      instance("a", {summary: using(1, "same", "2026-10-26T10:00:00Z")}),
      instance("b", {summary: using(2, "same", "2026-10-26T10:01:00Z")}),
      instance("c", {summary: using(5, "other")}),
      // An AgentAPI before 2.4: a spend, no tokens.
      instance("d", {summary: spending(3, 30, "older")}),
      instance("e", {state: "stopped", connection: "stopped", summary: using(100, "gone")}),
      instance("f"),
    ];
    expect(tokenTotals(rows)).toEqual({day: tokens(7), week: tokens(70), counted: 3});
    expect(countedTokens(rows).map(({instance}) => instance.id)).toEqual(["b", "c"]);
    // Of one scope, a figure without tokens doesn't stand in for one with them.
    const mixed = [instance("g", {summary: using(1, "s")}), instance("h", {summary: spending(9, 9, "s", "2026-10-27T00:00:00Z")})];
    expect(tokenTotals(mixed)).toEqual({day: tokens(1), week: tokens(10), counted: 1});
    expect(tokenTotals([])).toEqual({day: tokens(0), week: tokens(0), counted: 0});
  });

  test("tokens: short counts and titles", () => {
    expect([0, 7.4, 950, 999, 1000, 1234, 9_949, 9_960, 12_345, 999_499, 999_999, 3_412_000, 45_600_000, 1_234_000_000, 2e15].map(formatTokens)).toEqual([
      "0", "7", "950", "999", "1k", "1.2k", "9.9k", "10k", "12k", "999k", "1M", "3.4M", "46M", "1.2B", "2000T",
    ]);
    expect(tokensTitle(3_400_000, 21_000_000)).toBe("3.4M tokens in the last 24 hours, 21M in the last 7 days");
    expect(spendTitle(4.2, 30, false, 3_400_000)).toBe("$4.20 in the last 24 hours, $30 in the last 7 days (API-equivalent, at list prices; a subscription doesn't charge it); 3.4M tokens in the last 24 hours");
    expect(spendTitle(4.2, 30, true, 950)).toStartWith("Last known, not counted: $4.20");
    expect(spendTitle(4.2, 30, true, 950)).toEndWith("; 950 tokens in the last 24 hours");
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

const workNow = Date.parse("2026-11-03T12:00:00Z");

const worker = (id: string, work?: HubInstance["work"]): HubInstance => ({
  id,
  url: `http://${id}`,
  source: "webhook",
  last_seen: "2026-11-03T00:00:00Z",
  connection: "connected",
  state: work?.working_since ? "working" : "idle",
  work,
});

describe("work time", () => {
  test("formats compactly, rounded down", () => {
    expect(formatWork(0)).toBe("0m");
    expect(formatWork(59)).toBe("<1m");
    expect(formatWork(60)).toBe("1m");
    expect(formatWork(45 * 60 + 59)).toBe("45m");
    expect(formatWork(3600)).toBe("1h");
    expect(formatWork(2.36 * 3600)).toBe("2.3h");
    expect(formatWork(10 * 3600 - 1)).toBe("9.9h");
    expect(formatWork(31 * 3600 + 3599)).toBe("31h");
    expect(workTitle(2.5 * 3600, 31 * 3600)).toBe("Worked 2.5h in the last 24 hours, 31h in the last 7 days (as the hub saw it)");
  });

  test("adds the span going on to what the hub sent, within each window", () => {
    expect(workSeconds(worker("old"), workNow)).toEqual({day: 0, week: 0});
    const done = worker("a", {last_24h_seconds: 600, last_7d_seconds: 7200});
    expect(workSeconds(done, workNow)).toEqual({day: 600, week: 7200});
    const running = worker("b", {last_24h_seconds: 600, last_7d_seconds: 600, working_since: "2026-11-03T11:30:00Z"});
    expect(workSeconds(running, workNow)).toEqual({day: 600 + 1800, week: 600 + 1800});
    expect(workSeconds(running, workNow + 60_000)).toEqual({day: 600 + 1860, week: 600 + 1860});
    // Working for two days: the day window holds 24 hours of it.
    const long = worker("c", {last_24h_seconds: 0, last_7d_seconds: 0, working_since: "2026-11-01T12:00:00Z"});
    expect(workSeconds(long, workNow)).toEqual({day: 86400, week: 2 * 86400});
    // A browser clock behind the hub's adds nothing, not less.
    expect(workSeconds(worker("d", {last_24h_seconds: 5, last_7d_seconds: 5, working_since: "2026-11-03T12:01:00Z"}), workNow)).toEqual({day: 5, week: 5});
  });

  test("totals each instance's own time, and how many worked this week", () => {
    const rows = [
      worker("a", {last_24h_seconds: 600, last_7d_seconds: 7200}),
      worker("b", {last_24h_seconds: 0, last_7d_seconds: 0, working_since: "2026-11-03T11:00:00Z"}),
      worker("c", {last_24h_seconds: 0, last_7d_seconds: 0}),
      worker("d"),
    ];
    expect(workTotals(rows, workNow)).toEqual({day: 600 + 3600, week: 7200 + 3600, counted: 2});
  });
});

describe("agent filter", () => {
  const rows = [
    instance("a", {agent_type: "claude", state: "working"}),
    instance("b", {agent_type: "codex", state: "working"}),
    instance("c", {agent_type: "codex"}),
    instance("d", {agent_type: "pi", state: "stopped", connection: "stopped"}),
    instance("e", {agent_type: "aider"}),
    instance("f", {agent_type: "custom"}),
    instance("g"),
  ];

  test("Claude Code, Codex and Pi by type; any other agent, or none reported, is other", () => {
    expect(rows.map(agentOf)).toEqual(["claude", "codex", "codex", "pi", "other", "other", "other"]);
    expect(agentOf(instance("x", {agent_type: "toString"}))).toBe("other");
    expect(["claude", "codex", "pi", "other"].map((agent) => agentLabel(agent as never))).toEqual(["Claude Code", "Codex", "Pi", "Other"]);
  });

  test("none picked is every agent; the ones picked, any of them", () => {
    expect(rows.filter((row) => matchesAgents(row, new Set())).length).toBe(7);
    expect(rows.filter((row) => matchesAgents(row, new Set(["claude", "codex"]))).map((row) => row.id)).toEqual(["a", "b", "c"]);
    expect(rows.filter((row) => matchesAgents(row, new Set(["other"]))).map((row) => row.id)).toEqual(["e", "f", "g"]);
  });

  test("the agents the instances run, in the chips' order", () => {
    expect(agentsIn([...rows].reverse())).toEqual(["claude", "codex", "pi", "other"]);
    expect(agentsIn([instance("x", {agent_type: "pi"}), instance("y", {agent_type: "claude"})])).toEqual(["claude", "pi"]);
    expect(agentsIn([])).toEqual([]);
  });

  test("the list is filtered by the agents picked it has, and by none while it has one agent", () => {
    expect(shownAgents(new Set(["codex", "pi"]), ["claude", "codex"])).toEqual(new Set(["codex"]));
    expect(shownAgents(new Set(["pi"]), ["claude", "codex"])).toEqual(new Set());
    expect(shownAgents(new Set(["claude"]), ["claude"])).toEqual(new Set());
    expect(shownAgents(new Set(["claude"]), [])).toEqual(new Set());
  });

  test("each agent's count is of the state filtered", () => {
    expect(countAgents(rows, "all")).toEqual({claude: 1, codex: 2, pi: 1, other: 3});
    expect(countAgents(rows, "working")).toEqual({claude: 1, codex: 1, pi: 0, other: 0});
    expect(countAgents(rows, "offline")).toEqual({claude: 0, codex: 0, pi: 1, other: 0});
    // The state chips count the agents picked: with countFilters of those.
    expect(countFilters(rows.filter((row) => matchesAgents(row, new Set(["codex"]))))).toEqual({all: 2, "needs-you": 0, working: 1, unread: 0, offline: 0});
  });

  test("a chip picks its agent, and takes it out again", () => {
    const picked = new Set(["claude"] as const);
    expect(toggleAgent(picked, "codex")).toEqual(new Set(["claude", "codex"]));
    expect(toggleAgent(picked, "claude")).toEqual(new Set());
    expect(picked).toEqual(new Set(["claude"]));
  });

  test("what the browser kept: the known agents once each, anything else none", () => {
    expect(parseAgents(["pi", "codex", "pi"])).toEqual(new Set(["codex", "pi"]));
    expect(parseAgents(["claude", "aider", 3, null, {}])).toEqual(new Set(["claude"]));
    for (const bad of [null, undefined, "claude", 42, {claude: true}, true]) expect(parseAgents(bad)).toEqual(new Set());
  });

  test("an empty list says which filters left nothing", () => {
    const none = new Set<never>();
    expect(nothingMatches("  deploy ", "all", none)).toBe("No agent matches “deploy”");
    expect(nothingMatches("deploy", "working", none)).toBe("No agent matches “deploy” with these filters");
    expect(nothingMatches("deploy", "all", new Set(["pi"]))).toBe("No agent matches “deploy” with these filters");
    expect(nothingMatches("", "working", none)).toBe("No agent is in this state right now");
    expect(nothingMatches("", "working", new Set(["pi", "claude"]))).toBe("No Claude Code or Pi agent is in this state right now");
    expect(nothingMatches("", "unread", new Set(["other", "codex", "claude"]))).toBe("No Claude Code, Codex or other agent is in this state right now");
    expect(nothingMatches("", "all", new Set(["pi"]))).toBe("No Pi agent is here right now");
  });
});

describe("machines", () => {
  test("an instance's machine is its host name; none when it doesn't say", () => {
    expect(machineOf(instance("a", {hostname: "dev-1"}))).toBe("dev-1");
    expect(machineOf(instance("a", {hostname: " dev-1 "}))).toBe("dev-1");
    expect(machineOf(instance("a"))).toBe("");
    expect(machineOf(instance("a", {hostname: "  "}))).toBe("");
  });

  test("each machine's agents together, what needs you first, then by name; one that names no machine is one of its own", () => {
    const rows = [
      instance("dev-1:1", {name: "api", hostname: "dev-1", state: "idle"}),
      instance("dev-2:1", {name: "site", hostname: "dev-2", state: "working"}),
      instance("dev-1:2", {name: "api", hostname: "dev-1", state: "needs-you", summary: summary({attention: "request"})}),
      instance("dev-2:2", {name: "site", hostname: "dev-2", state: "unread"}),
      instance("x", {name: "lost", state: "working"}),
      instance("y", {name: "found", state: "working"}),
      instance("dev-3:1", {name: "docs", hostname: "dev-3", state: "working"}),
    ];
    expect(groupByMachine(rows).map((machine) => [machine.key, machine.hostname, machine.instances.map((row) => row.id)])).toEqual([
      ["host dev-1", "dev-1", ["dev-1:2", "dev-1:1"]],
      ["host dev-2", "dev-2", ["dev-2:1", "dev-2:2"]],
      ["host dev-3", "dev-3", ["dev-3:1"]],
      ["instance y", "", ["y"]],
      ["instance x", "", ["x"]],
    ]);
    expect(groupByMachine([])).toEqual([]);
  });

  test("by machine: the most this week first, without those with none; none while there is one machine", () => {
    const on = (hostname: string | undefined, day: number, week: number) => ({instance: instance(`${hostname}:${day}:${week}`, {hostname}), day, week});
    expect(
      machineSubtotals([on("dev-1", 1, 4), on("dev-2", 2, 9), on("dev-1", 0, 3), on("dev-3", 0, 0), on(undefined, 1, 7)]),
    ).toEqual([
      {name: "dev-2", day: 2, week: 9},
      {name: "dev-1", day: 1, week: 7},
      {name: "", day: 1, week: 7},
    ]);
    expect(machineSubtotals([on("dev-1", 1, 4), on("dev-1", 2, 3)])).toEqual([]);
    expect(machineSubtotals([on(undefined, 1, 4)])).toEqual([]);
    expect(machineSubtotals([])).toEqual([]);
  });

  test("by machine: past the limit, the rest add up in one", () => {
    const rows = [1, 2, 3, 4, 5].map((n) => ({instance: instance(`dev-${n}:1`, {hostname: `dev-${n}`}), day: n, week: n * 10}));
    expect(machineSubtotals(rows, 3)).toEqual([
      {name: "dev-5", day: 5, week: 50},
      {name: "dev-4", day: 4, week: 40},
      {name: "", day: 6, week: 60, others: 3},
    ]);
    expect(machineSubtotals(rows, 5)).toHaveLength(5);
  });

  test("by machine: spend adds up as the totals count it, each scope once", () => {
    const spending = (id: string, hostname: string, scope: string, week: number, at: string) =>
      instance(id, {hostname, summary: summary({spend: {day_usd: week / 7, week_usd: week, scope, updated_at: at} as Spend})});
    const rows = [
      spending("dev-1:1", "dev-1", "s1", 7, "2026-11-01T00:00:00Z"),
      spending("dev-1:2", "dev-1", "s1", 14, "2026-11-01T01:00:00Z"),
      spending("dev-2:1", "dev-2", "s2", 21, "2026-11-01T00:00:00Z"),
    ];
    const machines = machineSubtotals(countedSpends(rows).map(({instance, spend}) => ({instance, day: spend.day_usd, week: spend.week_usd})));
    expect(machines.map((machine) => [machine.name, machine.week])).toEqual([
      ["dev-2", 21],
      ["dev-1", 14],
    ]);
    expect(machines.reduce((sum, machine) => sum + machine.week, 0)).toBe(spendTotals(rows).week);
  });

  test("a machine's tabs: by agent, then name, whatever their states", () => {
    const rows = [
      instance("p", {agent_type: "pi", state: "needs-you", summary: summary({attention: "request"})}),
      instance("x", {agent_type: "aider"}),
      instance("c2", {agent_type: "codex", name: "b"}),
      instance("c1", {agent_type: "codex", name: "a", state: "working"}),
      instance("cl", {agent_type: "claude", state: "stopped", connection: "stopped"}),
    ];
    expect(tabOrder(rows).map((row) => row.id)).toEqual(["cl", "c1", "c2", "p", "x"]);
  });

  test("the agents of a machine, for the bar: none on a machine of one, or without one", () => {
    const rows = [
      instance("dev-1:2", {hostname: "dev-1", agent_type: "codex"}),
      instance("dev-1:1", {hostname: "dev-1", agent_type: "claude"}),
      instance("dev-2:1", {hostname: "dev-2", agent_type: "claude"}),
      instance("none", {agent_type: "claude"}),
      instance("none-2", {agent_type: "codex"}),
    ];
    expect(sameMachine(rows, rows[0]).map((row) => row.id)).toEqual(["dev-1:1", "dev-1:2"]);
    expect(sameMachine(rows, rows[2])).toEqual([]);
    expect(sameMachine(rows, rows[3])).toEqual([]);
  });

  test("the agent a card shows: the one picked, else the open one, else the one that needs you the most", () => {
    const machine = groupByMachine([
      instance("dev-1:1", {hostname: "dev-1", state: "idle"}),
      instance("dev-1:2", {hostname: "dev-1", state: "working"}),
      instance("dev-1:3", {hostname: "dev-1", state: "unread"}),
    ])[0];
    expect(activeAgent(machine)).toBe("dev-1:2");
    expect(activeAgent(machine, undefined, "dev-1:3")).toBe("dev-1:3");
    expect(activeAgent(machine, "dev-1:1", "dev-1:3")).toBe("dev-1:1");
    // Ones the card doesn't have (filtered out, or on another machine) don't count.
    expect(activeAgent(machine, "gone", "dev-2:1")).toBe("dev-1:2");
  });

  test("a machine's version, when all its agents run the same one", () => {
    const on = (version: string) => instance(version || "none", {agentapi_version: version});
    expect(sharedVersion([on("3.1.0"), on("3.1.0")])).toBe("3.1.0");
    expect(sharedVersion([on("3.1.0"), on("3.0.0")])).toBe("");
    expect(sharedVersion([on("")])).toBe("");
    expect(sharedVersion([])).toBe("");
  });
});
