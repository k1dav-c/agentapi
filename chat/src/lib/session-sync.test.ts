import {describe, expect, test} from "bun:test";
import {SyncTracker} from "./session-sync";

describe("SyncTracker", () => {
  test("the watermark waits for the end of a replay that isn't in seq order", () => {
    const sync = new SyncTracker();
    sync.restore("e1", 100);
    expect(sync.begin({epoch: "e1", seq: 950}, "")).toBe(false);
    expect(sync.replaying).toBe(true);
    // The newest change (a message) comes first; rich messages with lower
    // seqs are still on their way.
    expect(sync.noteEvent("")).toBe(false);
    sync.applied(950);
    expect(sync.watermark).toBe(100);
    sync.applied(120);
    expect(sync.noteEvent("")).toBe(false);
    // The replay's last event carries its SSE id.
    expect(sync.noteEvent("950")).toBe(true);
    expect(sync.replaying).toBe(false);
    expect(sync.watermark).toBe(950);
  });

  test("live changes move the watermark as they are applied", () => {
    const sync = new SyncTracker();
    sync.begin({epoch: "e1", seq: 5}, "");
    sync.noteEvent("5");
    sync.applied(6);
    sync.applied(7);
    expect(sync.watermark).toBe(7);
    // Live events without an id (status, errors) keep the last id.
    expect(sync.noteEvent("7")).toBe(false);
  });

  test("a full replay starts over", () => {
    const sync = new SyncTracker();
    sync.restore("old", 40);
    expect(sync.begin({epoch: "new", seq: 12}, "")).toBe(true);
    expect(sync.epoch).toBe("new");
    expect(sync.watermark).toBe(0);
    expect(sync.begin({epoch: "new", seq: 12, full: true}, "")).toBe(true);
  });

  test("a replay with nothing new is complete at once", () => {
    const sync = new SyncTracker();
    sync.restore("e1", 30);
    expect(sync.begin({epoch: "e1", seq: 30}, "")).toBe(false);
    expect(sync.replaying).toBe(false);
    sync.applied(31);
    expect(sync.watermark).toBe(31);
  });

  test("a connection dropped mid-replay resumes from the old watermark", () => {
    const sync = new SyncTracker();
    sync.restore("e1", 100);
    sync.begin({epoch: "e1", seq: 950}, "");
    sync.applied(950);
    // Reconnecting (or saving the cache) uses the frozen watermark, so the
    // server replays everything after it again.
    expect(sync.watermark).toBe(100);
    sync.begin({epoch: "e1", seq: 960}, "");
    expect(sync.noteEvent("960")).toBe(true);
    expect(sync.watermark).toBe(960);
  });

  test("reset forgets the conversation", () => {
    const sync = new SyncTracker();
    sync.restore("e1", 8);
    sync.reset();
    expect(sync.epoch).toBe("");
    expect(sync.watermark).toBe(0);
    expect(sync.replaying).toBe(false);
  });
});
