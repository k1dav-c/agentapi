import {describe, expect, test} from "bun:test";
import {formatDuration, formatMilliseconds, formatTimeOfDay, secondsBetween} from "./clock";

describe("clock", () => {
  test("durations show hours only from an hour on", () => {
    expect(formatDuration(0)).toBe("0:00");
    expect(formatDuration(65)).toBe("1:05");
    expect(formatDuration(3599)).toBe("59:59");
    expect(formatDuration(4500)).toBe("1:15:00");
  });

  test("a tool call's time in milliseconds", () => {
    expect(formatMilliseconds(400)).toBe("< 1s");
    expect(formatMilliseconds(4200)).toBe("4.2s");
    expect(formatMilliseconds(185_000)).toBe("3m 5s");
  });

  test("a time of day, or nothing for a time that doesn't parse", () => {
    expect(formatTimeOfDay("2026-10-18T09:05:00Z")).toMatch(/^\d{2}:\d{2}$/);
    expect(formatTimeOfDay("not a time")).toBe("");
  });

  test("seconds between two times, never negative", () => {
    expect(secondsBetween(1_000, 62_999)).toBe(61);
    expect(secondsBetween(5_000, 1_000)).toBe(0);
  });
});
