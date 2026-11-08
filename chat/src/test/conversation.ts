// Conversation fixtures for the tests of lib/pending-events and
// lib/conversation-state: messages, rich messages, tasks, requests and
// event batches with the fields those tests don't care about filled in.

import type {DraftMessage} from "@/lib/conversation";
import type {NativeRequest} from "@/lib/native-requests";
import type {EventBatch} from "@/lib/pending-events";
import type {ServerTask} from "@/lib/server-tasks";

export const message = (id: number, seq: number) => ({id, role: "agent" as const, content: `message ${id}`, time: "2026-10-22T10:00:00Z", seq});
export const prompt = (id: number, seq: number, time = "2026-10-22T10:00:00Z") => ({id, role: "user" as const, content: `prompt ${id}`, time, seq});
export const rich = (id: string, seq: number) => ({message_id: id, role: "assistant", timestamp: "2026-10-22T10:00:01Z", content: [], seq});
export const task = (id: number, seq: number, steered: number[] = []): ServerTask => ({
  id,
  status: "completed",
  prompt: `prompt ${id}`,
  started_at: "2026-10-22T10:00:00Z",
  errors: [],
  steered,
  seq,
});
export const request = (id: string, status: NativeRequest["status"], seq: number | undefined, createdAt = "2026-10-22T10:00:05Z"): NativeRequest => ({
  id,
  kind: "approval",
  title: `run ${id}`,
  status,
  created_at: createdAt,
  response_capability: "live",
  seq,
});
export const draft = (clientId: string, content: string, deliveryStatus: DraftMessage["deliveryStatus"] = "sending"): DraftMessage => ({
  role: "user",
  content,
  clientId,
  deliveryStatus,
});
export const batch = (fields: Partial<EventBatch>): EventBatch => ({
  replace: false,
  messages: [],
  richMessages: [],
  requests: [],
  tasks: [],
  held: false,
  replaceHeld: false,
  point: {epoch: "e1", seq: 0},
  ...fields,
});
