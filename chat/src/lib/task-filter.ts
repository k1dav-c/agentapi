import type {ServerStatus} from "./agent-status";
import {taskMatchesQuery} from "./task-actions";
import {countTaskMatches, getTaskStatus, toSearchableTask, type TaskFilter, type TaskSection, type TaskStatus} from "./task-timeline";

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
