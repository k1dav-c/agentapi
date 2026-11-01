import {describe, expect, test} from "bun:test";
import {groupInstances, type HubInstance, type Summary} from "./hub";
import {changedAt, listGroups, parseListOrder, sortRecent} from "./hub-order";

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
  last_seen: "2026-11-01T08:00:00Z",
  connection: "connected",
  summary: summary(),
  state: "idle",
  ...overrides,
});

// An instance the hub recorded a change of at minute m past 9:00.
const changed = (id: string, m: number, overrides: Partial<HubInstance> = {}) =>
  instance(id, {changed_at: `2026-11-01T09:${String(m).padStart(2, "0")}:00Z`, ...overrides});

const ids = (rows: HubInstance[]) => rows.map((row) => row.id);

describe("parseListOrder", () => {
  test("the recent order, as a browser stored it", () => {
    expect(parseListOrder("recent")).toBe("recent");
    expect(parseListOrder("groups")).toBe("groups");
  });

  test("nothing stored, or anything else, is the groups", () => {
    for (const stored of [null, undefined, "", " recent", "Recent", '"recent"', "flat", "null", "undefined", "[]"]) {
      expect(parseListOrder(stored)).toBe("groups");
    }
  });
});

describe("changedAt", () => {
  test("when the hub saw it change", () => {
    expect(changedAt(changed("a", 5))).toBe("2026-11-01T09:05:00Z");
  });

  test("from a hub that recorded none yet: since when the row says what it does, else when it was last heard of", () => {
    const working = instance("a", {
      state: "working",
      summary: summary({status: "running", task: {id: 1, status: "running", prompt: "Migrate invoices", started_at: "2026-11-01T09:10:00Z", errors: [], steered: []}}),
    });
    expect(changedAt(working)).toBe("2026-11-01T09:10:00Z");
    // An idle one with no task yet says no since.
    expect(changedAt(instance("b"))).toBe("2026-11-01T08:00:00Z");
    expect(changedAt(instance("c", {state: "stopped", connection: "stopped", last_seen: "2026-11-01T07:00:00Z"}))).toBe("2026-11-01T07:00:00Z");
  });

  test("a time that doesn't parse, or Go's zero time, doesn't count", () => {
    expect(changedAt(instance("a", {changed_at: "soon", last_seen: "2026-11-01T08:00:00Z"}))).toBe("2026-11-01T08:00:00Z");
    expect(changedAt(instance("b", {changed_at: "0001-01-01T00:00:00Z", last_seen: "0001-01-01T00:00:00Z"}))).toBeUndefined();
  });
});

describe("sortRecent", () => {
  test("the latest change first, whatever the state or the group", () => {
    const rows = sortRecent([
      changed("asks", 1, {state: "needs-you", group: "Clients", summary: summary({attention: "request"})}),
      changed("idle", 7),
      changed("gone", 9, {state: "stopped", connection: "stopped"}),
      changed("busy", 3, {state: "working", group: "Internal", summary: summary({status: "running"})}),
    ]);
    expect(ids(rows)).toEqual(["gone", "idle", "busy", "asks"]);
  });

  test("an instance with no record of a change falls in by its since, or when it was last heard of", () => {
    const finished = instance("finished", {
      state: "unread",
      summary: summary({unread: true, task: {id: 2, status: "completed", prompt: "Fix", started_at: "2026-11-01T09:00:00Z", ended_at: "2026-11-01T09:04:00Z", errors: [], steered: []}}),
    });
    const heard = instance("heard", {last_seen: "2026-11-01T09:06:00Z"});
    expect(ids(sortRecent([changed("a", 2), finished, changed("b", 5), heard, changed("c", 8)]))).toEqual(["c", "heard", "b", "finished", "a"]);
  });

  test("ties go by name, then ID; those with no time at all come last", () => {
    const never = {last_seen: "0001-01-01T00:00:00Z"};
    const rows = sortRecent([
      instance("z-none", {name: "alpha", ...never}),
      changed("b2", 4, {name: "beta"}),
      changed("b1", 4, {name: "beta"}),
      changed("a", 4, {name: "alpha"}),
      instance("y-none", {name: "alpha", ...never}),
      changed("c", 1),
    ]);
    expect(ids(rows)).toEqual(["a", "b1", "b2", "c", "y-none", "z-none"]);
  });

  test("the same instances come out in the same order, whatever order they come in", () => {
    const rows = [changed("a", 3), changed("b", 3), instance("c"), changed("d", 9), changed("e", 1, {name: "a"})];
    const once = ids(sortRecent(rows));
    expect(ids(sortRecent([...rows].reverse()))).toEqual(once);
    expect(ids(sortRecent([rows[2], rows[4], rows[0], rows[3], rows[1]]))).toEqual(once);
  });
});

describe("listGroups", () => {
  const rows = [
    changed("checkout", 2, {group: "Clients", state: "needs-you", summary: summary({attention: "request", title: "Deploy"})}),
    changed("billing", 8, {group: "Internal", state: "working", summary: summary({status: "running", title: "Invoices"})}),
    changed("docs", 5, {state: "unread", summary: summary({unread: true, title: "Deploy notes"})}),
    changed("legacy", 9, {group: "Internal", state: "unreachable", connection: "unreachable"}),
    changed("site", 1, {group: "Clients"}),
  ];

  test("groups: what groupInstances makes of what passes", () => {
    expect(listGroups(rows, {filter: "all", query: "", order: "groups"})).toEqual(groupInstances(rows));
  });

  test("recent: one block with no name, by the latest change", () => {
    const groups = listGroups(rows, {filter: "all", query: "", order: "recent"});
    expect(groups.map((group) => [group.name, ids(group.instances), group.needsYou])).toEqual([["", ["legacy", "billing", "docs", "checkout", "site"], 1]]);
  });

  test("the filter and the search apply to both orders", () => {
    expect(listGroups(rows, {filter: "offline", query: "", order: "recent"}).map((group) => ids(group.instances))).toEqual([["legacy"]]);
    expect(listGroups(rows, {filter: "all", query: "deploy", order: "recent"}).map((group) => ids(group.instances))).toEqual([["docs", "checkout"]]);
    expect(listGroups(rows, {filter: "all", query: "deploy", order: "groups"}).map((group) => [group.name, ids(group.instances)])).toEqual([
      ["Clients", ["checkout"]],
      ["", ["docs"]],
    ]);
    expect(listGroups(rows, {filter: "working", query: "deploy", order: "recent"})).toEqual([]);
    expect(listGroups(rows, {filter: "working", query: "deploy", order: "groups"})).toEqual([]);
  });
});
