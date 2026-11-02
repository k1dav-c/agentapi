import {describe, expect, test} from "bun:test";
import {groupInstances, type HubInstance, type Summary} from "./hub";
import {changedAt, listSections, movePin, parseListOrder, pinnedKey, pinOf, recentKey, sortPinned, sortRecent, type ListSection} from "./hub-order";

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

describe("listSections", () => {
  const rows = [
    changed("checkout", 2, {group: "Clients", state: "needs-you", summary: summary({attention: "request", title: "Deploy"})}),
    changed("billing", 8, {group: "Internal", state: "working", summary: summary({status: "running", title: "Invoices"})}),
    changed("docs", 5, {state: "unread", summary: summary({unread: true, title: "Deploy notes"})}),
    changed("legacy", 9, {group: "Internal", state: "unreachable", connection: "unreachable"}),
    changed("site", 1, {group: "Clients"}),
  ];
  // Pinned: billing on top, then docs.
  const pinned = rows.map((row) => (row.id === "billing" ? {...row, pin: 1} : row.id === "docs" ? {...row, pin: 2} : row));
  const shape = (sections: ListSection[]) => sections.map((section) => [section.label, ids(section.instances)]);

  test("groups: what groupInstances makes of what passes, each with a header once there is a group", () => {
    const sections = listSections(rows, {filter: "all", query: "", order: "groups"});
    expect(sections.map(({name, instances, needsYou}) => ({name, instances, needsYou}))).toEqual(groupInstances(rows));
    expect(sections.map((section) => [section.key, section.label])).toEqual([
      ["Clients", "Clients"],
      ["Internal", "Internal"],
      ["", "Ungrouped"],
    ]);
  });

  test("groups: no group, no headers", () => {
    const ungrouped = rows.map((row) => ({...row, group: undefined}));
    expect(shape(listSections(ungrouped, {filter: "all", query: "", order: "groups"}))).toEqual([["", ["checkout", "billing", "docs", "site", "legacy"]]]);
  });

  test("recent: one block with no header, by the latest change", () => {
    const sections = listSections(rows, {filter: "all", query: "", order: "recent"});
    expect(sections.map((section) => [section.key, section.label, section.name, ids(section.instances), section.needsYou])).toEqual([
      [recentKey, "", "", ["legacy", "billing", "docs", "checkout", "site"], 1],
    ]);
  });

  test("the filter and the search apply to both orders", () => {
    expect(listSections(rows, {filter: "offline", query: "", order: "recent"}).map((section) => ids(section.instances))).toEqual([["legacy"]]);
    expect(listSections(rows, {filter: "all", query: "deploy", order: "recent"}).map((section) => ids(section.instances))).toEqual([["docs", "checkout"]]);
    expect(shape(listSections(rows, {filter: "all", query: "deploy", order: "groups"}))).toEqual([
      ["Clients", ["checkout"]],
      ["Ungrouped", ["docs"]],
    ]);
    expect(listSections(rows, {filter: "working", query: "deploy", order: "recent"})).toEqual([]);
    expect(listSections(rows, {filter: "working", query: "deploy", order: "groups"})).toEqual([]);
  });

  test("groups: the pinned ones first, in their order, and not again in their groups", () => {
    const sections = listSections(pinned, {filter: "all", query: "", order: "groups"});
    expect(sections.map((section) => [section.key, section.label, ids(section.instances)])).toEqual([
      [pinnedKey, "Pinned", ["billing", "docs"]],
      ["Clients", "Clients", ["checkout", "site"]],
      ["Internal", "Internal", ["legacy"]],
    ]);
    expect(sections[0]).toMatchObject({name: "", pinned: true, needsYou: 0});
    expect(sections.slice(1).some((section) => section.pinned)).toBe(false);
  });

  test("recent: the pinned ones first, then the others by their latest change, under a header of their own", () => {
    expect(shape(listSections(pinned, {filter: "all", query: "", order: "recent"}))).toEqual([
      ["Pinned", ["billing", "docs"]],
      ["Recent changes", ["legacy", "checkout", "site"]],
    ]);
  });

  test("pinned ones and no group: the others under Ungrouped", () => {
    const ungrouped = pinned.map((row) => ({...row, group: undefined}));
    expect(shape(listSections(ungrouped, {filter: "all", query: "", order: "groups"}))).toEqual([
      ["Pinned", ["billing", "docs"]],
      ["Ungrouped", ["checkout", "site", "legacy"]],
    ]);
  });

  test("the pinned ones' section counts what needs you there", () => {
    const asks = pinned.map((row) => (row.id === "checkout" ? {...row, pin: 3} : row));
    const [top] = listSections(asks, {filter: "all", query: "", order: "groups"});
    expect([ids(top.instances), top.needsYou]).toEqual([["billing", "docs", "checkout"], 1]);
  });

  test("the filter and the search apply to the pinned ones too; a section none passes isn't there", () => {
    expect(shape(listSections(pinned, {filter: "working", query: "", order: "groups"}))).toEqual([["Pinned", ["billing"]]]);
    expect(shape(listSections(pinned, {filter: "all", query: "deploy", order: "recent"}))).toEqual([
      ["Pinned", ["docs"]],
      ["Recent changes", ["checkout"]],
    ]);
    // No pinned one passes: the list as it is without pins.
    expect(shape(listSections(pinned, {filter: "offline", query: "", order: "recent"}))).toEqual([["", ["legacy"]]]);
    expect(shape(listSections(pinned, {filter: "needs-you", query: "", order: "groups"}))).toEqual([["Clients", ["checkout"]]]);
    expect(listSections(pinned, {filter: "all", query: "nothing like it", order: "groups"})).toEqual([]);
  });
});

describe("pinOf", () => {
  test("a place from 1; none for anything else", () => {
    expect(pinOf(instance("a", {pin: 1}))).toBe(1);
    expect(pinOf(instance("a", {pin: 12}))).toBe(12);
    for (const pin of [undefined, 0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY, "2" as unknown as number, null as unknown as number]) {
      expect(pinOf(instance("a", {pin}))).toBeUndefined();
    }
  });
});

describe("sortPinned", () => {
  test("the pinned ones by place, without the others", () => {
    const rows = [instance("c", {pin: 3}), instance("free"), instance("a", {pin: 1}), instance("odd", {pin: 0}), instance("b", {pin: 2})];
    expect(ids(sortPinned(rows))).toEqual(["a", "b", "c"]);
  });

  test("gaps and shared places, as while the hub's events arrive: by place, then by ID", () => {
    const rows = [instance("z", {pin: 2}), instance("y", {pin: 7}), instance("x", {pin: 2}), instance("w", {pin: 5})];
    expect(ids(sortPinned(rows))).toEqual(["x", "z", "w", "y"]);
  });

  test("one forgotten leaves no trace: the others keep their order", () => {
    const rows = [instance("a", {pin: 1}), instance("b", {pin: 2}), instance("c", {pin: 3})];
    expect(ids(sortPinned(rows.filter((row) => row.id !== "b")))).toEqual(["a", "c"]);
  });
});

describe("movePin", () => {
  const order = ["a", "b", "c"];

  test("up and down past the next one", () => {
    expect(movePin(order, order, "b", -1)).toEqual(["b", "a", "c"]);
    expect(movePin(order, order, "b", 1)).toEqual(["a", "c", "b"]);
    expect(movePin(order, order, "c", -1)).toEqual(["a", "c", "b"]);
    expect(movePin(order, order, "a", 1)).toEqual(["b", "a", "c"]);
  });

  test("not past either end", () => {
    expect(movePin(order, order, "a", -1)).toBeUndefined();
    expect(movePin(order, order, "c", 1)).toBeUndefined();
    expect(movePin(["a"], ["a"], "a", -1)).toBeUndefined();
    expect(movePin(["a"], ["a"], "a", 1)).toBeUndefined();
  });

  test("past the next one the list shows; the hidden ones keep their places among the rest", () => {
    // A filter hides b.
    expect(movePin(order, ["a", "c"], "c", -1)).toEqual(["c", "a", "b"]);
    expect(movePin(order, ["a", "c"], "a", 1)).toEqual(["b", "c", "a"]);
    // Only hidden ones above it: the top of what shows.
    expect(movePin(order, ["c"], "c", -1)).toBeUndefined();
    expect(movePin(order, ["b", "c"], "b", -1)).toBeUndefined();
  });

  test("an instance that isn't pinned, or doesn't show, or a list with one gone: none, or around it", () => {
    expect(movePin(order, order, "d", -1)).toBeUndefined();
    expect(movePin(order, ["a", "c"], "b", 1)).toBeUndefined();
    expect(movePin([], [], "a", 1)).toBeUndefined();
    // One that left the list (forgotten) since: neither shown nor in the way.
    expect(movePin(["a", "gone", "c"], ["a", "c"], "c", -1)).toEqual(["c", "a", "gone"]);
  });

  test("the order it returns has each pinned one once", () => {
    for (const id of order) {
      for (const step of [-1, 1] as const) {
        const moved = movePin(order, order, id, step);
        if (moved) expect([...moved].sort()).toEqual(order);
      }
    }
  });
});
