import { describe, expect, test } from "bun:test";
import { contextPercent, formatResetIn, formatTokens, fullestLimit, usageTone } from "./usage";

describe("usage", () => {
  test("context percent", () => {
    expect(contextPercent({ used: 41000, size: 200000 })).toBeCloseTo(20.5);
    expect(contextPercent({ used: 5, size: 0 })).toBe(0);
    expect(contextPercent({ used: 300, size: 200 })).toBe(100);
  });

  test("tokens", () => {
    expect(formatTokens(950)).toBe("950");
    expect(formatTokens(41_499)).toBe("41k");
    expect(formatTokens(1_000_000)).toBe("1M");
    expect(formatTokens(1_250_000)).toBe("1.3M");
  });

  test("tone", () => {
    expect(usageTone(12)).toBe("ok");
    expect(usageTone(12, "allowed_warning")).toBe("warning");
    expect(usageTone(85)).toBe("warning");
    expect(usageTone(96)).toBe("danger");
    expect(usageTone(undefined, "rejected")).toBe("danger");
  });

  test("reset", () => {
    const now = Date.parse("2026-10-17T10:00:00Z");
    expect(formatResetIn("2026-10-17T10:30:00Z", now)).toBe("in 30m");
    expect(formatResetIn("2026-10-17T12:13:00Z", now)).toBe("in 2h 13m");
    expect(formatResetIn("2026-10-20T14:00:00Z", now)).toBe("in 3d 4h");
    expect(formatResetIn("2026-10-17T09:00:00Z", now)).toBe("resetting");
    expect(formatResetIn(undefined, now)).toBe("");
  });

  test("fullest limit", () => {
    expect(fullestLimit([{ id: "a", label: "A", used_percent: 12 }, { id: "b", label: "B", used_percent: 27 }])?.id).toBe("b");
    expect(fullestLimit([{ id: "a", label: "A" }])?.id).toBe("a");
    expect(fullestLimit(undefined)).toBeUndefined();
  });
});
