import {describe, expect, test} from "bun:test";
import {getReconnectDelay} from "./reconnect";

describe("getReconnectDelay", () => {
  test("uses exponential backoff", () => {
    expect(getReconnectDelay(1, 0.5)).toBe(1000);
    expect(getReconnectDelay(2, 0.5)).toBe(2000);
    expect(getReconnectDelay(5, 0.5)).toBe(16000);
  });

  test("caps the base delay and applies bounded jitter", () => {
    expect(getReconnectDelay(20, 0)).toBe(24000);
    expect(getReconnectDelay(20, 1)).toBe(30000);
  });
});
