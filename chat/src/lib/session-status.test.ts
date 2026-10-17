import {describe, expect, test} from "bun:test";
import {getAgentState, getStatusMeta} from "./session-status";

const connected = {connectionStatus: "connected"} as const;

describe("getAgentState", () => {
  test("follows the activity status", () => {
    expect(getAgentState({...connected, serverStatus: "running"})).toBe("working");
    expect(getAgentState({...connected, serverStatus: "stable"})).toBe("ready");
    expect(getAgentState({...connected, serverStatus: "unknown"})).toBe("connecting");
  });

  test("an open terminal prompt needs the user, even while stable", () => {
    expect(getAgentState({...connected, serverStatus: "stable", terminalPrompt: "Do you want to proceed?\n❯ 1. Yes"})).toBe("needs-you");
  });

  test("a request the agent waits on (native transport) needs the user", () => {
    expect(getAgentState({...connected, serverStatus: "stable", pendingRequestCount: 1})).toBe("needs-you");
  });

  test("a login the agent waits for needs the user, like a request", () => {
    // While it waits at startup the agent reports running (starting).
    expect(getAgentState({...connected, serverStatus: "running", loginRequired: true})).toBe("needs-you");
    expect(getAgentState({...connected, serverStatus: "stable", loginRequired: true})).toBe("needs-you");
  });

  test("the connection still comes first", () => {
    expect(getAgentState({serverStatus: "running", connectionStatus: "reconnecting", loginRequired: true})).toBe("reconnecting");
    expect(getAgentState({serverStatus: "running", connectionStatus: "offline", pendingRequestCount: 1})).toBe("offline");
    expect(getAgentState({serverStatus: "offline", connectionStatus: "connected"})).toBe("offline");
  });
});

describe("getStatusMeta", () => {
  test("says what the agent waits for", () => {
    expect(getStatusMeta("needs-you", true).detail).toBe("Agent is waiting for you to log in");
    expect(getStatusMeta("needs-you").detail).toBe("Agent is waiting for your answer");
    expect(getStatusMeta("working", true)).toMatchObject({kind: "working", label: "Working", detail: "Agent is working"});
  });
});
