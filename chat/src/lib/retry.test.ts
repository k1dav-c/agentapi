import {describe, expect, test} from "bun:test";
import type {RetryWait} from "./chat-api";
import {formatCountdown, retryCountdown, retryTitle} from "./retry";

const now = Date.parse("2026-11-07T10:00:00Z");
const wait: RetryWait = {
  message: "API Error: 502 Bad Gateway. This is a server-side issue, usually temporary — try again in a moment.",
  api_status: 502,
  stopped_at: "2026-11-07T09:59:58Z",
  attempt: 1,
  attempts: 3,
  retry_at: "2026-11-07T10:00:28Z",
};

describe("retry", () => {
  test("the countdown reads like a clock", () => {
    expect(formatCountdown(28_000)).toBe("0:28");
    expect(formatCountdown(27_200)).toBe("0:28");
    expect(formatCountdown(245_000)).toBe("4:05");
    expect(formatCountdown(3_720_000)).toBe("1:02:00");
    expect(formatCountdown(-5)).toBe("0:00");
  });

  test("names the API's status when the agent says it", () => {
    expect(retryTitle(wait)).toBe("The API returned 502");
    expect(retryTitle({...wait, api_status: undefined})).toBe("The API request failed");
  });

  test("says when AgentAPI tries again, and which try it is", () => {
    expect(retryCountdown(wait, now)).toBe("Retrying in 0:28 (1 of 3).");
    expect(retryCountdown({...wait, attempt: 3}, now + 28_000)).toBe("Retrying now (3 of 3).");
  });
});
