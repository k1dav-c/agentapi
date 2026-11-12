// The conversation as the chat renders it, and every change to it: the
// /events batches, the cached conversation shown before connecting, a
// conversation started over, the requests read or answered over REST, and
// the drafts of the messages the user sends. ChatSession holds it in one
// useReducer (conversationReducer); the rules of each change are here, pure.

import {isDraftMessage, richMessageKey, withoutSeq, type DraftMessage, type Message, type RichMessage} from "./conversation";
import {prependPage, type HistoryPage} from "./history";
import {mergeListedRequests, mergeRequests, type NativeRequest} from "./native-requests";
import type {EventBatch, RichPlace, SyncPoint} from "./pending-events";
import {mergeTasks, type ServerTask} from "./server-tasks";
import type {CachedSession} from "./session-cache";

export interface ConversationState {
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  // Requests the agent made (native transport), pending and closed.
  requests: NativeRequest[];
  // The tasks as the server derives them (lib/server-tasks).
  tasks: ServerTask[];
  // A full replay is on its way: what the state holds is its progress (held
  // batches, see EventBatch), the messages so far and the rich messages that
  // came; a task's reply may be among those still to come. The batch that
  // completes the replay, a reset and a cached conversation end it.
  replaying: boolean;
  // The point of the conversation the state is at. It changes with the
  // updates that bring the state there, and renders with them: the
  // tracker's watermark runs ahead, and a cache that saved it with the
  // state of the last render could claim changes the state didn't hold,
  // which ?since= never sends again.
  synced: SyncPoint;
}

export const initialConversation: ConversationState = {
  messages: [],
  richMessages: [],
  requests: [],
  tasks: [],
  replaying: false,
  synced: {epoch: "", seq: 0},
};

export type ConversationAction =
  // An /events batch (PendingEvents.take), in two parts: the transcript
  // (messages, rich messages, tasks and the point), which renders in a
  // transition, and the requests, which apply at once so they are never
  // behind the point (see useEventStream).
  | {type: "batch"; batch: EventBatch}
  | {type: "batchRequests"; batch: EventBatch}
  // The cached conversation, shown before the first connection; the drafts
  // stay.
  | {type: "restore"; cached: CachedSession}
  // The conversation started over (DELETE /messages, resuming a session)
  // on a server that doesn't say so itself: nothing, at no point.
  | {type: "reset"}
  // GET /requests, with the requests held when it was asked (see
  // mergeListedRequests); a request's state as answering it returned.
  | {type: "requestsListed"; listed: NativeRequest[]; heldWhenAsked: NativeRequest[]}
  | {type: "requestAnswered"; request: NativeRequest}
  // A message the user sends, shown as a draft until the server has it;
  // dropped, or marked failed when the send failed; the failed drafts a
  // reload kept.
  | {type: "draftAdded"; draft: DraftMessage}
  | {type: "draftRemoved"; clientId: string}
  | {type: "draftFailed"; clientId: string; error: string}
  | {type: "draftsRestored"; drafts: DraftMessage[]}
  // A page of earlier tasks the chat read (lib/history.ts): it goes before
  // what the state holds. The point stays: the server's changes to those
  // tasks came before it.
  | {type: "earlierLoaded"; page: HistoryPage};

// conversationReducer returns the state once action is applied. A list
// that doesn't change keeps its object, and so does the state when nothing
// does: the views and the cache compare them by identity.
export function conversationReducer(state: ConversationState, action: ConversationAction): ConversationState {
  switch (action.type) {
    case "batch": {
      const {batch} = action;
      return withChanges(state, {
        synced: samePoint(state.synced, batch.point) ? state.synced : batch.point,
        messages: messagesAfter(state.messages, batch),
        richMessages: richMessagesAfter(state.richMessages, batch),
        tasks: tasksAfter(state.tasks, batch),
        // Only a full replay's progress is held: any other batch is a
        // replay's last, or comes after it.
        replaying: batch.held,
      });
    }
    case "batchRequests":
      return withChanges(state, {requests: requestsAfter(state.requests, action.batch)});
    case "restore": {
      const {cached} = action;
      const point = {epoch: cached.epoch, seq: cached.seq};
      return {
        messages: [...cached.messages, ...state.messages.filter(isDraftMessage)],
        richMessages: cached.richMessages,
        requests: cached.requests,
        tasks: cached.tasks,
        replaying: false,
        synced: samePoint(state.synced, point) ? state.synced : point,
      };
    }
    case "reset":
      return {...initialConversation, synced: samePoint(state.synced, initialConversation.synced) ? state.synced : initialConversation.synced};
    case "requestsListed":
      return withChanges(state, {requests: mergeListedRequests(state.requests, action.listed, action.heldWhenAsked)});
    case "requestAnswered":
      return withChanges(state, {requests: mergeRequests(state.requests, [action.request])});
    case "draftAdded":
      return {...state, messages: [...state.messages, action.draft]};
    case "draftRemoved": {
      const messages = state.messages.filter((message) => !isDraftMessage(message) || message.clientId !== action.clientId);
      return messages.length === state.messages.length ? state : {...state, messages};
    }
    case "draftFailed": {
      const {clientId, error} = action;
      return {
        ...state,
        messages: state.messages.map((message) =>
          isDraftMessage(message) && message.clientId === clientId ? {...message, deliveryStatus: "failed", error} : message,
        ),
      };
    }
    case "earlierLoaded":
      return prependPage(state, action.page);
    case "draftsRestored": {
      const existing = new Set(state.messages.filter(isDraftMessage).map((message) => message.clientId));
      const restored = action.drafts.filter((draft) => !existing.has(draft.clientId));
      return restored.length === 0 ? state : {...state, messages: [...state.messages, ...restored]};
    }
  }
}

// withChanges returns state with changes, or state itself when each
// change is the object it has already.
function withChanges(state: ConversationState, changes: Partial<ConversationState>): ConversationState {
  const keys = Object.keys(changes) as (keyof ConversationState)[];
  return keys.every((key) => changes[key] === state[key]) ? state : {...state, ...changes};
}

function samePoint(a: SyncPoint, b: SyncPoint): boolean {
  return a.epoch === b.epoch && a.seq === b.seq;
}

// messagesAfter returns the messages once batch is applied to previous
// (previous itself when the batch changes none). The server's messages
// replace their own; of the drafts, the failed ones stay until they are
// retried or dismissed, and those still being sent until the server has
// them: a new user message of the batch with their content takes the
// place of one, and sendMessage drops the others once the server answered.
// A draft dropped before that, by any update, would leave a send that
// fails nothing to mark failed. A replay that came the newest task first
// (richOrder) puts the server's messages in ID order, in the places they
// hold among the drafts.
export function messagesAfter(previous: (Message | DraftMessage)[], batch: EventBatch): (Message | DraftMessage)[] {
  if (!batch.replace && batch.messages.length === 0) return previous;
  const known = new Set(previous.flatMap((message) => (isDraftMessage(message) ? [] : [message.id])));
  // The content of the new user messages, as many times as each came.
  const arrived = new Map<string, number>();
  for (const message of batch.messages) {
    if (message.role !== "user" || known.has(message.id)) continue;
    const content = message.content.trim();
    arrived.set(content, (arrived.get(content) ?? 0) + 1);
  }
  const kept = previous.filter((message) => {
    if (!isDraftMessage(message)) return !batch.replace;
    if (message.deliveryStatus === "failed") return true;
    const content = message.content.trim();
    const count = arrived.get(content) ?? 0;
    if (count === 0) return true;
    arrived.set(content, count - 1);
    return false;
  });
  const merged = mergeByKey(kept, batch.messages.map(withoutSeq), (message) => (isDraftMessage(message) ? undefined : String(message.id)));
  return batch.richOrder ? inIdOrder(merged) : merged;
}

// inIdOrder returns messages with the server's in ID order; the drafts
// keep their places.
function inIdOrder(messages: (Message | DraftMessage)[]): (Message | DraftMessage)[] {
  const slots: number[] = [];
  const server: Message[] = [];
  messages.forEach((message, i) => {
    if (isDraftMessage(message)) return;
    slots.push(i);
    server.push(message);
  });
  server.sort((a, b) => a.id - b.id);
  const ordered = messages.slice();
  slots.forEach((slot, i) => {
    ordered[slot] = server[i];
  });
  return ordered;
}

// richMessagesAfter returns the rich messages once batch is applied to
// previous: a full replay replaces them, and an update replaces its own.
// A replay that came the newest task first puts them in the order of the
// conversation: by task, the earliest first, and within a task as they
// came (richOrder); any that came after it go last.
export function richMessagesAfter(previous: RichMessage[], batch: EventBatch): RichMessage[] {
  if (!batch.replace && batch.richMessages.length === 0) return previous;
  const merged = mergeByKey(batch.replace ? [] : previous, batch.richMessages.map(withoutSeq), richMessageKey);
  const order = batch.richOrder;
  if (!order) return merged;
  const last: RichPlace = {block: Number.POSITIVE_INFINITY, index: Number.POSITIVE_INFINITY};
  const placed = merged.map((message) => ({message, place: order.get(richMessageKey(message)) ?? last}));
  // A stable sort: those that came after the replay keep their order.
  placed.sort((a, b) => compare(a.place.block, b.place.block) || compare(a.place.index, b.place.index));
  return placed.map(({message}) => message);
}

// compare orders numbers, the infinite ones too (whose difference is NaN).
function compare(a: number, b: number): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

// tasksAfter returns the tasks once batch is applied to previous. Tasks
// keep their seq, as requests do, so an update never replaces a later one.
// While a full replay is on its way they stay as they were: the transcript
// is grouped by them, and without them a long replay's progress would show
// each steered message as a task of its own, with a guessed status, until
// they came. Those whose message the replay sends as another message
// (another conversation's, after a restart without a state database) go.
// The batch that completes the replay replaces them.
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
// with mergeRequests: requests keep their seq, which decides between an
// event and a state read from the REST API. While a full replay is on its
// way they stay as they were, so its progress doesn't take the decision
// cards away until its requests come. Those the agent made before the
// replay's first message (replayFrom) go: they belong to another
// conversation, one the server replaced since (a restart, a new session).
// The batch that completes the replay replaces them.
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

// Applies a batch of upserts keyed by keyOf in one pass: existing entries are
// replaced in place, new ones appended in arrival order. Replaying a long
// session is thousands of updates; doing a copy and a linear search per
// update made reloads quadratic.
export function mergeByKey<T>(previous: T[], updates: Iterable<T>, keyOf: (item: T) => string | undefined): T[] {
  const next = previous.slice();
  const index = new Map<string, number>();
  next.forEach((item, i) => {
    const key = keyOf(item);
    if (key !== undefined) index.set(key, i);
  });
  for (const item of updates) {
    const key = keyOf(item);
    const at = key === undefined ? undefined : index.get(key);
    if (at === undefined) {
      if (key !== undefined) index.set(key, next.length);
      next.push(item);
    } else {
      next[at] = item;
    }
  }
  return next;
}
