import {describe, expect, test} from "bun:test";
import {PendingEvents, tasksAfter, type EventBatch} from "./pending-events";
import type {ServerTask} from "./server-tasks";
import {SyncTracker} from "./session-sync";

const message = (id: number, seq: number) => ({id, role: "agent", content: `message ${id}`, time: "2026-10-22T10:00:00Z", seq});
const prompt = (id: number, seq: number, time = "2026-10-22T10:00:00Z") => ({id, role: "user", content: `prompt ${id}`, time, seq});
const rich = (id: string, seq: number) => ({message_id: id, role: "assistant", timestamp: "2026-10-22T10:00:01Z", content: [], seq});
const task = (id: number, seq: number, steered: number[] = []): ServerTask => ({
  id,
  status: "completed",
  prompt: `prompt ${id}`,
  started_at: "2026-10-22T10:00:00Z",
  errors: [],
  steered,
  seq,
});
const batch = (fields: Partial<EventBatch>): EventBatch => ({
  replace: false,
  messages: [],
  richMessages: [],
  requests: [],
  tasks: [],
  replaceTasks: false,
  holdTasks: false,
  point: {epoch: "e1", seq: 0},
  ...fields,
});

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

  test("a queue update holds the whole queue: the latest is in the batch, its seq in the point", () => {
    const sync = new SyncTracker();
    const pending = new PendingEvents(sync);
    sync.begin({epoch: "e1", seq: 0}, "");
    expect(pending.take().queue).toBeUndefined();
    pending.setQueue([{id: 1, content: "next", time: "2026-10-22T10:00:00Z"}], 11);
    pending.setQueue([], 12);
    const batch = pending.take();
    expect(batch.queue).toEqual([]);
    expect(batch.point).toEqual({epoch: "e1", seq: 12});
    expect(pending.take().queue).toBeUndefined();
    pending.setQueue([], 13);
    pending.replaceAll();
    expect(pending.take().queue).toBeUndefined();
  });

  test("a long full replay holds its tasks until it is complete", () => {
    // The server sends the tasks last: a batch that shows a long replay's
    // progress has none of them, and mustn't clear the chat's.
    const sync = new SyncTracker();
    const pending = new PendingEvents(sync);
    sync.restore("old", 40);
    sync.begin({epoch: "new", seq: 9}, "");
    pending.replaceAll();
    pending.addMessage(prompt(0, 1));
    expect(pending.take()).toMatchObject({replace: true, holdTasks: true, replaceTasks: false, tasks: []});
    pending.addTask(task(0, 8));
    expect(pending.take()).toMatchObject({replace: false, holdTasks: true, replaceTasks: false, tasks: []});
    pending.addTask(task(4, 9));
    sync.noteEvent("9");
    const last = pending.take();
    expect(last).toMatchObject({holdTasks: false, replaceTasks: true});
    expect(last.tasks.map((t) => t.id)).toEqual([0, 4]);
    expect(last.point).toEqual({epoch: "new", seq: 9});
    expect(pending.take()).toMatchObject({holdTasks: false, replaceTasks: false, tasks: []});

    // A full state without a replay to wait for replaces them at once.
    sync.begin({epoch: "reset", full: true, seq: 0}, "");
    pending.replaceAll();
    expect(pending.take()).toMatchObject({replace: true, holdTasks: false, replaceTasks: true, tasks: []});
  });

  test("a full replay's tasks replace the chat's once it is complete, which keeps them until then", () => {
    const cached = [task(0, 30, [2]), task(4, 38)];
    // While the replay is on its way the tasks stay as they are.
    const progress = batch({replace: true, holdTasks: true, messages: [prompt(0, 1), message(1, 2), prompt(2, 3)]});
    expect(tasksAfter(cached, progress)).toBe(cached);
    expect(tasksAfter(cached, batch({holdTasks: true}))).toBe(cached);
    // But for one whose message the replay sends as another message:
    // another conversation's.
    expect(tasksAfter(cached, batch({holdTasks: true, messages: [message(4, 5)]}))).toEqual([cached[0]]);
    expect(tasksAfter(cached, batch({holdTasks: true, messages: [prompt(4, 5, "2026-10-22T11:00:00Z")]}))).toEqual([cached[0]]);
    // The replay's end replaces them, whatever their seqs.
    expect(tasksAfter(cached, batch({replaceTasks: true, tasks: [task(0, 7)]}))).toEqual([task(0, 7)]);
    expect(tasksAfter(cached, batch({replaceTasks: true}))).toEqual([]);
    // Other updates merge.
    expect(tasksAfter(cached, batch({tasks: [task(8, 41)]})).map((t) => t.id)).toEqual([0, 4, 8]);
    expect(tasksAfter(cached, batch({messages: [message(9, 42)]}))).toBe(cached);
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
