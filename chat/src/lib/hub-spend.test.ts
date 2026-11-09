import {describe, expect, test} from "bun:test";
import {idleInstance as instance, summary} from "./hub-fixtures";
import {countedSpends, countedTokens, currentSpend, spendTitle, spendTotals, tokensTitle, tokenTotals, type Spend} from "./hub-spend";

describe("spend", () => {
  const spending = (day_usd: number, week_usd: number, scope: string, updated_at = "2026-10-26T00:00:00Z") =>
    summary({spend: {day_usd, week_usd, scope, updated_at}});

  test("spend: totals of the instances that report one", () => {
    const totals = spendTotals([instance("a", {summary: spending(1.5, 10, "s1")}), instance("b", {summary: spending(100, 300, "s2")}), instance("c")]);
    expect(totals).toEqual({day: 101.5, week: 310, counted: 2});
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

  test("tokens: titles", () => {
    expect(tokensTitle(3_400_000, 21_000_000)).toBe("3.4M tokens in the last 24 hours, 21M in the last 7 days");
    expect(spendTitle(4.2, 30, false, 3_400_000)).toBe("$4.20 in the last 24 hours, $30 in the last 7 days (API-equivalent, at list prices; a subscription doesn't charge it); 3.4M tokens in the last 24 hours");
    expect(spendTitle(4.2, 30, true, 950)).toStartWith("Last known, not counted: $4.20");
    expect(spendTitle(4.2, 30, true, 950)).toEndWith("; 950 tokens in the last 24 hours");
  });

});
