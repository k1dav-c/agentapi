// The /events updates received but not applied yet. The chat applies them
// together (conversationReducer in lib/conversation-state.ts), and with
// them the point of the conversation they bring its state to.

import {richMessageKey, type Message, type RichMessage} from "./conversation";
import type {NativeRequest} from "./native-requests";
import type {QueuedMessage} from "./queue";
import type {ServerTask} from "./server-tasks";
import type {SyncTracker} from "./session-sync";

// Where a state of the conversation is: its epoch, and the highest change
// it holds. What a cache of that state may claim, and resume from.
export interface SyncPoint {
  epoch: string;
  seq: number;
}

export interface EventBatch {
  // The updates replace the state: a full replay.
  replace: boolean;
  messages: (Message & {seq?: number})[];
  richMessages: (RichMessage & {seq?: number})[];
  requests: NativeRequest[];
  tasks: ServerTask[];
  // A full replay sends the requests and the tasks after the messages (the
  // tasks last). While it is on its way (held) they wait, and the state's
  // stay (requestsAfter, tasksAfter in lib/conversation-state.ts); the
  // batch that completes it replaces them (replaceHeld). replayFrom is the
  // time of the replay's earliest message so far, the start of the
  // conversation it holds, while held.
  held: boolean;
  replaceHeld: boolean;
  replayFrom?: string;
  // A full replay that came the newest task first: the state's messages
  // go in ID order, its rich messages by their places here (see
  // ReplayOrder), each batch of the replay's and the one that completes
  // it alike. Unset for any other batch, and for a replay in the order the
  // conversation happened (earlier servers), which applies as it came.
  richOrder?: ReadonlyMap<string, RichPlace>;
  // The message queue, when an update said it: the latest.
  queue?: QueuedMessage[];
  // The point of the state once the batch is applied.
  point: SyncPoint;
}

// Where a rich message of a full replay goes: with the task it came with,
// known by the ID of the task's first message (block), and among the
// task's rich messages, in the order they came (index).
export interface RichPlace {
  block: number;
  index: number;
}

// The order of a full replay as it comes. A sync replay holds the newest
// task first, each task its messages in ID order and then its rich
// messages, so that the task the user looks at is whole first; a message
// with a lower ID than the one before it starts the next, earlier task.
// Each rich message takes its place with the task it came after. A replay
// in the order the conversation happened has none of those (reordered
// stays false), and applies as it came.
class ReplayOrder {
  private last: number | undefined;
  private block = Number.POSITIVE_INFINITY;
  private next = 0;
  readonly rich = new Map<string, RichPlace>();
  reordered = false;

  message(id: number) {
    if (this.last !== undefined && id < this.last) {
      this.reordered = true;
      this.block = id;
    } else if (this.last === undefined) {
      this.block = id;
    }
    this.last = id;
  }

  richMessage(key: string) {
    if (!this.rich.has(key)) this.rich.set(key, {block: this.block, index: this.next++});
  }
}

export class PendingEvents {
  private messages = new Map<number, Message & {seq?: number}>();
  private richMessages = new Map<string, RichMessage & {seq?: number}>();
  private requests = new Map<string, NativeRequest>();
  private tasks = new Map<number, ServerTask>();
  private queue: {queue: QueuedMessage[]; seq: number} | undefined;
  private replace = false;
  private replaceHeld = false;
  private replayFrom: string | undefined;
  private replayFromId: number | undefined;
  // The order of the full replay on its way, until the batch that
  // completes it.
  private order: ReplayOrder | undefined;

  constructor(private readonly sync: SyncTracker) {}

  addMessage(message: Message & {seq?: number}) {
    // The replay's earliest message starts the conversation it holds (""
    // for one without a time).
    if (this.replaceHeld && (this.replayFromId === undefined || message.id < this.replayFromId)) {
      this.replayFromId = message.id;
      this.replayFrom = message.time ?? "";
    }
    if (this.sync.replaying) this.order?.message(message.id);
    this.messages.set(message.id, message);
  }

  addRichMessage(message: RichMessage & {seq?: number}) {
    const key = richMessageKey(message);
    if (this.sync.replaying) this.order?.richMessage(key);
    this.richMessages.set(key, message);
  }

  addRequest(request: NativeRequest) {
    this.requests.set(request.id, request);
  }

  addTask(task: ServerTask) {
    this.tasks.set(task.id, task);
  }

  // A queue update holds the whole queue: the latest replaces the others.
  setQueue(queue: QueuedMessage[], seq: number) {
    this.queue = {queue, seq};
  }

  // What follows is the full state: it replaces whatever is pending, and
  // the state.
  replaceAll() {
    this.messages.clear();
    this.richMessages.clear();
    this.requests.clear();
    this.tasks.clear();
    this.queue = undefined;
    this.replace = true;
    this.replaceHeld = true;
    this.replayFrom = undefined;
    this.replayFromId = undefined;
    this.order = new ReplayOrder();
  }

  // take returns the updates received so far and marks them applied: the
  // tracker's watermark moves past them (unless a replay is still on its
  // way), and the batch's point is where the tracker is then. Applying the
  // updates and the point together keeps a cache of the state from
  // claiming a change it doesn't hold: the watermark alone can run ahead of
  // the state, as the replay's last event moves it before the replay is
  // applied.
  //
  // A batch that shows the progress of a long full replay leaves its
  // requests and tasks pending: they replace the state's in the batch that
  // completes it. The batches of a replay that came the newest task first
  // carry its order (richOrder), the same for each.
  take(): EventBatch {
    const held = this.replaceHeld && this.sync.replaying;
    const batch = {
      replace: this.replace,
      messages: [...this.messages.values()],
      richMessages: [...this.richMessages.values()],
      requests: held ? [] : [...this.requests.values()],
      tasks: held ? [] : [...this.tasks.values()],
      held,
      replaceHeld: this.replaceHeld && !held,
      replayFrom: held ? this.replayFrom : undefined,
      richOrder: this.order?.reordered ? this.order.rich : undefined,
      queue: this.queue?.queue,
    };
    const queueSeq = this.queue?.seq;
    this.messages.clear();
    this.richMessages.clear();
    if (!held) {
      this.requests.clear();
      this.tasks.clear();
      this.replaceHeld = false;
      this.replayFrom = undefined;
      this.replayFromId = undefined;
      this.order = undefined;
    }
    this.queue = undefined;
    this.replace = false;
    for (const update of [...batch.messages, ...batch.richMessages, ...batch.requests, ...batch.tasks]) this.sync.applied(update.seq ?? 0);
    if (queueSeq !== undefined) this.sync.applied(queueSeq);
    return {...batch, point: {epoch: this.sync.epoch, seq: this.sync.watermark}};
  }
}
