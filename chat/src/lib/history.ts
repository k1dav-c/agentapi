// The conversation's earlier tasks. The server sends the latest tasks
// when the chat connects (the full /events replay) and keeps the others
// for the chat to read a page at a time (GET /tasks, /messages and
// /rich-messages with before=): a long conversation loads as fast as a
// short one. The chat puts each page it reads before what it holds.

import {isDraftMessage, richMessageKey, type DraftMessage, type Message, type RichMessage} from "./conversation";
import type {ServerTask} from "./server-tasks";

// How many tasks a page the chat reads holds.
export const HISTORY_PAGE_TASKS = 20;

// A page of the conversation's history: the tasks that started before
// the task before, with what belongs to them.
export interface HistoryPage {
  before: number;
  tasks: ServerTask[];
  messages: Message[];
  richMessages: RichMessage[];
  // Tasks started before the page's first.
  hasMore: boolean;
}

// earlierTasks returns how many of the server's tasks came before those
// the chat holds: the number of its first, less one. A server that
// predates the numbers sends every task, so there are none.
export function earlierTasks(tasks: readonly ServerTask[]): number {
  const first = tasks[0]?.number;
  return first ? Math.max(0, first - 1) : 0;
}

interface History {
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  tasks: ServerTask[];
}

// prependPage returns state with page put before what it holds, or state
// itself when the page doesn't end where it starts: the conversation was
// replaced, or the page came already.
export function prependPage<S extends History>(state: S, page: HistoryPage): S {
  if (state.tasks[0]?.id !== page.before) return state;
  const tasks = new Set(state.tasks.map((task) => task.id));
  const messages = new Set(state.messages.flatMap((message) => (isDraftMessage(message) ? [] : [message.id])));
  const rich = new Set(state.richMessages.map(richMessageKey));
  const newTasks = page.tasks.filter((task) => !tasks.has(task.id));
  if (newTasks.length === 0 && page.messages.length === 0) return state;
  return {
    ...state,
    tasks: [...newTasks, ...state.tasks],
    messages: [...page.messages.filter((message) => !messages.has(message.id)), ...state.messages],
    richMessages: [...page.richMessages.filter((message) => !rich.has(richMessageKey(message))), ...state.richMessages],
  };
}
