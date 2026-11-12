import {describe, expect, test} from "bun:test";
import {idleInstance as instance, summary} from "./hub-fixtures";
import {
  countedSpends,
  countedTokens,
  currentSpend,
  providerPeriod,
  providerPriced,
  providerTitle,
  providerTotals,
  spendTitle,
  spendTotals,
  tokensTitle,
  tokenTotals,
  type ProviderSpend,
  type Spend,
} from "./hub-spend";

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

  // A provider's figures: day dollars and n tokens in the last 24 hours,
  // seven times both in the week.
  const onProvider = (provider: string, day: number, n: number): ProviderSpend => ({
    provider,
    day_usd: day,
    week_usd: 7 * day,
    day_tokens: tokens(n / 10),
    week_tokens: tokens((7 * n) / 10),
  });
  const byProvider = (scope: string, providers: ProviderSpend[], updated_at = "2026-10-26T00:00:00Z") =>
    summary({spend: {day_usd: 0, week_usd: 0, scope, updated_at, providers}});

  test("providers: each provider's totals, each scope once, the most spent this week first", () => {
    const rows = [
      instance("a", {summary: byProvider("same", [onProvider("default", 1, 100)], "2026-10-26T10:00:00Z")}),
      // The same sessions, read later: they count, not a's.
      instance("b", {summary: byProvider("same", [onProvider("default", 2, 200), onProvider("gateway", 0, 5000)], "2026-10-26T10:01:00Z")}),
      instance("c", {summary: byProvider("other", [onProvider("vllm", 3, 10), onProvider("gateway", 0, 1000)])}),
      // An earlier AgentAPI: a spend, no providers.
      instance("d", {summary: spending(9, 90, "older")}),
      instance("e", {state: "stopped", connection: "stopped", summary: byProvider("gone", [onProvider("vllm", 100, 100)])}),
    ];
    const totals = providerTotals(rows);
    expect(totals.counted).toBe(3);
    expect(totals.providers).toEqual([
      {provider: "vllm", day: 3, week: 21, dayTokens: 10, weekTokens: 70},
      {provider: "default", day: 2, week: 14, dayTokens: 200, weekTokens: 1400},
      // Unpriced: the most tokens after the amounts.
      {provider: "gateway", day: 0, week: 0, dayTokens: 6000, weekTokens: 42000},
    ]);
    expect(providerTotals([instance("f")])).toEqual({providers: [], counted: 0});
  });

  test("providers: an amount of nothing with tokens has no known price", () => {
    expect(providerPriced(0, 0)).toBe(true);
    expect(providerPriced(1.5, 100)).toBe(true);
    expect(providerPriced(0, 100)).toBe(false);
    expect(providerPeriod(4.2, 3_400_000)).toBe("3.4M tokens, $4.20");
    expect(providerPeriod(0, 950)).toBe("950 tokens, no price");
    expect(providerPeriod(0, 0)).toBe("0 tokens, $0");
    expect(providerTitle({provider: "gateway", day: 0, week: 4.2, dayTokens: 0, weekTokens: 3_400_000})).toBe(
      "0 tokens ($0) in the last 24 hours, 3.4M tokens ($4.20) in the last 7 days (API-equivalent, at list prices)",
    );
    expect(providerTitle({provider: "gateway", day: 0, week: 0, dayTokens: 950, weekTokens: 1200})).toBe(
      "950 tokens (no known price) in the last 24 hours, 1.2k tokens (no known price) in the last 7 days (API-equivalent, at list prices)",
    );
  });

});
