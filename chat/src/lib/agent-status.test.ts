import {describe, expect, test} from "bun:test";
import {applyStatusChange, initialAgentStatus, sameValue, update} from "./agent-status";
import type {StatusChangeEvent} from "./events";

const event: StatusChangeEvent = {
  status: "running",
  agent_type: "codex",
  turn: 1,
  server_id: "s1",
  lifecycle: "ready",
  agentapi_version: "1.2.3",
  capabilities: {steer: true, interrupt: true, resume: true, images: false, settings: true},
  settings: {permission_mode: "bypass", model: "gpt-5"},
  usage: {context: {used: 10, size: 100}},
  terminal: true,
};

describe("applyStatusChange", () => {
  test("reads the event", () => {
    const status = applyStatusChange(initialAgentStatus, event);
    expect(status.serverStatus).toBe("running");
    expect(status.agentType).toBe("codex");
    expect(status.agentapiVersion).toBe("1.2.3");
    expect(status.capabilities?.steer).toBe(true);
    expect(status.settings?.model).toBe("gpt-5");
    expect(status.terminalAvailable).toBe(true);
    expect(status.loginRequired).toBeNull();
  });

  test("an event that changes nothing keeps the status", () => {
    const status = applyStatusChange(initialAgentStatus, event);
    // The server sends new objects every time.
    expect(applyStatusChange(status, structuredClone(event))).toBe(status);
  });

  test("what an event doesn't change keeps its object", () => {
    const status = applyStatusChange(initialAgentStatus, event);
    const next = applyStatusChange(status, {...structuredClone(event), status: "stable", usage: {context: {used: 20, size: 100}}});
    expect(next).not.toBe(status);
    expect(next.serverStatus).toBe("stable");
    expect(next.usage?.context?.used).toBe(20);
    expect(next.capabilities).toBe(status.capabilities);
    expect(next.settings).toBe(status.settings);
  });

  test("an unknown status or agent", () => {
    // Not what the schema allows, but what a newer server might send.
    const status = applyStatusChange(initialAgentStatus, {status: "weird", agent_type: "brand-new"} as unknown as StatusChangeEvent);
    expect(status.serverStatus).toBe("unknown");
    expect(status.agentType).toBe("custom");
  });
});

describe("update", () => {
  test("returns the status itself when nothing differs", () => {
    expect(update(initialAgentStatus, {serverStatus: "unknown", capabilities: null})).toBe(initialAgentStatus);
    expect(update(initialAgentStatus, {serverStatus: "offline"}).serverStatus).toBe("offline");
  });
});

describe("sameValue", () => {
  test("compares JSON values", () => {
    expect(sameValue({a: [1, {b: 2}]}, {a: [1, {b: 2}]})).toBe(true);
    expect(sameValue({a: 1}, {a: 1, b: undefined})).toBe(true);
    expect(sameValue({a: 1}, {a: 2})).toBe(false);
    expect(sameValue([1, 2], [1, 2, 3])).toBe(false);
    expect(sameValue(null, {})).toBe(false);
    expect(sameValue([], {})).toBe(false);
  });
});
