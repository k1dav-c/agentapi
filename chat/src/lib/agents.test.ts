import {describe, expect, test} from "bun:test";
import {
  agentName,
  countRunningAgents,
  formatElapsed,
  formatTokens,
  sortAgentsForDisplay,
  type SubAgent,
} from "./agents";

function agent(overrides: Partial<SubAgent>): SubAgent {
  return {
    thread_id: "t",
    parent_thread_id: "root",
    path: "/root/worker",
    depth: 1,
    status: "running",
    started_at: "2026-09-26T02:00:00Z",
    updated_at: "2026-09-26T02:00:00Z",
    ...overrides,
  };
}

describe("agents", () => {
  test("names agents by the last path segment", () => {
    expect(agentName(agent({path: "/root/research/sleep_one"}))).toBe("sleep_one");
    expect(agentName(agent({path: "", thread_id: "abc"}))).toBe("abc");
  });

  test("counts running agents", () => {
    expect(countRunningAgents([agent({}), agent({status: "completed"}), agent({})])).toBe(2);
  });

  test("lists running agents first, then most recently updated", () => {
    const sorted = sortAgentsForDisplay([
      agent({thread_id: "old-done", status: "completed", updated_at: "2026-09-26T02:01:00Z"}),
      agent({thread_id: "new-done", status: "completed", updated_at: "2026-09-26T02:05:00Z"}),
      agent({thread_id: "running", updated_at: "2026-09-26T01:00:00Z"}),
    ]);
    expect(sorted.map((a) => a.thread_id)).toEqual(["running", "new-done", "old-done"]);
  });

  test("formats elapsed time", () => {
    const start = "2026-09-26T02:00:00Z";
    const at = (s: number) => Date.parse(start) + s * 1000;
    expect(formatElapsed(start, at(45))).toBe("45s");
    expect(formatElapsed(start, at(185))).toBe("3m 05s");
    expect(formatElapsed(start, at(3720))).toBe("1h 02m");
    expect(formatElapsed("bad", at(1))).toBe("");
  });

  test("formats token counts", () => {
    expect(formatTokens(undefined)).toBe("");
    expect(formatTokens(950)).toBe("950 tokens");
    expect(formatTokens(1234)).toBe("1.2k tokens");
    expect(formatTokens(87235)).toBe("87k tokens");
  });
});
