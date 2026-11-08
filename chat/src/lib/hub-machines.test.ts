import {describe, expect, test} from "bun:test";
import {idleInstance as instance, summary} from "./hub-fixtures";
import {activeAgent, groupByMachine, hostPrefix, machineOf, machineSubtotals, sameMachine, sharedVersion, tabOrder} from "./hub-machines";
import {countedSpends, spendTotals, type Spend} from "./hub-spend";

describe("machines", () => {
  test("the start most machines' names share, up to a - or .", () => {
    const coder = ["coder-dave-api", "coder-dave-web", "coder-dave-web-2", "gpu-9e61"];
    expect(hostPrefix(coder)).toBe("coder-dave-");
    // Shared by fewer than half: none.
    expect(hostPrefix(["coder-dave-api", "coder-dave-web", "a", "b", "c"])).toBe("");
    expect(hostPrefix(["dev-1.lan", "dev-2.lan"])).toBe("dev-");
    expect(hostPrefix(["api", "web"])).toBe("");
    expect(hostPrefix(["coder-dave-api"])).toBe("");
    // A name that ends at the start doesn't count it: something must be left.
    expect(hostPrefix(["coder-", "coder-"])).toBe("");
  });

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
