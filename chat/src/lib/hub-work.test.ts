import {describe, expect, test} from "bun:test";
import {hubInstance} from "./hub-fixtures";
import type {HubInstance} from "./hub";
import {formatWork, workSeconds, workTitle, workTotals} from "./hub-work";

const workNow = Date.parse("2026-11-03T12:00:00Z");

const worker = (id: string, work?: HubInstance["work"]): HubInstance =>
  hubInstance(id, {last_seen: "2026-11-03T00:00:00Z", state: work?.working_since ? "working" : "idle", work});

describe("work time", () => {
  test("formats compactly, rounded down", () => {
    expect(formatWork(0)).toBe("0m");
    expect(formatWork(59)).toBe("<1m");
    expect(formatWork(60)).toBe("1m");
    expect(formatWork(45 * 60 + 59)).toBe("45m");
    expect(formatWork(3600)).toBe("1h");
    expect(formatWork(2.36 * 3600)).toBe("2.3h");
    expect(formatWork(10 * 3600 - 1)).toBe("9.9h");
    expect(formatWork(31 * 3600 + 3599)).toBe("31h");
    expect(workTitle(2.5 * 3600, 31 * 3600)).toBe("Worked 2.5h in the last 24 hours, 31h in the last 7 days (as the hub saw it)");
  });

  test("adds the span going on to what the hub sent, within each window", () => {
    expect(workSeconds(worker("old"), workNow)).toEqual({day: 0, week: 0});
    const done = worker("a", {last_24h_seconds: 600, last_7d_seconds: 7200});
    expect(workSeconds(done, workNow)).toEqual({day: 600, week: 7200});
    const running = worker("b", {last_24h_seconds: 600, last_7d_seconds: 600, working_since: "2026-11-03T11:30:00Z"});
    expect(workSeconds(running, workNow)).toEqual({day: 600 + 1800, week: 600 + 1800});
    expect(workSeconds(running, workNow + 60_000)).toEqual({day: 600 + 1860, week: 600 + 1860});
    // Working for two days: the day window holds 24 hours of it.
    const long = worker("c", {last_24h_seconds: 0, last_7d_seconds: 0, working_since: "2026-11-01T12:00:00Z"});
    expect(workSeconds(long, workNow)).toEqual({day: 86400, week: 2 * 86400});
    // A browser clock behind the hub's adds nothing, not less.
    expect(workSeconds(worker("d", {last_24h_seconds: 5, last_7d_seconds: 5, working_since: "2026-11-03T12:01:00Z"}), workNow)).toEqual({day: 5, week: 5});
  });

  test("totals each instance's own time, and how many worked this week", () => {
    const rows = [
      worker("a", {last_24h_seconds: 600, last_7d_seconds: 7200}),
      worker("b", {last_24h_seconds: 0, last_7d_seconds: 0, working_since: "2026-11-03T11:00:00Z"}),
      worker("c", {last_24h_seconds: 0, last_7d_seconds: 0}),
      worker("d"),
    ];
    expect(workTotals(rows, workNow)).toEqual({day: 600 + 3600, week: 7200 + 3600, counted: 2});
  });
});
