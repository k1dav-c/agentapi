import type {DraftMessage, Message, RichContentBlock, RichMessage} from "@/lib/conversation";
import {sameValue, type ServerStatus} from "@/lib/agent-status";
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

// A conversation as the transcript shows it: the messages before the first
// task, and the tasks.
export interface Timeline {
  prelude: (Message | DraftMessage)[];
  tasks: TaskSection[];
}

// The tool calls of a conversation, in the order they started, and its
// timeline.
export interface Transcript {
  toolCalls: ToolCall[];
  timeline: Timeline;
}

// What a rich message adds to the transcript.
interface MessageParts {
  // The message's time, parsed: the tasks are found by binary search over
  // their prompts' times, and parsing in every step was most of the work.
  time: number;
  // Its blocks that start or finish a tool call.
  toolBlocks: RichContentBlock[];
  // What an agent's message shows in its task, in order: its text and
  // thinking, and the IDs of the tool calls it makes (their objects change
  // as the calls run).
  activity: (TaskActivity | string)[];
}

// A block of a tool call, the message it is in, and that message's place
// among the rich messages.
interface ToolTouch {
  block: RichContentBlock;
  message: RichMessage;
  index: number;
}

interface CallEntry {
  call: ToolCall;
  // Its timestamp, parsed.
  time: number;
  // The blocks the call was made from: the same blocks make the same call.
  touches: ToolTouch[];
  // Its place among the calls, in the order they started.
  position: number;
}

// What a task holds besides its messages: the IDs of its tool calls, and
// the places of the agent's rich messages put into it.
interface TaskItems {
  calls: string[];
  parts: number[];
}

// TimelineBuilder builds the transcript of a conversation as messages
// stream in, and makes again only what changed. The chat keeps a message's
// object until the message changes (mergeByKey), so what a build gets
// usually starts with what the last one got: it remakes the tool calls the
// rest touches, and the tasks from the first one something changed in. The
// tasks before it and the calls that didn't change keep their objects, and
// their memoized components don't render again. Only when the prompts'
// times are out of order is everything put into its task again. What a
// rich message adds (its parsed time, its text and thinking) is kept by
// message object, and a task or tool call made again that is the same as
// before (a replay sends everything again, as new objects) keeps its
// object.
export class TimelineBuilder {
  private parts = new WeakMap<RichMessage, MessageParts>();
  private promptTimes = new WeakMap<Message | DraftMessage, number>();
  private toolActivity = new WeakMap<ToolCall, TaskActivity>();

  // The last build: what it was given, and what it made.
  private messages: (Message | DraftMessage)[] = [];
  private richMessages: RichMessage[] = [];
  private calls = new Map<string, CallEntry>();
  // The IDs of the calls, in the order they started.
  private order: string[] = [];
  // Where each task starts among the messages, its prompt's time, and what
  // it holds.
  private starts: number[] = [];
  private times: number[] = [];
  private items: TaskItems[] = [];
  private last: Transcript = {toolCalls: [], timeline: {prelude: [], tasks: []}};

  build(messages: (Message | DraftMessage)[], richMessages: RichMessage[]): Transcript {
    const oldRich = this.richMessages;
    const unchanged = commonPrefix(oldRich, richMessages);
    const {changes, orderChanged} = this.collectToolCalls(richMessages, unchanged);
    const timeline = this.buildTimeline(messages, richMessages, oldRich, unchanged, changes);
    const toolCalls = changes.size > 0 || orderChanged ? this.order.map((id) => this.calls.get(id)!.call) : this.last.toolCalls;
    if (toolCalls !== this.last.toolCalls || timeline !== this.last.timeline) this.last = {toolCalls, timeline};
    return this.last;
  }

  private partsOf(message: RichMessage): MessageParts {
    let parts = this.parts.get(message);
    if (parts) return parts;
    const activity: (TaskActivity | string)[] = [];
    const toolBlocks: RichContentBlock[] = [];
    message.content.forEach((block, index) => {
      if ((block.type === "tool_use" || block.type === "tool_result") && block.tool_use_id) toolBlocks.push(block);
      if (message.role !== "assistant") return;
      if (block.type === "text" && block.text) {
        activity.push({
          type: "message",
          key: `rich-message-${message.message_id}-${index}`,
          message: {id: -1, role: "assistant", content: block.text, time: message.timestamp},
        });
      }
      if (block.type === "thinking" && block.thinking) {
        activity.push({
          type: "thinking",
          key: `rich-thinking-${message.message_id}-${index}`,
          content: block.thinking,
          timestamp: message.timestamp,
        });
      }
      if (block.type === "tool_use" && block.tool_use_id) activity.push(block.tool_use_id);
    });
    parts = {time: Date.parse(message.timestamp), toolBlocks, activity};
    this.parts.set(message, parts);
    return parts;
  }

  private promptTime(message: Message | DraftMessage): number {
    let time = this.promptTimes.get(message);
    if (time === undefined) {
      time = message.time ? Date.parse(message.time) : Number.NaN;
      this.promptTimes.set(message, time);
    }
    return time;
  }

  // collectToolCalls remakes the tool calls that the rich messages from
  // unchanged on touch, or touched before. It returns those that changed (a
  // new object, a new place, or gone) with their entries before, undefined
  // for a new call. A call made again the same as before (its message came
  // again) keeps its object and isn't a change.
  private collectToolCalls(richMessages: RichMessage[], unchanged: number) {
    const old = this.richMessages;
    // The blocks of each call the changed messages touch, but theirs.
    const touched = new Map<string, ToolTouch[]>();
    for (let i = unchanged; i < old.length; i++) {
      for (const block of this.partsOf(old[i]).toolBlocks) {
        const id = block.tool_use_id!;
        if (!touched.has(id)) touched.set(id, this.calls.get(id)!.touches.filter((touch) => touch.index < unchanged));
      }
    }
    // The calls that started in the changed messages start again where
    // they now do: the order is by start, so they are its end.
    let cut = this.order.length;
    while (cut > 0 && this.calls.get(this.order[cut - 1])!.touches[0].index >= unchanged) cut--;
    const removed = this.order.splice(cut);
    for (let i = unchanged; i < richMessages.length; i++) {
      for (const block of this.partsOf(richMessages[i]).toolBlocks) {
        const id = block.tool_use_id!;
        let touches = touched.get(id);
        if (!touches) touched.set(id, (touches = this.calls.get(id)?.touches.slice() ?? []));
        if (touches.length === 0) this.order.push(id);
        touches.push({block, message: richMessages[i], index: i});
      }
    }
    const positions = new Map(this.order.slice(cut).map((id, i) => [id, cut + i]));
    const changes = new Map<string, CallEntry | undefined>();
    for (const [id, touches] of touched) {
      const previous = this.calls.get(id);
      if (touches.length === 0) {
        this.calls.delete(id);
        changes.set(id, previous);
        continue;
      }
      const position = positions.get(id) ?? previous!.position;
      const same = previous !== undefined && sameTouches(previous.touches, touches);
      if (same && position === previous.position) continue;
      const made = same ? previous.call : makeToolCall(id, touches);
      const call = previous && sameValue(previous.call, made) ? previous.call : made;
      this.calls.set(id, {call, time: call === previous?.call ? previous.time : Date.parse(call.timestamp), touches, position});
      if (call !== previous?.call || position !== previous.position) changes.set(id, previous);
    }
    this.richMessages = richMessages;
    return {changes, orderChanged: !sameItems(removed, this.order.slice(cut))};
  }

  // buildTimeline groups the messages into tasks, each a user's message and
  // what followed it, with the tool calls and the agent's structured
  // messages (text, thinking, tool use) put into the task they happened in:
  // the last whose prompt is at or before them.
  private buildTimeline(
    messages: (Message | DraftMessage)[],
    richMessages: RichMessage[],
    oldRich: RichMessage[],
    unchanged: number,
    changes: Map<string, CallEntry | undefined>,
  ): Timeline {
    const starts: number[] = [];
    messages.forEach((message, i) => {
      if (message.role === "user") starts.push(i);
    });
    const times = starts.map((start) => this.promptTime(messages[start]));
    const changed = this.firstChangedTask(messages, starts, times, richMessages, oldRich, unchanged, changes);
    const previous = this.last.timeline;
    this.messages = messages;
    this.starts = starts;
    this.times = times;
    if (changed === starts.length && changed === previous.tasks.length) return previous;
    const from = Math.max(0, changed);

    // What the tasks from the first changed one on hold: what they held,
    // and what changed; from the first task, everything.
    let callIDs: Iterable<string>;
    const parts: number[] = [];
    if (from === 0) {
      callIDs = this.order;
      richMessages.forEach((message, i) => {
        if (message.role === "assistant") parts.push(i);
      });
    } else {
      const ids = new Set<string>();
      for (const items of this.items.slice(from)) {
        for (const id of items.calls) ids.add(id);
        for (const i of items.parts) if (i < unchanged) parts.push(i);
      }
      for (const id of changes.keys()) ids.add(id);
      for (let i = unchanged; i < richMessages.length; i++) if (richMessages[i].role === "assistant") parts.push(i);
      parts.sort((a, b) => a - b);
      callIDs = ids;
    }
    const items = starts.slice(from).map((): TaskItems => ({calls: [], parts: []}));
    const entries: CallEntry[][] = items.map(() => []);
    for (const id of callIDs) {
      const entry = this.calls.get(id);
      const index = entry ? taskAt(times, entry.time) : -1;
      if (index >= from) entries[index - from].push(entry!);
    }
    for (const i of parts) {
      const index = taskAt(times, this.partsOf(richMessages[i]).time);
      if (index >= from) items[index - from].parts.push(i);
    }

    const tasks = previous.tasks.slice(0, from);
    const byKey = new Map(previous.tasks.slice(from).map((task) => [task.key, task]));
    for (let j = from; j < starts.length; j++) {
      const callEntries = entries[j - from];
      if (from > 0) callEntries.sort((a, b) => a.position - b.position);
      items[j - from].calls = callEntries.map((entry) => entry.call.id);
      const prompt = messages[starts[j]];
      const task: TaskSection = {
        key: `task-${prompt.id ?? `draft-${j}`}`,
        prompt,
        responses: messages.slice(starts[j] + 1, starts[j + 1] ?? messages.length),
        toolCalls: callEntries.map((entry) => entry.call),
        richActivity: this.activityOf(items[j - from].parts, richMessages),
      };
      const old = byKey.get(task.key);
      tasks.push(old && sameTask(old, task) ? old : task);
    }
    this.items = [...this.items.slice(0, from), ...items];
    const prelude = from === 0 ? messages.slice(0, starts[0] ?? messages.length) : previous.prelude;
    const sharedPrelude = sameItems(previous.prelude, prelude, sameValue) ? previous.prelude : prelude;
    if (sharedPrelude === previous.prelude && sameItems(previous.tasks, tasks)) return previous;
    return {prelude: sharedPrelude, tasks};
  }

  // firstChangedTask returns the first task that may hold something else
  // than it did: the tasks before it hold what they held. It is -1 when the
  // messages before the first task may have changed, or the tasks' prompt
  // times are out of order (a failed draft among them): then where
  // something goes depends on all of them.
  private firstChangedTask(
    messages: (Message | DraftMessage)[],
    starts: number[],
    times: number[],
    richMessages: RichMessage[],
    oldRich: RichMessage[],
    unchanged: number,
    changes: Map<string, CallEntry | undefined>,
  ): number {
    if (!inOrder(times) || !inOrder(this.times)) return -1;
    let from = starts.length;
    const at = (list: number[], time: number) => {
      const index = taskAt(list, time);
      if (index >= 0) from = Math.min(from, index);
    };
    // The first message that changed, and the task it is in.
    const changedMessage = commonPrefix(this.messages, messages);
    if (changedMessage < Math.max(this.messages.length, messages.length)) {
      from = Math.min(from, lastAtOrBefore(starts, changedMessage), lastAtOrBefore(this.starts, changedMessage));
    }
    // A new or changed prompt time moves what comes after it from the task
    // before.
    let same = 0;
    while (same < Math.min(times.length, this.times.length) && Object.is(times[same], this.times[same])) same++;
    if (same < Math.max(times.length, this.times.length)) from = Math.min(from, same - 1);
    // The agent's rich messages that changed, where they were and are.
    for (let i = unchanged; i < oldRich.length; i++) if (oldRich[i].role === "assistant") at(this.times, this.partsOf(oldRich[i]).time);
    for (let i = unchanged; i < richMessages.length; i++) if (richMessages[i].role === "assistant") at(times, this.partsOf(richMessages[i]).time);
    // The tool calls that changed, and the messages that show them.
    for (const [id, previous] of changes) {
      const entry = this.calls.get(id);
      for (const [list, made] of [[this.times, previous], [times, entry]] as const) {
        if (!made) continue;
        at(list, made.time);
        for (const {block, message} of made.touches) {
          if (block.type === "tool_use" && message.role === "assistant") at(list, this.partsOf(message).time);
        }
      }
    }
    return from;
  }

  // The activity of an agent's rich messages, in order.
  private activityOf(parts: number[], richMessages: RichMessage[]): TaskActivity[] {
    const activity: TaskActivity[] = [];
    for (const i of parts) {
      for (const item of this.partsOf(richMessages[i]).activity) {
        if (typeof item !== "string") {
          activity.push(item);
          continue;
        }
        const call = this.calls.get(item)?.call;
        if (call) activity.push(this.toolActivityOf(call));
      }
    }
    return activity;
  }

  private toolActivityOf(call: ToolCall): TaskActivity {
    let activity = this.toolActivity.get(call);
    if (!activity) {
      activity = {type: "tool", key: `rich-tool-${call.id}`, toolCall: call};
      this.toolActivity.set(call, activity);
    }
    return activity;
  }
}

// buildTranscript builds the transcript of a conversation once, as for a
// subagent's run.
export function buildTranscript(messages: (Message | DraftMessage)[], richMessages: RichMessage[]): Transcript {
  return new TimelineBuilder().build(messages, richMessages);
}

// A tool call's status as a block reports it; undefined when it doesn't, or
// says something the chat doesn't know.
function toolStatus(status: string | undefined): ToolCall["status"] {
  return status === "running" || status === "completed" || status === "failed" ? status : undefined;
}

// makeToolCall makes a tool call from its blocks, in order: the tool use
// (the agent may send it again as it runs), then the result.
function makeToolCall(id: string, touches: ToolTouch[]): ToolCall {
  let call: ToolCall | undefined;
  for (const {block, message} of touches) {
    if (block.type === "tool_use") {
      call = {
        ...call,
        id,
        name: block.tool_name || "Tool",
        input: block.tool_input,
        status: toolStatus(block.status) ?? "running",
        timestamp: message.timestamp,
      };
      continue;
    }
    const images = block.images?.flatMap((image) => (image.url ? [image.url] : []));
    call = {
      id,
      name: call?.name || "Tool",
      input: call?.input,
      result: block.text ?? "",
      ...(images?.length ? {images} : {}),
      status: toolStatus(block.status) ?? (block.is_error ? "failed" : "completed"),
      isError: block.status === "failed" || block.is_error,
      timestamp: call?.timestamp || message.timestamp,
      resultTimestamp: message.timestamp,
    };
  }
  return call!;
}

function sameTouches(a: ToolTouch[], b: ToolTouch[]): boolean {
  return a.length === b.length && a.every((touch, i) => touch.block === b[i].block && touch.message === b[i].message && touch.index === b[i].index);
}

// How many items a and b start with that are the same objects.
function commonPrefix<T>(a: T[], b: T[]): number {
  const length = Math.min(a.length, b.length);
  let i = 0;
  while (i < length && a[i] === b[i]) i++;
  return i;
}

// The index of the last of starts at or before index; -1 when none is.
function lastAtOrBefore(starts: number[], index: number): number {
  let low = 0;
  let high = starts.length - 1;
  let match = -1;
  while (low <= high) {
    const middle = (low + high) >> 1;
    if (starts[middle] <= index) {
      match = middle;
      low = middle + 1;
    } else {
      high = middle - 1;
    }
  }
  return match;
}

// Whether prompt times are in order, those without a time (drafts) last:
// the binary search of taskAt needs it to find the same task for what
// didn't change.
function inOrder(times: number[]): boolean {
  for (let i = 1; i < times.length; i++) {
    if (Number.isNaN(times[i - 1]) ? !Number.isNaN(times[i]) : times[i] < times[i - 1]) return false;
  }
  return true;
}

// The index of the task a time falls in: the last whose prompt is at or
// before it. Prompts are in time order; one without a time (a draft) is
// at the end. -1 when the time is before the first task, or not a time.
function taskAt(promptTimes: number[], time: number): number {
  if (Number.isNaN(time)) return -1;
  let low = 0;
  let high = promptTimes.length - 1;
  let match = -1;
  while (low <= high) {
    const middle = (low + high) >> 1;
    const promptTime = promptTimes[middle];
    if (Number.isNaN(promptTime) || promptTime > time) {
      high = middle - 1;
    } else {
      match = middle;
      low = middle + 1;
    }
  }
  return match;
}

// Whether two tasks show the same. Messages and tool calls that came again
// as new objects (a reconnect replays them all, an agent sends a message
// again as it grows) are compared by what they hold.
function sameTask(a: TaskSection, b: TaskSection): boolean {
  return (
    sameValue(a.prompt, b.prompt) &&
    sameItems(a.responses, b.responses, sameValue) &&
    sameItems(a.toolCalls, b.toolCalls) &&
    sameItems(a.richActivity, b.richActivity, sameActivity)
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

// Whether a and b have the same items: the same objects, or ones same says
// are the same.
function sameItems<T>(a: T[], b: T[], same: (x: T, y: T) => boolean = Object.is): boolean {
  return a.length === b.length && a.every((item, i) => item === b[i] || same(item, b[i]));
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
