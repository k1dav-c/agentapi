import {describe, expect, test} from "bun:test";
import {activityGrid, activityTotal, cellText, levelOf, levelThresholds, metricValue, parseActivity, parseActivityMetric, type Activity, type ActivityDay} from "./hub-activity";

// day is a day's figures.
const day = (date: string, work = 0, usd = 0, tokens = 0): ActivityDay => ({
  date,
  work_seconds: work,
  usd,
  tokens: {input: tokens, output: 0, cache_read: 0, cache_write: 0, total: tokens},
});

// days are n days to last, the earliest first, with no figures.
function days(last: string, n: number): ActivityDay[] {
  const end = Date.parse(`${last}T00:00:00Z`);
  return Array.from({length: n}, (_, i) => day(new Date(end - (n - 1 - i) * 86_400_000).toISOString().slice(0, 10)));
}

describe("the activity's levels", () => {
  test("a quarter of the days with any each, none at 0", () => {
    const thresholds = levelThresholds([0, 4, 1, 3, 2, 0]);
    expect(thresholds).toEqual([1, 2, 3, 4]);
    expect([0, 1, 2, 3, 4, 100].map((v) => levelOf(v, thresholds))).toEqual([0, 1, 2, 3, 4, 4]);
  });

  test("one busy day doesn't wash out the rest", () => {
    const thresholds = levelThresholds([1, 1, 2, 2, 3, 3, 1000]);
    expect([1, 2, 3, 1000].map((v) => levelOf(v, thresholds))).toEqual([1, 2, 3, 4]);
  });

  test("nothing yet: every day is 0; days all alike are the busiest", () => {
    expect(levelOf(0, levelThresholds([0, 0]))).toBe(0);
    expect(levelOf(5, levelThresholds([5, 5, 0]))).toBe(4);
  });
});

describe("the activity's grid", () => {
  // Tuesday 2026-11-10 is today.
  const activity: Activity = {days: days("2026-11-10", 371), since: "2026-11-03", time_zone: "Asia/Taipei"};

  test("53 weeks, Sunday on top, today in the last column, the rest of its week to come", () => {
    const grid = activityGrid(activity);
    expect(grid.weeks).toHaveLength(53);
    expect(grid.weeks.every((week) => week.length === 7)).toBe(true);
    const last = grid.weeks[52];
    expect(last[0].date).toBe("2026-11-08");
    expect(last[2].date).toBe("2026-11-10");
    expect(last[2].future).toBe(false);
    expect(last[3].future).toBe(true);
    expect(grid.weeks[0][0].date).toBe("2025-11-09");
  });

  test("days before since aren't counted; those after have their figures", () => {
    const cells = activityGrid(activity).weeks.flat();
    const at = (date: string) => cells.find((cell) => cell.date === date)!;
    expect(at("2026-11-02").before).toBe(true);
    expect(at("2026-11-03").before).toBe(false);
    expect(at("2026-11-03").day?.date).toBe("2026-11-03");
    expect(cellText(at("2026-11-02"))).toBe("Mon, Nov 2, 2026: not recorded");
  });

  test("a month is labeled where its first week starts, the first only with room", () => {
    const months = activityGrid(activity).months;
    expect(months.slice(0, 2)).toEqual([
      {column: 0, label: "Nov"},
      {column: 4, label: "Dec"},
    ]);
    expect(months.at(-1)).toEqual({column: 51, label: "Nov"});
    expect(months).toHaveLength(13);
    // Starting a week before a month's first: no room for its label.
    const tight = activityGrid({...activity, days: days("2026-11-24", 371)}).months;
    expect(tight[0].label).toBe("Dec");
  });
});

describe("the activity's figures", () => {
  const busy = day("2026-11-10", 5400, 12.5, 3_400_000);

  test("by measure, and what a cell says of all three", () => {
    expect(metricValue(busy, "work")).toBe(5400);
    expect(metricValue(busy, "tokens")).toBe(3_400_000);
    expect(metricValue(busy, "spend")).toBe(12.5);
    expect(cellText({date: busy.date, day: busy, before: false, future: false})).toBe("Tue, Nov 10, 2026: worked 1.5h, 3.4M tokens, $12.50");
    expect(activityTotal({days: [busy, busy], since: "", time_zone: "UTC"}, "spend")).toBe(25);
  });

  test("what the hub answers, checked", () => {
    expect(parseActivity({days: [busy, {date: 1}], since: "2026-11-01", time_zone: "UTC"})).toEqual({days: [busy], since: "2026-11-01", time_zone: "UTC"});
    expect(parseActivity({days: "no"})).toBeUndefined();
    expect(parseActivity(null)).toBeUndefined();
  });

  test("the measure the browser kept, or work time", () => {
    expect(parseActivityMetric("spend")).toBe("spend");
    expect(parseActivityMetric("nope")).toBe("work");
    expect(parseActivityMetric(null)).toBe("work");
  });
});
