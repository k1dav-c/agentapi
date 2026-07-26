import {describe, expect, test} from "bun:test";
import {parseFailedMessages} from "./failed-messages";

describe("failed message persistence", () => {
  test("restores retryable user messages", () => {
    const failed = {
      clientId: "request-1",
      role: "user",
      content: "Run the tests",
      deliveryStatus: "failed",
      error: "offline",
    };
    expect(parseFailedMessages(JSON.stringify([failed]))).toEqual([failed]);
  });

  test("rejects sending messages and malformed storage", () => {
    expect(
      parseFailedMessages(
        JSON.stringify([
          {
            clientId: "request-1",
            role: "user",
            content: "Run the tests",
            deliveryStatus: "sending",
          },
        ]),
      ),
    ).toEqual([]);
    expect(parseFailedMessages("not-json")).toEqual([]);
  });
});
