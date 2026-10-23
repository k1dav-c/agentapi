import {describe, expect, test} from "bun:test";
import {getReconnectDelay, reconnectAtOnce} from "./reconnect";

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

describe("reconnectAtOnce", () => {
  test("a stream that was open comes back at once", () => {
    expect(reconnectAtOnce(true, [], 1000)).toBe(true);
  });

  test("one that never opened waits for the backoff", () => {
    expect(reconnectAtOnce(false, [], 1000)).toBe(false);
  });

  test("a server that keeps closing streams gets the backoff, until it stops", () => {
    const recent: number[] = [];
    expect([0, 100, 200, 300].map((now) => reconnectAtOnce(true, recent, now))).toEqual([true, true, true, false]);
    expect(reconnectAtOnce(true, recent, 10_050)).toBe(true);
  });
});
