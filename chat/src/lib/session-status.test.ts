import {describe, expect, test} from "bun:test";
import {getAgentState, getStatusMeta} from "./session-status";

describe("getAgentState", () => {
  test("a login the agent waits for needs the user, like a request", () => {
    // While it waits at startup the agent reports running (starting).
    expect(getAgentState("running", "connected", "", 0, true)).toBe("needs-you");
    expect(getAgentState("stable", "connected", "", 0, true)).toBe("needs-you");
    expect(getAgentState("stable", "connected", "", 1)).toBe("needs-you");
    expect(getAgentState("running", "connected")).toBe("working");
  });

  test("the connection still comes first", () => {
    expect(getAgentState("running", "reconnecting", "", 0, true)).toBe("reconnecting");
    expect(getAgentState("running", "offline", "", 0, true)).toBe("offline");
  });
});

describe("getStatusMeta", () => {
  test("says what the agent waits for", () => {
    expect(getStatusMeta("running", "connected", "", 0, true).detail).toBe("Agent is waiting for you to log in");
    expect(getStatusMeta("stable", "connected", "", 1).detail).toBe("Agent is waiting for your answer");
  });
});
