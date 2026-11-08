import {describe, expect, test} from "bun:test";
import {idleInstance as instance, summary} from "./hub-fixtures";
import {agentLabel, agentOf, agentsIn, countAgents, countFilters, matchesAgents, matchesSearch, nothingMatches, parseAgents, shownAgents, toggleAgent} from "./hub-filter";

describe("filters", () => {
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
