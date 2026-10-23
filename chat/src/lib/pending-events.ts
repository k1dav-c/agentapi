// The /events updates received but not applied yet. The chat applies them
// together (see use-event-stream.ts), and with them the point of the
// conversation they bring its state to.

import {richMessageKey, type Message, type RichMessage} from "./conversation";
import type {NativeRequest} from "./native-requests";
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
  // The point of the state once the batch is applied.
  point: SyncPoint;
}

export class PendingEvents {
  private messages = new Map<number, Message & {seq?: number}>();
  private richMessages = new Map<string, RichMessage & {seq?: number}>();
  private requests = new Map<string, NativeRequest>();
  private tasks = new Map<number, ServerTask>();
  private replace = false;

  constructor(private readonly sync: SyncTracker) {}

  addMessage(message: Message & {seq?: number}) {
    this.messages.set(message.id, message);
  }

  addRichMessage(message: RichMessage & {seq?: number}) {
    this.richMessages.set(richMessageKey(message), message);
  }

  addRequest(request: NativeRequest) {
    this.requests.set(request.id, request);
  }

  addTask(task: ServerTask) {
    this.tasks.set(task.id, task);
  }

  // What follows is the full state: it replaces whatever is pending, and
  // the state.
  replaceAll() {
    this.messages.clear();
    this.richMessages.clear();
    this.requests.clear();
    this.tasks.clear();
    this.replace = true;
  }

  // take returns the updates received so far and marks them applied: the
  // tracker's watermark moves past them (unless a replay is still on its
  // way), and the batch's point is where the tracker is then. Applying the
  // updates and the point together keeps a cache of the state from
  // claiming a change it doesn't hold: the watermark alone can run ahead of
  // the state, as the replay's last event moves it before the replay is
  // applied.
  take(): EventBatch {
    const batch = {
      replace: this.replace,
      messages: [...this.messages.values()],
      richMessages: [...this.richMessages.values()],
      requests: [...this.requests.values()],
      tasks: [...this.tasks.values()],
    };
    this.messages.clear();
    this.richMessages.clear();
    this.requests.clear();
    this.tasks.clear();
    this.replace = false;
    for (const update of [...batch.messages, ...batch.richMessages, ...batch.requests, ...batch.tasks]) this.sync.applied(update.seq ?? 0);
    return {...batch, point: {epoch: this.sync.epoch, seq: this.sync.watermark}};
  }
}
