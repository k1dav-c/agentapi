import {describe, expect, test} from "bun:test";
import {hubInstance, summary as baseSummary} from "./hub-fixtures";
import type {HubInstance, Summary} from "./hub";
import type {HubAgent} from "./hub-filter";
import {groupInstances} from "./hub-groups";
import {machinesInOrder} from "./hub-machines";
import {changedAt, keepMachines, listSections, movePin, parseListOrder, pinnedKey, pinOf, recentAgent, recentKey, sortPinned, sortRecent, type ListSection} from "./hub-order";

const summary = (overrides: Partial<Summary> = {}): Summary => baseSummary({seen_at: "2026-10-26T00:00:00Z", ...overrides});

// No agent picked: every agent.
const none = new Set<HubAgent>();

const instance = (id: string, overrides: Partial<HubInstance> = {}): HubInstance =>
  hubInstance(id, {last_seen: "2026-11-01T08:00:00Z", summary: summary(), ...overrides});

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
    for (const stored of [null, undefined, "", " recent", "Recent", "machines", '"recent"', "flat", "null", "undefined", "[]"]) {
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
  test("those with tasks first, then those with none or muted, each by its latest change, whatever the group", () => {
    const rows = sortRecent([
      changed("asks", 1, {state: "needs-you", group: "Clients", summary: summary({attention: "request", tasks: 2})}),
      changed("fresh", 7),
      changed("gone", 9, {state: "stopped", connection: "stopped", summary: summary({tasks: 4})}),
      changed("busy", 3, {state: "working", group: "Internal", summary: summary({status: "running", tasks: 1})}),
      changed("quiet", 8, {state: "muted", summary: summary({attention: "login", tasks: 3})}),
    ]);
    // gone ran tasks before it stopped, so it stays on top; fresh has none and quiet is muted.
    expect(ids(rows)).toEqual(["gone", "busy", "asks", "quiet", "fresh"]);
  });

  test("an instance with no record of a change falls in by its since, or when it was last heard of", () => {
    const finished = instance("finished", {
      state: "unread",
      summary: summary({unread: true, task: {id: 2, status: "completed", prompt: "Fix", started_at: "2026-11-01T09:00:00Z", ended_at: "2026-11-01T09:04:00Z", errors: [], steered: []}}),
    });
    const heard = instance("heard", {last_seen: "2026-11-01T09:06:00Z"});
    // The finished, unseen one is active, so it leads.
    expect(ids(sortRecent([changed("a", 2), finished, changed("b", 5), heard, changed("c", 8)]))).toEqual(["finished", "c", "heard", "b", "a"]);
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
    changed("checkout", 2, {group: "Clients", state: "needs-you", summary: summary({attention: "request", title: "Deploy", tasks: 1})}),
    changed("billing", 8, {group: "Internal", state: "working", summary: summary({status: "running", title: "Invoices", tasks: 1})}),
    changed("docs", 5, {state: "unread", summary: summary({unread: true, title: "Deploy notes", tasks: 1})}),
    changed("legacy", 9, {group: "Internal", state: "unreachable", connection: "unreachable"}),
    changed("site", 1, {group: "Clients"}),
  ];
  // Pinned: billing on top, then docs.
  const pinned = rows.map((row) => (row.id === "billing" ? {...row, pin: 1} : row.id === "docs" ? {...row, pin: 2} : row));
  const shape = (sections: ListSection[]) => sections.map((section) => [section.label, ids(section.instances)]);

  test("groups: what groupInstances makes of what passes, each with a header once there is a group", () => {
    const sections = listSections(rows, {agents: none, filter: "all", query: "", order: "groups"});
    expect(sections.map(({name, instances, needsYou}) => ({name, instances, needsYou}))).toEqual(groupInstances(rows));
    expect(sections.map((section) => [section.key, section.label])).toEqual([
      ["Clients", "Clients"],
      ["Internal", "Internal"],
      ["", "Ungrouped"],
    ]);
  });

  test("groups: no group, no headers", () => {
    const ungrouped = rows.map((row) => ({...row, group: undefined}));
    expect(shape(listSections(ungrouped, {agents: none, filter: "all", query: "", order: "groups"}))).toEqual([["", ["checkout", "billing", "docs", "site", "legacy"]]]);
  });

  test("recent: one block with no header, those with tasks first, each part by the latest change", () => {
    const sections = listSections(rows, {agents: none, filter: "all", query: "", order: "recent"});
    expect(sections.map((section) => [section.key, section.label, section.name, ids(section.instances), section.needsYou])).toEqual([
      [recentKey, "", "", ["billing", "docs", "checkout", "legacy", "site"], 1],
    ]);
  });

  test("machines: the groups, each machine's agents together, the one that needs you first", () => {
    // checkout and site on dev-1, billing and legacy on dev-2, docs on dev-1 and in no group.
    const host: Record<string, string> = {checkout: "dev-1", site: "dev-1", docs: "dev-1", billing: "dev-2", legacy: "dev-2"};
    const onMachines = rows.map((row) => ({...row, hostname: host[row.id]}));
    const sections = listSections(onMachines, {agents: none, filter: "all", query: "", order: "groups", byMachine: true});
    expect(sections.map((section) => [section.key, section.label, section.machines?.map((machine) => [machine.hostname, ids(machine.instances)])])).toEqual([
      ["Clients", "Clients", [["dev-1", ["checkout", "site"]]]],
      ["Internal", "Internal", [["dev-2", ["billing", "legacy"]]]],
      ["", "Ungrouped", [["dev-1", ["docs"]]]],
    ]);
    // A machine that needs you comes before one that works, whatever their names.
    const urgent = onMachines.map((row) => (row.id === "legacy" ? {...row, hostname: "dev-0"} : row.id === "billing" ? {...row, group: "Clients"} : row));
    const [clients] = listSections(urgent, {agents: none, filter: "all", query: "", order: "groups", byMachine: true});
    expect(clients.machines?.map((machine) => machine.hostname)).toEqual(["dev-1", "dev-2"]);
    expect(ids(clients.instances)).toEqual(["checkout", "site", "billing"]);
    // The filter, the agents and the search leave each machine the agents that pass.
    expect(listSections(onMachines, {agents: none, filter: "all", query: "dev-2", order: "groups", byMachine: true}).map((section) => section.machines?.map((machine) => ids(machine.instances)))).toEqual([[["billing", "legacy"]]]);
  });

  test("machines: the pinned ones first, one by one; no group, no headers", () => {
    const host: Record<string, string> = {checkout: "dev-1", site: "dev-1", docs: "dev-1", billing: "dev-1", legacy: "dev-2"};
    const sections = listSections(
      pinned.map((row) => ({...row, hostname: host[row.id]})),
      {agents: none, filter: "all", query: "", order: "groups", byMachine: true},
    );
    expect(sections.map((section) => [section.label, ids(section.instances), section.machines?.length])).toEqual([
      ["Pinned", ["billing", "docs"], undefined],
      ["Clients", ["checkout", "site"], 1],
      ["Internal", ["legacy"], 1],
    ]);
    const ungrouped = rows.map((row) => ({...row, group: undefined, hostname: host[row.id]}));
    expect(listSections(ungrouped, {agents: none, filter: "all", query: "", order: "groups", byMachine: true}).map((section) => [section.label, section.machines?.map((machine) => machine.hostname)])).toEqual([
      ["", ["dev-1", "dev-2"]],
    ]);
  });

  test("recent by machine: one section, each machine's agents together, the latest change first", () => {
    // checkout (2), site (1) and docs (5) on dev-1; billing (8) and legacy (9) on dev-2.
    const host: Record<string, string> = {checkout: "dev-1", site: "dev-1", docs: "dev-1", billing: "dev-2", legacy: "dev-2"};
    const onMachines = rows.map((row) => ({...row, hostname: host[row.id]}));
    const sections = listSections(onMachines, {agents: none, filter: "all", query: "", order: "recent", byMachine: true});
    expect(sections.map((section) => [section.key, section.label, section.machines?.map((machine) => [machine.hostname, ids(machine.instances)])])).toEqual([
      // As the recent rows: billing, docs, checkout, then legacy and site with no task.
      [recentKey, "", [["dev-2", ["billing", "legacy"]], ["dev-1", ["docs", "checkout", "site"]]]],
    ]);
    expect(ids(sections[0].instances)).toEqual(["billing", "legacy", "docs", "checkout", "site"]);
    // Pinned ones stay apart, one by one.
    const pinnedOnMachines = pinned.map((row) => ({...row, hostname: host[row.id]}));
    expect(
      listSections(pinnedOnMachines, {agents: none, filter: "all", query: "", order: "recent", byMachine: true}).map((section) => [section.label, ids(section.instances), section.machines?.map((machine) => machine.hostname)]),
    ).toEqual([
      ["Pinned", ["billing", "docs"], undefined],
      ["Recent changes", ["checkout", "site", "legacy"], ["dev-1", "dev-2"]],
    ]);
  });

  test("off by machine, the orders are as they were", () => {
    const host: Record<string, string> = {checkout: "dev-1", site: "dev-1", docs: "dev-1", billing: "dev-2", legacy: "dev-2"};
    const onMachines = rows.map((row) => ({...row, hostname: host[row.id]}));
    for (const order of ["groups", "recent"] as const) {
      const off = listSections(onMachines, {agents: none, filter: "all", query: "", order, byMachine: false});
      expect(off).toEqual(listSections(onMachines, {agents: none, filter: "all", query: "", order}));
      expect(off.some((section) => section.machines)).toBe(false);
    }
  });

  test("the filter and the search apply to both orders", () => {
    expect(listSections(rows, {agents: none, filter: "offline", query: "", order: "recent"}).map((section) => ids(section.instances))).toEqual([["legacy"]]);
    expect(listSections(rows, {agents: none, filter: "all", query: "deploy", order: "recent"}).map((section) => ids(section.instances))).toEqual([["docs", "checkout"]]);
    expect(shape(listSections(rows, {agents: none, filter: "all", query: "deploy", order: "groups"}))).toEqual([
      ["Clients", ["checkout"]],
      ["Ungrouped", ["docs"]],
    ]);
    expect(listSections(rows, {agents: none, filter: "working", query: "deploy", order: "recent"})).toEqual([]);
    expect(listSections(rows, {agents: none, filter: "working", query: "deploy", order: "groups"})).toEqual([]);
  });

  test("groups: the pinned ones first, in their order, and not again in their groups", () => {
    const sections = listSections(pinned, {agents: none, filter: "all", query: "", order: "groups"});
    expect(sections.map((section) => [section.key, section.label, ids(section.instances)])).toEqual([
      [pinnedKey, "Pinned", ["billing", "docs"]],
      ["Clients", "Clients", ["checkout", "site"]],
      ["Internal", "Internal", ["legacy"]],
    ]);
    expect(sections[0]).toMatchObject({name: "", pinned: true, needsYou: 0});
    expect(sections.slice(1).some((section) => section.pinned)).toBe(false);
  });

  test("recent: the pinned ones first, then the others by their latest change, under a header of their own", () => {
    expect(shape(listSections(pinned, {agents: none, filter: "all", query: "", order: "recent"}))).toEqual([
      ["Pinned", ["billing", "docs"]],
      ["Recent changes", ["checkout", "legacy", "site"]],
    ]);
  });

  test("pinned ones and no group: the others under Ungrouped", () => {
    const ungrouped = pinned.map((row) => ({...row, group: undefined}));
    expect(shape(listSections(ungrouped, {agents: none, filter: "all", query: "", order: "groups"}))).toEqual([
      ["Pinned", ["billing", "docs"]],
      ["Ungrouped", ["checkout", "site", "legacy"]],
    ]);
  });

  test("the pinned ones' section counts what needs you there", () => {
    const asks = pinned.map((row) => (row.id === "checkout" ? {...row, pin: 3} : row));
    const [top] = listSections(asks, {agents: none, filter: "all", query: "", order: "groups"});
    expect([ids(top.instances), top.needsYou]).toEqual([["billing", "docs", "checkout"], 1]);
  });

  test("the filter and the search apply to the pinned ones too; a section none passes isn't there", () => {
    expect(shape(listSections(pinned, {agents: none, filter: "working", query: "", order: "groups"}))).toEqual([["Pinned", ["billing"]]]);
    expect(shape(listSections(pinned, {agents: none, filter: "all", query: "deploy", order: "recent"}))).toEqual([
      ["Pinned", ["docs"]],
      ["Recent changes", ["checkout"]],
    ]);
    // No pinned one passes: the list as it is without pins.
    expect(shape(listSections(pinned, {agents: none, filter: "offline", query: "", order: "recent"}))).toEqual([["", ["legacy"]]]);
    expect(shape(listSections(pinned, {agents: none, filter: "needs-you", query: "", order: "groups"}))).toEqual([["Clients", ["checkout"]]]);
    expect(listSections(pinned, {agents: none, filter: "all", query: "nothing like it", order: "groups"})).toEqual([]);
  });

  test("the agents picked apply with the filter and the search, to the pinned ones too", () => {
    // billing and checkout run Codex, legacy an agent of AgentAPI 2; the rest Claude Code.
    const agentOf: Record<string, string> = {billing: "codex", checkout: "codex", legacy: "aider"};
    const mixed = pinned.map((row) => ({...row, agent_type: agentOf[row.id] ?? "claude"}));
    const view = (agents: HubAgent[], filter: "all" | "working" = "all", query = "") => shape(listSections(mixed, {agents: new Set(agents), filter, query, order: "groups"}));
    expect(view(["codex"])).toEqual([
      ["Pinned", ["billing"]],
      ["Clients", ["checkout"]],
    ]);
    expect(view(["codex", "other"])).toEqual([
      ["Pinned", ["billing"]],
      ["Clients", ["checkout"]],
      ["Internal", ["legacy"]],
    ]);
    expect(view(["codex"], "working")).toEqual([["Pinned", ["billing"]]]);
    expect(view(["claude"], "all", "deploy")).toEqual([["Pinned", ["docs"]]]);
    expect(view(["pi"])).toEqual([]);
    expect(shape(listSections(mixed, {agents: new Set(["codex"]), filter: "all", query: "", order: "recent"}))).toEqual([
      ["Pinned", ["billing"]],
      ["Recent changes", ["checkout"]],
    ]);
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

describe("recentAgent", () => {
  // On dev-1: a changed at 9:30, b at 9:10, c at 9:05; in the recent order.
  const machine = machinesInOrder([
    changed("a", 30, {hostname: "dev-1", summary: summary({tasks: 1})}),
    changed("b", 10, {hostname: "dev-1", summary: summary({tasks: 1})}),
    changed("c", 5, {hostname: "dev-1", summary: summary({tasks: 1})}),
  ])[0];
  const at = (m: number) => Date.parse(`2026-11-01T09:${String(m).padStart(2, "0")}:00Z`);

  test("the latest change, by default", () => {
    expect(recentAgent(machine)).toBe("a");
  });

  test("a pick at the latest change or after it", () => {
    expect(recentAgent(machine, {id: "c", at: at(31)})).toBe("c");
    expect(recentAgent(machine, {id: "c", at: at(30)})).toBe("c");
  });

  test("the latest change over a pick before it, or one that doesn't say when", () => {
    expect(recentAgent(machine, {id: "c", at: at(29)})).toBe("a");
    expect(recentAgent(machine, {id: "c"})).toBe("a");
  });

  test("the agent open beside the list over all of them; picks and agents of other cards don't count", () => {
    expect(recentAgent(machine, {id: "c", at: at(40)}, "b")).toBe("b");
    expect(recentAgent(machine, {id: "x", at: at(40)}, "y")).toBe("a");
  });
});

describe("keepMachines", () => {
  const view = {agents: none, filter: "all" as const, query: "", order: "groups" as const, byMachine: true};
  const rows = [instance("a", {hostname: "dev-1"}), instance("b", {hostname: "dev-1"}), instance("c", {hostname: "dev-2"})];

  test("a machine whose agents didn't change is the one shown before", () => {
    const before = keepMachines(listSections(rows, view), []);
    // An event for c: a new object for it, the others the same.
    const after = keepMachines(listSections([rows[0], rows[1], {...rows[2], name: "renamed"}], view), before);
    const [dev1, dev2] = after[0].machines!;
    expect(dev1).toBe(before[0].machines![0]);
    expect(dev2).not.toBe(before[0].machines![1]);
    expect(dev2.instances[0].name).toBe("renamed");
  });

  test("a machine with an agent more, or less, or in another section, is new", () => {
    const before = keepMachines(listSections(rows, view), []);
    const more = keepMachines(listSections([...rows, instance("d", {hostname: "dev-1"})], view), before);
    expect(more[0].machines![0]).not.toBe(before[0].machines![0]);
    expect(ids(more[0].machines![0].instances)).toEqual(["a", "b", "d"]);
    const less = keepMachines(listSections([rows[0], rows[2]], view), before);
    expect(less[0].machines![0]).not.toBe(before[0].machines![0]);
    const grouped = keepMachines(listSections([rows[0], rows[1], {...rows[2], group: "Ops"}], view), before);
    expect(grouped.find((section) => section.key === "Ops")!.machines![0]).not.toBe(before[0].machines![1]);
  });
});
