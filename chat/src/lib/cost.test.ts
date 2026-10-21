import {describe, expect, test} from "bun:test";
import type {ModelCall} from "./chat-api";
import {costDetails, formatTokens, formatUSD, periodTotals, taskCosts} from "./cost";

function call(time: string, usd: number, priced = true): ModelCall {
  return {time, usd, priced, model: "claude-opus-5-5", tokens: {input: 10, cache_read: 1000, cache_write: 100, output: 50}};
}

describe("formatUSD", () => {
  test("cents, fractions of a cent and thousands", () => {
    expect(formatUSD(0)).toBe("$0.00");
    expect(formatUSD(0.0042)).toBe("$0.004");
    expect(formatUSD(12.345)).toBe("$12.35");
    expect(formatUSD(1234.5)).toBe("$1,235");
  });
});

describe("formatTokens", () => {
  test("rounds to thousands and millions", () => {
    expect(formatTokens(512)).toBe("512");
    expect(formatTokens(1500)).toBe("1.5k");
    expect(formatTokens(34_000)).toBe("34k");
    expect(formatTokens(1_200_000)).toBe("1.2M");
  });
});

describe("taskCosts", () => {
  test("a task's calls run until the next task's prompt", () => {
    const costs = taskCosts(
      ["2026-10-20T10:00:00Z", "2026-10-20T11:00:00Z", undefined],
      [
        call("2026-10-20T09:59:59Z", 0.5), // before the first task: in it
        call("2026-10-20T10:00:05Z", 1),
        call("2026-10-20T11:00:00Z", 2),
        call("2026-10-20T11:30:00Z", 0, false),
      ],
    );
    expect(costs[0]?.usd).toBe(1.5);
    expect(costs[0]?.calls).toBe(2);
    expect(costs[1]?.usd).toBe(2);
    expect(costs[1]?.unpriced).toBe(true);
    expect(costs[1]?.tokens.cache_read).toBe(2000);
    expect(costs[2]).toBeUndefined();
  });

  test("no sent task, no costs", () => {
    expect(taskCosts([undefined], [call("2026-10-20T10:00:00Z", 1)])).toEqual([undefined]);
  });
});

describe("periodTotals", () => {
  test("sums hours into the viewer's days", () => {
    const now = new Date(2026, 9, 21, 15, 0);
    const hour = (daysAgo: number, h: number) => new Date(2026, 9, 21 - daysAgo, h).toISOString();
    const totals = periodTotals(
      [
        {hour: hour(40, 9), usd: 8},
        {hour: hour(20, 9), usd: 4},
        {hour: hour(6, 0), usd: 2},
        {hour: hour(1, 23), usd: 1},
        {hour: hour(0, 0), usd: 0.5},
      ],
      now,
    );
    expect(totals).toEqual({today: 0.5, week: 3.5, month: 7.5, all: 15.5});
  });
});

describe("costDetails", () => {
  test("says it is the API-equivalent and lists the tokens", () => {
    const details = costDetails({usd: 1.5, calls: 2, tokens: {input: 20, cache_read: 2000, cache_write: 200, output: 100}}, true);
    expect(details).toContain("$1.50 API-equivalent cost");
    expect(details).toContain("2 model calls");
    expect(details).toContain("Input 2.2k: 20 uncached, 2.0k cached, 200 written to the cache");
    expect(details).toContain("without a known price");
  });
});
