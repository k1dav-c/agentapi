// The /events updates received but not applied yet. The chat applies them
// together (see use-event-stream.ts), and with them the point of the
// conversation they bring its state to.

import {richMessageKey, type Message, type RichMessage} from "./conversation";
import {mergeRequests, type NativeRequest} from "./native-requests";
import type {QueuedMessage} from "./queue";
import {mergeTasks, type ServerTask} from "./server-tasks";
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
  // stay (requestsAfter, tasksAfter); the batch that completes it replaces
  // them (replaceHeld). replayFrom is the time of the replay's first
  // message, the start of the conversation it holds, while held.
  held: boolean;
  replaceHeld: boolean;
  replayFrom?: string;
  // The message queue, when an update said it: the latest.
  queue?: QueuedMessage[];
  // The point of the state once the batch is applied.
  point: SyncPoint;
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

  constructor(private readonly sync: SyncTracker) {}

  addMessage(message: Message & {seq?: number}) {
    // A full replay sends the messages first, the earliest first ("" for
    // one without a time).
    if (this.replaceHeld) this.replayFrom ??= message.time ?? "";
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
  // completes it.
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
    }
    this.queue = undefined;
    this.replace = false;
    for (const update of [...batch.messages, ...batch.richMessages, ...batch.requests, ...batch.tasks]) this.sync.applied(update.seq ?? 0);
    if (queueSeq !== undefined) this.sync.applied(queueSeq);
    return {...batch, point: {epoch: this.sync.epoch, seq: this.sync.watermark}};
  }
}

// tasksAfter returns the tasks once batch is applied to previous. While a
// full replay is on its way they stay as they were: the transcript is
// grouped by them, and without them a long replay's progress would show
// each steered message as a task of its own, with a guessed status, until
// they came. Those whose message the replay sends as another message
// (another conversation's, after a restart without a state database) go.
export function tasksAfter(previous: ServerTask[], batch: EventBatch): ServerTask[] {
  if (batch.replaceHeld) return mergeTasks([], batch.tasks);
  if (batch.held) return tasksStillOf(previous, batch.messages);
  return batch.tasks.length > 0 ? mergeTasks(previous, batch.tasks) : previous;
}

// tasksStillOf returns the tasks but those whose user message messages has
// as another message: not the user's, or sent at another time.
function tasksStillOf(tasks: ServerTask[], messages: readonly Message[]): ServerTask[] {
  if (tasks.length === 0 || messages.length === 0) return tasks;
  const byId = new Map(messages.map((message) => [message.id, message]));
  const kept = tasks.filter((task) => {
    const message = byId.get(task.id);
    return !message || (message.role === "user" && (message.time === undefined || Date.parse(message.time) === Date.parse(task.started_at)));
  });
  return kept.length === tasks.length ? tasks : kept;
}

// requestsAfter returns the requests once batch is applied to previous,
// with mergeRequests. While a full replay is on its way they stay as they
// were, so its progress doesn't take the decision cards away until its
// requests come. Those the agent made before the replay's first message
// (replayFrom) go: they belong to another conversation, one the server
// replaced since (a restart, a new session).
export function requestsAfter(previous: NativeRequest[], batch: EventBatch): NativeRequest[] {
  if (batch.replaceHeld) return mergeRequests([], batch.requests);
  if (batch.held) return requestsSince(previous, batch.replayFrom);
  return batch.requests.length > 0 ? mergeRequests(previous, batch.requests) : previous;
}

// requestsSince returns the requests but those the agent made before from.
function requestsSince(requests: NativeRequest[], from: string | undefined): NativeRequest[] {
  const start = from === undefined ? NaN : Date.parse(from);
  if (Number.isNaN(start)) return requests;
  const kept = requests.filter((request) => !(Date.parse(request.created_at) < start));
  return kept.length === requests.length ? requests : kept;
}
