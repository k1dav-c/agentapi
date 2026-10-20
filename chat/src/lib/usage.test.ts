import { describe, expect, test } from "bun:test";
import { coldCache, contextPercent, formatAgo, formatResetIn, fullestLimit, usageTone } from "./usage";

describe("usage", () => {
  test("context percent", () => {
    expect(contextPercent({ used: 41000, size: 200000 })).toBeCloseTo(20.5);
    expect(contextPercent({ used: 5, size: 0 })).toBe(0);
    expect(contextPercent({ used: 300, size: 200 })).toBe(100);
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
  test("cold cache", () => {
    const now = Date.parse("2026-10-17T10:00:00Z");
    const usage = {
      context: { used: 120_000, size: 200_000 },
      prompt_cache: { ttl_seconds: 3600, expires_at: "2026-10-17T09:48:00Z" },
    };
    expect(coldCache(usage, now)).toEqual({ expiredAt: Date.parse("2026-10-17T09:48:00Z"), tokens: 120_000 });
    // Still warm.
    expect(coldCache(usage, Date.parse("2026-10-17T09:47:00Z"))).toBeNull();
    // Too little context to bother.
    expect(coldCache({ ...usage, context: { used: 8_000, size: 200_000 } }, now)).toBeNull();
    // Agents that don't report their cache.
    expect(coldCache({ context: usage.context }, now)).toBeNull();
    expect(coldCache(null, now)).toBeNull();
  });

  test("ago", () => {
    const now = Date.parse("2026-10-17T10:00:00Z");
    expect(formatAgo(now - 20_000, now)).toBe("just now");
    expect(formatAgo(now - 12 * 60_000, now)).toBe("12 min ago");
    expect(formatAgo(now - 3 * 3_600_000, now)).toBe("3 h ago");
    expect(formatAgo(now - 50 * 3_600_000, now)).toBe("2 d ago");
  });
});
