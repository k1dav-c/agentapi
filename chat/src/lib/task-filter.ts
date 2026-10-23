import type {ServerStatus} from "./agent-status";
import type {ServerTask} from "./server-tasks";
import {taskMatchesQuery} from "./task-actions";
import {countTaskMatches, getTaskStatus, toSearchableTask, type TaskFilter, type TaskSection, type TaskStatus} from "./task-timeline";

// A task the transcript shows: its place among all tasks, its status (and
// the server's task it has it from), and how often the search query occurs
// in it.
export interface FilteredTask {
  task: TaskSection;
  index: number;
  status: TaskStatus;
  serverTask?: ServerTask;
  matchCount: number;
}

// The tasks that pass filter and contain query (all of them when both are
// unset), in order. taskByMessage holds the server's tasks by the user
// messages that belong to them (tasksByMessage).
export function filterTasks(
  tasks: TaskSection[],
  serverStatus: ServerStatus,
  filter: TaskFilter,
  query: string,
  taskByMessage: ReadonlyMap<number, ServerTask> = new Map(),
): FilteredTask[] {
  return tasks
    .map((task, index) => {
      const serverTask = task.prompt.id === undefined ? undefined : taskByMessage.get(task.prompt.id);
      return {
        task,
        index,
        status: getTaskStatus(task, index, tasks.length, serverStatus, serverTask),
        serverTask,
        matchCount: countTaskMatches(task, query),
      };
    })
    .filter(({task, status, matchCount}) => {
      const matchesFilter =
        filter === "all" || (filter === "tool-error" ? task.toolCalls.some((tool) => tool.isError) : status === filter);
      return matchesFilter && (query.trim() === "" || matchCount > 0 || taskMatchesQuery(toSearchableTask(task), query));
    });
}
