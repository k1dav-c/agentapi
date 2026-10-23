import {describe, expect, test} from "bun:test";
import {PendingEvents} from "./pending-events";
import {SyncTracker} from "./session-sync";

const message = (id: number, seq: number) => ({id, role: "agent", content: `message ${id}`, time: "2026-10-22T10:00:00Z", seq});
const rich = (id: string, seq: number) => ({message_id: id, role: "assistant", timestamp: "2026-10-22T10:00:01Z", content: [], seq});

describe("PendingEvents", () => {
  test("a batch's point is the changes it holds", () => {
    const sync = new SyncTracker();
    const pending = new PendingEvents(sync);
    sync.begin({epoch: "e1", seq: 0}, "");
    pending.addMessage(message(1, 5));
    pending.addRichMessage(rich("m1", 6));
    const batch = pending.take();
    expect(batch.messages.map((m) => m.id)).toEqual([1]);
    expect(batch.richMessages.map((m) => m.message_id)).toEqual(["m1"]);
    expect(batch.point).toEqual({epoch: "e1", seq: 6});
    expect(pending.take().messages).toEqual([]);
  });

  test("the end of a replay moves the watermark before the replay is applied, but not the point", () => {
    // The page saves its state with the point of the last batch it applied,
    // not with the watermark: at the end of a replay the watermark already
    // counts changes that are still pending, and a cache that claimed them
    // would never get them again (?since= skips them).
    const sync = new SyncTracker();
    const pending = new PendingEvents(sync);
    sync.restore("e1", 100);
    sync.begin({epoch: "e1", seq: 140}, "");
    pending.addMessage(message(1, 140));
    // A batch while the replay is on its way doesn't pass the watermark.
    expect(pending.take().point).toEqual({epoch: "e1", seq: 100});
    pending.addRichMessage(rich("m1", 120));
    // The replay's last event: the watermark moves at once.
    expect(sync.noteEvent("140")).toBe(true);
    expect(sync.watermark).toBe(140);
    // The point comes with the replay's last updates.
    const batch = pending.take();
    expect(batch.richMessages.map((m) => m.seq)).toEqual([120]);
    expect(batch.point).toEqual({epoch: "e1", seq: 140});
  });

  test("task updates come in the batch by task, the latest of each, and count in its point", () => {
    const sync = new SyncTracker();
    const pending = new PendingEvents(sync);
    sync.begin({epoch: "e1", seq: 0}, "");
    const started = {id: 4, status: "running" as const, prompt: "fix it", started_at: "2026-10-22T10:00:00Z", errors: [], steered: []};
    pending.addTask({...started, seq: 7});
    pending.addTask({...started, status: "completed", seq: 9});
    const batch = pending.take();
    expect(batch.tasks.map((t) => [t.id, t.status])).toEqual([[4, "completed"]]);
    expect(batch.point).toEqual({epoch: "e1", seq: 9});
    pending.addTask({...started, seq: 10});
    pending.replaceAll();
    expect(pending.take().tasks).toEqual([]);
  });

  test("a full replay replaces what is pending, and has the new epoch", () => {
    const sync = new SyncTracker();
    const pending = new PendingEvents(sync);
    sync.restore("old", 40);
    pending.addMessage(message(9, 41));
    expect(sync.begin({epoch: "new", seq: 3}, "")).toBe(true);
    pending.replaceAll();
    pending.addMessage(message(0, 1));
    sync.noteEvent("3");
    const batch = pending.take();
    expect(batch.replace).toBe(true);
    expect(batch.messages.map((m) => m.id)).toEqual([0]);
    expect(batch.point).toEqual({epoch: "new", seq: 3});
    expect(pending.take().replace).toBe(false);
  });
});
