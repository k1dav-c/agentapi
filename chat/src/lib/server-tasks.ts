// The conversation's tasks as the server derives them (GET /tasks, the
// task_update event): a user's message and the agent's turn on it, until
// the agent is done. Messages the user adds while one runs (steering)
// belong to it. The chat groups the messages into tasks itself
// (lib/task-timeline.ts) and takes from these what only the server knows:
// whether a task is done, how it ended, and when.

import type {components} from "./api-schema";

type Schemas = components["schemas"];

export type ServerTaskStatus = Schemas["TaskStatus"];

// A task as the chat holds it: its lists are never null, and seq is the
// change sequence of the task_update it came from.
export type ServerTask = Omit<Schemas["Task"], "errors" | "steered"> & {
  errors: string[];
  steered: number[];
  seq?: number;
};

// normalizeTask returns task with empty lists for missing or null ones.
export function normalizeTask<T extends Schemas["Task"] & {seq?: number}>(task: T): Omit<T, "errors" | "steered"> & ServerTask {
  return {...task, errors: task.errors ?? [], steered: task.steered ?? []};
}

// mergeTasks applies task updates: a task keeps its place, a new one comes
// last (the server sends them in the order they started), and a later seq
// wins over an earlier one.
export function mergeTasks(previous: ServerTask[], updates: Iterable<ServerTask>): ServerTask[] {
  const next = previous.slice();
  const index = new Map(next.map((task, i) => [task.id, i]));
  for (const update of updates) {
    const at = index.get(update.id);
    if (at === undefined) {
      index.set(update.id, next.length);
      next.push(update);
    } else if (next[at].seq === undefined || update.seq === undefined || update.seq >= next[at].seq) {
      next[at] = update;
    }
  }
  return next;
}

// What to say of how a task ended, beside its status: the first error of
// a failed task, what the agent said of the limit that stopped one.
export function taskStatusDetail(task: ServerTask | undefined): string | undefined {
  if (task?.status === "failed") return task.errors[0];
  if (task?.status === "limited") return task.limit?.message;
  return undefined;
}

// tasksByMessage returns the tasks by the user messages that belong to
// them: the one that started each, and those steered into it.
export function tasksByMessage(tasks: ServerTask[]): Map<number, ServerTask> {
  const byMessage = new Map<number, ServerTask>();
  for (const task of tasks) {
    byMessage.set(task.id, task);
    for (const id of task.steered) byMessage.set(id, task);
  }
  return byMessage;
}

// latestTaskEnd returns when the latest task ended, undefined while it runs
// or before the first: what the user hasn't seen until a client shows the
// conversation once it ended.
export function latestTaskEnd(tasks: readonly ServerTask[]): string | undefined {
  return tasks.at(-1)?.ended_at;
}
