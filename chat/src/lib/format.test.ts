import {describe, expect, test} from "bun:test";
import {formatAge, formatAgo, formatTokens, formatUSD, formatUSDShort} from "./format";

describe("formatTokens", () => {
  test("a count at a glance, the unit reached where rounding reaches it", () => {
    expect([0, 7.4, 512, 950, 999, 999.6, 1000, 1234, 1500, 4_210, 9_949, 9_960, 12_345, 34_000, 41_499, 999_499, 999_600, 1_200_000, 1_250_000, 3_412_000, 45_600_000, 1_234_000_000, 2e15].map(formatTokens)).toEqual([
      "0", "7", "512", "950", "999", "1k", "1k", "1.2k", "1.5k", "4.2k", "9.9k", "10k", "12k", "34k", "41k", "999k", "1M", "1.2M", "1.3M", "3.4M", "46M", "1.2B", "2000T",
    ]);
    expect(formatTokens(-5)).toBe("0");
  });
});

describe("formatUSD", () => {
  test("cents, fractions of a cent and thousands", () => {
    expect([0, 0.0042, 12.345, 999.99, 999.996, 1234.5].map(formatUSD)).toEqual(["$0.00", "$0.004", "$12.35", "$999.99", "$1,000", "$1,235"]);
  });
});

describe("formatUSDShort", () => {
  test("an amount at a glance, the unit reached where rounding reaches it", () => {
    expect([0, 0.004, 4.2, 9.996, 109.77, 452.31, 999.6, 1234, 9_960, 98_765, 999_600, 1_500_000].map(formatUSDShort)).toEqual([
      "$0", "$0", "$4.20", "$10", "$110", "$452", "$1k", "$1.2k", "$10k", "$99k", "$1M", "$1.5M",
    ]);
  });
});

describe("ages", () => {
  const now = Date.parse("2026-10-26T10:00:00Z");

  test("in words, from a time or a timestamp", () => {
    expect([now - 20_000, now - 12 * 60_000, now - 3 * 3_600_000, now - 50 * 3_600_000, now + 60_000].map((at) => formatAgo(at, now))).toEqual([
      "just now", "12 min ago", "3 h ago", "2 d ago", "just now",
    ]);
    expect(formatAgo("2026-10-26T09:48:00Z", now)).toBe("12 min ago");
    expect(formatAgo(undefined, now)).toBe("");
    expect(formatAgo("soon", now)).toBe("");
  });

  test("short, rounded as the words are", () => {
    expect(["2026-10-26T09:59:55Z", "2026-10-26T09:59:15Z", "2026-10-26T09:48:00Z", "2026-10-26T09:47:00.5Z", "2026-10-26T07:00:00Z", "2026-10-24T10:00:00Z"].map((at) => formatAge(at, now))).toEqual([
      "now", "45s", "12m", "12m", "3h", "2d",
    ]);
    expect(formatAge(now - 12 * 60_000, now)).toBe("12m");
    expect(formatAge(undefined, now)).toBe("");
    expect(formatAge("", now)).toBe("");
  });
});
