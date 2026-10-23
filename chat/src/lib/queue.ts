// The messages waiting for the agent, as the chat shows them. The server
// says what its queue holds in queue_update events (sync clients) and in
// GET /queue; an order the user gave shows at once, before the server took
// it.

import type {components} from "./api-schema";

export type QueuedMessage = components["schemas"]["QueuedMessage"];

export class QueueView {
  // What the server said last.
  private server: QueuedMessage[] = [];
  // An order the user gave that the server hasn't taken yet.
  private order: number[] | null = null;
  // How many queue events came: a read that started before one came is
  // older than it.
  events = 0;

  // The queue to show: the server's, in the order the user gave while it
  // is on its way.
  get shown(): QueuedMessage[] {
    return this.order ? inOrder(this.server, this.order) : this.server;
  }

  // A queue_update: the server's queue now.
  event(queue: QueuedMessage[]) {
    this.events++;
    this.server = queue;
  }

  // The queue a read (GET /queue) returned, which started when events was
  // since. It returns false, and leaves the queue as it is, when a queue
  // event came in the meantime: the event is newer.
  read(queue: QueuedMessage[], since: number): boolean {
    if (since !== this.events) return false;
    this.server = queue;
    return true;
  }

  // The user moved the queued messages into this order.
  reorder(ids: number[]) {
    this.order = ids;
  }

  // The server took the order (ok), and its queue now has it, though its
  // event may still be on its way; or it refused.
  reordered(ok: boolean) {
    if (ok && this.order) this.server = inOrder(this.server, this.order);
    this.order = null;
  }
}

// The messages of queue in the order of ids; those ids leave out (queued
// since) come after, as the server adds them at the end.
function inOrder(queue: QueuedMessage[], ids: number[]): QueuedMessage[] {
  const byId = new Map(queue.map((message) => [message.id, message]));
  const ordered = ids.flatMap((id) => byId.get(id) ?? []);
  const placed = new Set(ids);
  return [...ordered, ...queue.filter((message) => !placed.has(message.id))];
}
