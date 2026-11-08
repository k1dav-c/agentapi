import {describe, expect, test} from "bun:test";
import {agentDepth} from "./use-hub-history";

describe("agentDepth", () => {
  test("the agents opened that lead to a page of the history", () => {
    expect(agentDepth(null)).toBe(0);
    expect(agentDepth({other: true})).toBe(0);
    expect(agentDepth({hub: "agent", depth: 3})).toBe(3);
    // An older hub's page of an agent.
    expect(agentDepth({hub: "agent"})).toBe(1);
    expect(agentDepth({hub: "agent", depth: 0})).toBe(1);
    expect(agentDepth({hub: "agent", depth: "2"})).toBe(1);
  });
});
