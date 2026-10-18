import type {ServerStatus} from "./agent-status";
import {contentFingerprint} from "./content-fingerprint";
import {taskMatchesQuery} from "./task-actions";
import {countTaskMatches, getTaskStatus, toSearchableTask, type TaskFilter, type TaskSection, type TaskStatus, type Timeline} from "./task-timeline";

// A task the transcript shows: its place among all tasks, its status, and
// how often the search query occurs in it.
export interface FilteredTask {
  task: TaskSection;
  index: number;
  status: TaskStatus;
  matchCount: number;
}

// The tasks that pass filter and contain query (all of them when both are
// unset), in order.
export function filterTasks(tasks: TaskSection[], serverStatus: ServerStatus, filter: TaskFilter, query: string): FilteredTask[] {
  return tasks
    .map((task, index) => ({
      task,
      index,
      status: getTaskStatus(task, index, tasks.length, serverStatus),
      matchCount: countTaskMatches(task, query),
    }))
    .filter(({task, status, matchCount}) => {
      const matchesFilter =
        filter === "all" || (filter === "tool-error" ? task.toolCalls.some((tool) => tool.isError) : status === filter);
      return matchesFilter && (query.trim() === "" || matchCount > 0 || taskMatchesQuery(toSearchableTask(task), query));
    });
}

// A string that changes whenever what the transcript shows does: the
// transcript scrolls along when it does.
export function timelineSignature(timeline: Timeline): string {
  return [
    ...timeline.prelude.map((message, index) => `prelude-${message.id ?? index}:${contentFingerprint(message.content)}`),
    ...timeline.tasks.map(
      (task) =>
        `${task.key}:${contentFingerprint(task.prompt.content)}:${task.responses
          .map((message) => contentFingerprint(message.content))
          .join(",")}:${task.toolCalls.map((tool) => `${tool.id}:${contentFingerprint(tool.result ?? "")}`).join(",")}`,
    ),
  ].join("|");
}
