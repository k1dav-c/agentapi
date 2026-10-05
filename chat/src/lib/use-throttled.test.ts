import {describe, expect, test} from "bun:test";
import {throttleDelay} from "./use-throttled";

describe("throttleDelay", () => {
  test("shows at once when the last update is older than the interval", () => {
    expect(throttleDelay(0, 10_000, 1000)).toBe(0);
    expect(throttleDelay(9_000, 10_000, 1000)).toBe(0);
  });
  test("waits for the rest of the interval otherwise", () => {
    expect(throttleDelay(9_700, 10_000, 1000)).toBe(700);
  });
});
