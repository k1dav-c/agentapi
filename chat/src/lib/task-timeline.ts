import type {
  DraftMessage,
  Message,
  RichMessage,
  ServerStatus,
} from "@/components/chat-provider";
import { formatToolInput } from "./tool-format";

export interface ToolCall {
  id: string;
  name: string;
  input?: unknown;
  result?: string;
  status?: "running" | "completed" | "failed";
  isError?: boolean;
  timestamp: string;
  resultTimestamp?: string;
}

export interface TaskSection {
  key: string;
  prompt: Message | DraftMessage;
  responses: (Message | DraftMessage)[];
  toolCalls: ToolCall[];
  richActivity: TaskActivity[];
}

export type TaskActivity =
  | {
      type: "message";
      key: string;
      message: Message | DraftMessage;
    }
  | {
      type: "tool";
      key: string;
      toolCall: ToolCall;
    }
  | {
      type: "thinking";
      key: string;
      content: string;
      timestamp: string;
    };

export type TaskStatus = "queued" | "running" | "completed" | "failed";
export type TaskFilter = "all" | TaskStatus | "tool-error";

export function getTaskStatus(
  task: TaskSection,
  index: number,
  taskCount: number,
  serverStatus: ServerStatus,
): TaskStatus {
  if (
    task.prompt.id === undefined &&
    (task.prompt as DraftMessage).deliveryStatus === "failed"
  ) {
    return "failed";
  }
  if (task.prompt.id === undefined) {
    return serverStatus === "running" ? "queued" : "running";
  }
  if (task.toolCalls.some((tool) => tool.isError)) return "failed";
  if (index === taskCount - 1 && serverStatus === "running") return "running";
  return "completed";
}

export function collectToolCalls(richMessages: RichMessage[]): ToolCall[] {
  const calls = new Map<string, ToolCall>();

  for (const message of richMessages) {
    for (const block of message.content) {
      if (block.type === "tool_use" && block.tool_use_id) {
        calls.set(block.tool_use_id, {
          ...calls.get(block.tool_use_id),
          id: block.tool_use_id,
          name: block.tool_name || "Tool",
          input: block.tool_input,
          status: block.status || "running",
          timestamp: message.timestamp,
        });
      }

      if (block.type === "tool_result" && block.tool_use_id) {
        const existing = calls.get(block.tool_use_id);
        calls.set(block.tool_use_id, {
          id: block.tool_use_id,
          name: existing?.name || "Tool",
          input: existing?.input,
          result: block.text ?? "",
          status: block.status || (block.is_error ? "failed" : "completed"),
          isError: block.status === "failed" || block.is_error,
          timestamp: existing?.timestamp || message.timestamp,
          resultTimestamp: message.timestamp,
        });
      }
    }
  }

  return [...calls.values()];
}

export function findTaskAtTime(tasks: TaskSection[], timestamp: string) {
  const targetTime = Date.parse(timestamp);
  if (Number.isNaN(targetTime)) return undefined;

  let low = 0;
  let high = tasks.length - 1;
  let match: TaskSection | undefined;
  while (low <= high) {
    const middle = Math.floor((low + high) / 2);
    const promptTime = tasks[middle].prompt.time
      ? Date.parse(tasks[middle].prompt.time!)
      : Number.NaN;
    if (Number.isNaN(promptTime) || promptTime > targetTime) {
      high = middle - 1;
    } else {
      match = tasks[middle];
      low = middle + 1;
    }
  }
  return match;
}

// Whether the task has a structured transcript (text or thinking from the
// agent's JSONL session log) rather than only parsed screen output.
export function hasStructuredTranscript(task: TaskSection): boolean {
  return task.richActivity.some(
    (item) => item.type === "message" || item.type === "thinking",
  );
}

// The activity to show for a task, in order.
//
// With a structured transcript, that transcript is the content: it keeps
// the real order of text, thinking and tools, and its Markdown renders
// properly. The text parsed from the terminal screen repeats the same
// answer (often cut off), so it is left out; TaskGroup offers it as
// "Terminal output". While the task is still running (live), the latest
// screen text is kept at the end, because the agent only writes a text
// block to its log once the block is finished.
//
// Without a transcript, the screen text and the tool calls are shown in
// time order.
export function getTaskActivity(task: TaskSection, {live = false}: {live?: boolean} = {}): TaskActivity[] {
  const coveredToolIDs = new Set(
    task.richActivity
      .filter((item): item is Extract<TaskActivity, {type: "tool"}> => item.type === "tool")
      .map((item) => item.toolCall.id),
  );
  const uncoveredTools: TaskActivity[] = task.toolCalls
    .filter((toolCall) => !coveredToolIDs.has(toolCall.id))
    .map((toolCall) => ({type: "tool" as const, key: `tool-${toolCall.id}`, toolCall}));
  const screenText = (message: Message | DraftMessage, index: number): TaskActivity => ({
    type: "message" as const,
    key: `response-${message.id ?? index}`,
    message,
  });

  if (hasStructuredTranscript(task)) {
    const result = [...task.richActivity, ...uncoveredTools];
    const latest = task.responses.at(-1);
    if (live && latest) result.push(screenText(latest, task.responses.length - 1));
    return result;
  }

  const result: TaskActivity[] = [
    ...task.responses.map(screenText),
    ...task.richActivity.filter((item) => item.type === "tool"),
    ...uncoveredTools,
  ];
  const timeOf = (item: TaskActivity) =>
    item.type === "message" ? item.message.time :
    item.type === "tool" ? item.toolCall.timestamp : item.timestamp;
  return result.sort((left, right) => {
    const leftTime = timeOf(left);
    const rightTime = timeOf(right);
    if (!leftTime) return 1;
    if (!rightTime) return -1;
    return Date.parse(leftTime) - Date.parse(rightTime);
  });
}

export function toSearchableTask(task: TaskSection) {
  const activity = getTaskActivity(task);
  return {
    prompt: task.prompt.content,
    responses: activity
      .filter(
        (item): item is Extract<TaskActivity, {type: "message"}> =>
          item.type === "message",
      )
      .map((item) => item.message.content),
    tools: task.toolCalls.map((tool) => ({
      name: tool.name,
      input: formatToolInput(tool.input),
      result: tool.result,
      isError: tool.isError,
    })),
  };
}

export function countTaskMatches(task: TaskSection, query: string) {
  const normalized = query.trim().toLocaleLowerCase();
  if (!normalized) return 0;
  const searchable = toSearchableTask(task);
  return [
    searchable.prompt,
    ...searchable.responses,
    ...searchable.tools.flatMap((tool) => [
      tool.name,
      tool.input ?? "",
      tool.result ?? "",
    ]),
  ].reduce(
    (total, content) =>
      total + content.toLocaleLowerCase().split(normalized).length - 1,
    0,
  );
}
