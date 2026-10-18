import type {DraftMessage, Message, RichMessage} from "@/components/chat-provider";
import type {ServerStatus} from "@/lib/agent-status";
import { formatToolInput } from "./tool-format";

export interface ToolCall {
  id: string;
  name: string;
  input?: unknown;
  result?: string;
  // Paths (relative to the AgentAPI URL) of the images in the result.
  images?: string[];
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
  if (index === taskCount - 1 && serverStatus === "running") return "running";
  // Agents often run commands that fail on purpose or recover from one (a
  // grep with no match exits 1), so a failed command only fails the task
  // when it is the task's last step: the agent stopped there.
  const lastStep = task.richActivity.findLast((item) => item.type !== "thinking");
  if (lastStep) return lastStep.type === "tool" && lastStep.toolCall.isError ? "failed" : "completed";
  return task.toolCalls.at(-1)?.isError ? "failed" : "completed";
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
        const images = block.images?.flatMap((image) => (image.url ? [image.url] : []));
        calls.set(block.tool_use_id, {
          id: block.tool_use_id,
          name: existing?.name || "Tool",
          input: existing?.input,
          result: block.text ?? "",
          ...(images?.length ? {images} : {}),
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

// A conversation as the transcript shows it: the messages before the first
// task, and the tasks.
export interface Timeline {
  prelude: (Message | DraftMessage)[];
  tasks: TaskSection[];
}

// buildTimeline groups the messages into tasks, each a user's message and
// what followed it, with the tool calls and the agent's structured messages
// (text, thinking, tool use) put into the task they happened in.
export function buildTimeline(messages: (Message | DraftMessage)[], richMessages: RichMessage[], toolCalls: ToolCall[]): Timeline {
  const tasks: TaskSection[] = [];
  const prelude: (Message | DraftMessage)[] = [];

  for (const message of messages) {
    if (message.role === "user") {
      tasks.push({
        key: `task-${message.id ?? `draft-${tasks.length}`}`,
        prompt: message,
        responses: [],
        toolCalls: [],
        richActivity: [],
      });
    } else if (tasks.length > 0) {
      tasks.at(-1)!.responses.push(message);
    } else {
      prelude.push(message);
    }
  }

  for (const toolCall of toolCalls) {
    const target = findTaskAtTime(tasks, toolCall.timestamp);
    target?.toolCalls.push(toolCall);
  }

  const callsByID = new Map<string, ToolCall>(toolCalls.map((tc) => [tc.id, tc]));
  for (const message of richMessages) {
    if (message.role !== "assistant") continue;
    const target = findTaskAtTime(tasks, message.timestamp);
    if (!target) continue;

    message.content.forEach((block, index) => {
      if (block.type === "text" && block.text) {
        target.richActivity.push({
          type: "message",
          key: `rich-message-${message.message_id}-${index}`,
          message: {
            id: -1,
            role: "assistant",
            content: block.text,
            time: message.timestamp,
          },
        });
      }
      if (block.type === "thinking" && block.thinking) {
        target.richActivity.push({
          type: "thinking",
          key: `rich-thinking-${message.message_id}-${index}`,
          content: block.thinking,
          timestamp: message.timestamp,
        });
      }
      if (block.type === "tool_use" && block.tool_use_id) {
        const tc = callsByID.get(block.tool_use_id);
        if (tc) {
          target.richActivity.push({
            type: "tool",
            key: `rich-tool-${block.tool_use_id}`,
            toolCall: tc,
          });
        }
      }
    });
  }

  return {prelude, tasks};
}

// The transcript rebuilds the timeline whenever a message streams, but most
// of it didn't change. These put back what the previous build had where the
// new one has the same, so unchanged tasks and tool calls keep their objects
// and their components (memoized) don't render again.

// shareToolCalls returns next with the calls of previous that are the same.
export function shareToolCalls(previous: ToolCall[], next: ToolCall[]): ToolCall[] {
  const byID = new Map(previous.map((call) => [call.id, call]));
  return next.map((call) => {
    const old = byID.get(call.id);
    return old && sameFields(old, call) ? old : call;
  });
}

// shareTimeline returns next with the tasks of previous that are the same.
export function shareTimeline(previous: Timeline | null, next: Timeline): Timeline {
  if (!previous) return next;
  const byKey = new Map(previous.tasks.map((task) => [task.key, task]));
  const tasks = next.tasks.map((task) => {
    const old = byKey.get(task.key);
    return old && sameTask(old, task) ? old : task;
  });
  const prelude = sameItems(previous.prelude, next.prelude) ? previous.prelude : next.prelude;
  if (prelude === previous.prelude && sameItems(previous.tasks, tasks)) return previous;
  return {prelude, tasks};
}

function sameTask(a: TaskSection, b: TaskSection): boolean {
  return (
    a.prompt === b.prompt &&
    sameItems(a.responses, b.responses) &&
    sameItems(a.toolCalls, b.toolCalls) &&
    a.richActivity.length === b.richActivity.length &&
    a.richActivity.every((item, i) => sameActivity(item, b.richActivity[i]))
  );
}

function sameActivity(a: TaskActivity, b: TaskActivity): boolean {
  if (a.key !== b.key) return false;
  switch (a.type) {
    case "message":
      return b.type === "message" && a.message.content === b.message.content && a.message.time === b.message.time;
    case "thinking":
      return b.type === "thinking" && a.content === b.content && a.timestamp === b.timestamp;
    case "tool":
      return b.type === "tool" && a.toolCall === b.toolCall;
  }
}

function sameItems<T>(a: T[], b: T[]): boolean {
  return a.length === b.length && a.every((item, i) => item === b[i]);
}

// sameFields reports whether a and b have the same fields, each the same
// value or object.
function sameFields(a: object, b: object): boolean {
  const left = a as Record<string, unknown>;
  const right = b as Record<string, unknown>;
  const keys = Object.keys(left);
  return keys.length === Object.keys(right).length && keys.every((key) => sameValueOrItems(left[key], right[key]));
}

function sameValueOrItems(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  return Array.isArray(a) && Array.isArray(b) && sameItems(a, b);
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
//
// transcriptOnly (the native transport) never shows the agent messages: they
// are the transcript's text joined, so they would repeat it. A message
// steered into a running turn starts a task whose reply belongs to the
// transcript of the task before it.
export function getTaskActivity(
  task: TaskSection,
  {live = false, transcriptOnly = false}: {live?: boolean; transcriptOnly?: boolean} = {},
): TaskActivity[] {
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

  if (transcriptOnly) return [...task.richActivity, ...uncoveredTools];
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
