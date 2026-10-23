// The events of GET /events (server-sent events), and of the other event
// streams, as the server sends them. Their types come from the API schema
// (lib/api-schema.ts), so they follow what the server says it sends; a
// payload is normalized where it arrives (use-event-stream.ts) into what
// the rest of the chat holds, where no list is null.

import type {components} from "./api-schema";
import {normalizeRichMessage, type Message, type RichMessage} from "./conversation";
import type {Capabilities} from "./dispatch";
import {normalizeRequest, type NativeRequest} from "./native-requests";
import {normalizeTask, type ServerTask} from "./server-tasks";
import type {QueuedMessage} from "./queue";

type Schemas = components["schemas"];

export type SessionSyncEvent = Schemas["SessionSyncBody"];
export type MessageUpdateEvent = Schemas["MessageUpdateBody"];
export type RichMessageUpdateEvent = Schemas["RichMessageUpdateBody"];
export type RequestUpdateEvent = Schemas["RequestUpdateBody"];
export type AgentErrorEvent = Schemas["ErrorBody"];
// The conversation's tasks and the message queue, sent to sync clients
// (?sync=1) only.
export type TaskUpdateEvent = Schemas["TaskUpdateBody"];
export type QueueUpdateEvent = Schemas["QueueUpdateBody"];
// The screen of TTY mode (GET /internal/screen), and the shell terminal's
// output and end (GET /terminal).
export type ScreenEvent = Schemas["ScreenUpdateBody"];
export type TerminalOutputEvent = Schemas["TerminalOutputBody"];
export type TerminalExitEvent = Schemas["TerminalExitBody"];

// A server older than the chat (the chat in development, or served apart
// from it) leaves out what it didn't have yet: the transport (GET /status
// says then), the lifecycle, the version, the terminal prompt and some
// capabilities.
type Missing = "transport" | "lifecycle" | "agentapi_version" | "terminal_prompt";
export type StatusChangeEvent = Omit<Schemas["StatusChangeBody"], Missing | "capabilities"> &
  Partial<Pick<Schemas["StatusChangeBody"], Missing>> & {capabilities?: Capabilities};

// A message_update as a message of the chat's, with the change's seq.
export function messageFromEvent(event: MessageUpdateEvent): Message & {seq: number} {
  return {role: event.role, content: event.message, id: event.id, time: event.time, seq: event.seq};
}

// A rich_message_update as a rich message, with the change's seq.
export function richMessageFromEvent(event: RichMessageUpdateEvent): RichMessage & {seq: number} {
  return normalizeRichMessage(event);
}

// A request_update as a request, with the change's seq.
export function requestFromEvent(event: RequestUpdateEvent): NativeRequest {
  return normalizeRequest(event);
}

// A queue_update's queue (never null).
export function queueFromEvent(event: QueueUpdateEvent): QueuedMessage[] {
  return event.queue ?? [];
}

// A task_update as a task, with the change's seq.
export function taskFromEvent(event: TaskUpdateEvent): ServerTask & {seq: number} {
  return normalizeTask(event);
}
