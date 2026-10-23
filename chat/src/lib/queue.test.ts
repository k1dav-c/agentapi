import {describe, expect, test} from "bun:test";
import {QueueView, type QueuedMessage} from "./queue";

const queued = (id: number): QueuedMessage => ({id, content: `task ${id}`, time: "2026-10-22T10:00:00Z"});
const ids = (view: QueueView) => view.shown.map((message) => message.id);

describe("QueueView", () => {
  test("shows what the server's events and reads say", () => {
    const view = new QueueView();
    expect(view.read([queued(1), queued(2)], view.events)).toBe(true);
    expect(ids(view)).toEqual([1, 2]);
    view.event([queued(2)]);
    expect(ids(view)).toEqual([2]);
  });

  test("a read that started before an event came is older than it", () => {
    const view = new QueueView();
    const since = view.events;
    // The queued task went out while GET /queue was on its way.
    view.event([]);
    expect(view.read([queued(1)], since)).toBe(false);
    expect(ids(view)).toEqual([]);
  });

  test("an order the user gave shows until the server took it, also as events come", () => {
    const view = new QueueView();
    view.event([queued(1), queued(2), queued(3)]);
    view.reorder([3, 1, 2]);
    expect(ids(view)).toEqual([3, 1, 2]);
    // An event from before the server took the order: 2 went out, 4 came.
    view.event([queued(1), queued(3), queued(4)]);
    expect(ids(view)).toEqual([3, 1, 4]);
    // Taken: the order stays until the server's event says it.
    view.reordered(true);
    expect(ids(view)).toEqual([3, 1, 4]);
    view.event([queued(3), queued(1), queued(4)]);
    expect(ids(view)).toEqual([3, 1, 4]);
  });

  test("an order the server refused goes back to the server's", () => {
    const view = new QueueView();
    view.event([queued(1), queued(2)]);
    view.reorder([2, 1]);
    view.reordered(false);
    expect(ids(view)).toEqual([1, 2]);
  });
});
