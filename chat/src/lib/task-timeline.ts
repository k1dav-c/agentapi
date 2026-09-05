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

export function getTaskActivity(task: TaskSection): TaskActivity[] {
  // When rich activity contains both text and tool entries, it preserves the
  // actual interleaving (text → tool → text → tool) from the JSONL session
  // log. Use it directly instead of the PTY blob + sorted tool calls, which
  // collapses all text into one entry and clusters tools together.
  const hasRichText = task.richActivity.some(
    item => item.type === "message" || item.type === "thinking",
  );
  const hasRichTools = task.richActivity.some(item => item.type === "tool");

  if (hasRichText && hasRichTools) {
    const coveredToolIDs = new Set(
      task.richActivity
        .filter((item): item is Extract<TaskActivity, {type: "tool"}> => item.type === "tool")
        .map(item => item.toolCall.id),
    );
    const result: TaskActivity[] = [...task.richActivity];
    // Append any tool calls not already covered by rich activity.
    for (const toolCall of task.toolCalls) {
      if (!coveredToolIDs.has(toolCall.id)) {
        result.push({
          type: "tool" as const,
          key: `tool-${toolCall.id}`,
          toolCall,
        });
      }
    }
    return result;
  }

  // Fallback: PTY responses + tool calls sorted by timestamp.
  // Used when the agent doesn't emit JSONL rich messages (no interleaving
  // info available).
  const result: TaskActivity[] = task.responses.map((message, index) => ({
    type: "message" as const,
    key: `response-${message.id ?? index}`,
    message,
  }));

  const coveredToolIDs = new Set(
    task.richActivity
      .filter((item) => item.type === "tool")
      .map((item) => (item as Extract<TaskActivity, {type: "tool"}>).toolCall.id),
  );

  for (const item of task.richActivity) {
    if (item.type === "tool") {
      result.push(item);
    }
  }

  for (const toolCall of task.toolCalls) {
    if (!coveredToolIDs.has(toolCall.id)) {
      result.push({
        type: "tool" as const,
        key: `tool-${toolCall.id}`,
        toolCall,
      });
    }
  }

  return result.sort((left, right) => {
    const leftTime =
      left.type === "message" ? left.message.time :
      left.type === "tool" ? left.toolCall.timestamp : left.timestamp;
    const rightTime =
      right.type === "message" ? right.message.time :
      right.type === "tool" ? right.toolCall.timestamp : right.timestamp;
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
