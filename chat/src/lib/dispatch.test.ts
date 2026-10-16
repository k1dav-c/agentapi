import {describe, expect, test} from "bun:test";
import {
  canInterrupt,
  canSteer,
  dispatchModeForKey,
  dispatchModes,
  moveInQueue,
  sendNotice,
  turnActive,
  type DispatchContext,
} from "./dispatch";

const caps = {steer: true, interrupt: true, resume: true, images: true};
const native = (overrides: Partial<DispatchContext> = {}): DispatchContext => ({
  transport: "native",
  capabilities: caps,
  serverStatus: "running",
  pendingRequests: 0,
  ...overrides,
});

const key = (overrides: Partial<{shiftKey: boolean; metaKey: boolean; ctrlKey: boolean; isComposing: boolean}> = {}) => ({
  key: "Enter",
  shiftKey: false,
  metaKey: false,
  ctrlKey: false,
  ...overrides,
});

describe("dispatchModes", () => {
  test("a working native agent can be steered and interrupted", () => {
    expect(dispatchModes(native())).toEqual(["queue", "steer", "interrupt"]);
  });

  test("an idle agent only takes new turns", () => {
    expect(dispatchModes(native({serverStatus: "stable"}))).toEqual(["queue"]);
  });

  test("while it waits on a request it can be stopped but not steered", () => {
    const waiting = native({serverStatus: "stable", pendingRequests: 1});
    expect(turnActive(waiting)).toBe(true);
    expect(canSteer(waiting)).toBe(false);
    expect(canInterrupt(waiting)).toBe(true);
  });

  test("follows the capabilities", () => {
    expect(dispatchModes(native({capabilities: {...caps, steer: false}}))).toEqual(["queue", "interrupt"]);
    expect(dispatchModes(native({capabilities: null}))).toEqual(["queue"]);
  });

  test("other transports only queue", () => {
    expect(dispatchModes(native({transport: "pty"}))).toEqual(["queue"]);
    expect(dispatchModes(native({transport: "acp"}))).toEqual(["queue"]);
    expect(turnActive(native({transport: "pty", serverStatus: "stable", pendingRequests: 1}))).toBe(false);
  });
});

describe("dispatchModeForKey", () => {
  const all = dispatchModes(native());
  test("Enter queues, mod+Enter steers, mod+Shift+Enter interrupts", () => {
    expect(dispatchModeForKey(key(), all)).toBe("queue");
    expect(dispatchModeForKey(key({metaKey: true}), all)).toBe("steer");
    expect(dispatchModeForKey(key({ctrlKey: true}), all)).toBe("steer");
    expect(dispatchModeForKey(key({ctrlKey: true, shiftKey: true}), all)).toBe("interrupt");
  });

  test("falls back to queueing when the mode isn't available", () => {
    expect(dispatchModeForKey(key({metaKey: true}), ["queue"])).toBe("queue");
    expect(dispatchModeForKey(key({metaKey: true, shiftKey: true}), ["queue"])).toBe("queue");
  });

  test("Shift+Enter, other keys and IME composition don't send", () => {
    expect(dispatchModeForKey(key({shiftKey: true}), all)).toBeNull();
    expect(dispatchModeForKey({...key(), key: "a"}, all)).toBeNull();
    expect(dispatchModeForKey(key({isComposing: true}), all)).toBeNull();
  });
});

describe("sendNotice", () => {
  test("tells what happened to the message", () => {
    expect(sendNotice({queued: false, steered: true}, "steer")).toBe("Added to the current turn");
    expect(sendNotice({queued: true}, "queue")).toBe("Task queued");
    expect(sendNotice({queued: true}, "interrupt")).toBe("Stopping the agent; your message goes next");
    // A steer sent once the turn had ended starts a new turn.
    expect(sendNotice({queued: false}, "steer")).toBeNull();
  });
});

describe("moveInQueue", () => {
  test("moves a message up or down", () => {
    expect(moveInQueue([1, 2, 3], 3, -1)).toEqual([1, 3, 2]);
    expect(moveInQueue([1, 2, 3], 1, 1)).toEqual([2, 1, 3]);
    expect(moveInQueue([1, 2, 3], 1, 2)).toEqual([2, 3, 1]);
  });

  test("refuses moves past the ends or of unknown messages", () => {
    expect(moveInQueue([1, 2, 3], 1, -1)).toBeNull();
    expect(moveInQueue([1, 2, 3], 3, 1)).toBeNull();
    expect(moveInQueue([1, 2, 3], 9, 1)).toBeNull();
  });
});
