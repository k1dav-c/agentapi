// The hub's view of the AgentAPIs it watches (agentapi hub serve): each
// instance as GET /api/instances and the /api/events stream give it, how a
// row shows it, and in which order.

import type {components} from "./api-schema";

export type Summary = components["schemas"]["SummaryBody"];
export type HubConnection = "connecting" | "connected" | "unreachable" | "stopped";

export interface HubInstance {
  id: string;
  name?: string;
  url: string;
  server_id?: string;
  hostname?: string;
  port?: number;
  cwd?: string;
  agent_type?: string;
  source: "webhook" | "manual";
  last_seen: string;
  connection: HubConnection;
  error?: string;
  summary?: Summary | null;
}

// InstanceState is what a row says of an instance, the most pressing
// first: it needs you, works, finished a task you haven't seen, waits,
// or can't be reached.
export type InstanceState = "needs-you" | "working" | "unread" | "idle" | "connecting" | "unreachable" | "stopped";

const stateRank: Record<InstanceState, number> = {
  "needs-you": 0,
  working: 1,
  unread: 2,
  idle: 3,
  connecting: 4,
  unreachable: 5,
  stopped: 6,
};

export function instanceState(instance: HubInstance): InstanceState {
  if (instance.connection === "stopped") return "stopped";
  if (instance.connection === "unreachable") return "unreachable";
  const summary = instance.summary;
  if (!summary) return "connecting";
  if (summary.attention !== "none" && summary.attention !== "exited") return "needs-you";
  if (summary.attention === "exited") return "stopped";
  if (summary.status === "running") return "working";
  if (summary.unread) return "unread";
  return "idle";
}

const attentionLabel: Record<Summary["attention"], string> = {
  login: "Needs login",
  request: "Needs your answer",
  dialog: "Needs your answer",
  limit: "Usage limit",
  exited: "Agent exited",
  none: "",
};

// stateLabel says what the row's state means.
export function stateLabel(instance: HubInstance): string {
  const state = instanceState(instance);
  switch (state) {
    case "needs-you":
      return attentionLabel[instance.summary!.attention] || "Needs you";
    case "working":
      return "Working";
    case "unread":
      return "Finished";
    case "idle":
      return "Idle";
    case "connecting":
      return "Connecting";
    case "unreachable":
      return "Unreachable";
    case "stopped":
      return instance.summary?.attention === "exited" ? "Agent exited" : "Stopped";
  }
}

export function displayName(instance: HubInstance): string {
  return instance.name || instance.id;
}

// sortInstances orders the rows: what needs you first, then by name.
export function sortInstances(instances: Iterable<HubInstance>): HubInstance[] {
  return [...instances].sort(
    (a, b) =>
      stateRank[instanceState(a)] - stateRank[instanceState(b)] ||
      displayName(a).localeCompare(displayName(b)) ||
      a.id.localeCompare(b.id),
  );
}

// needsYou counts the instances that need you, for the tab title.
export function needsYou(instances: Iterable<HubInstance>): number {
  let count = 0;
  for (const instance of instances) if (instanceState(instance) === "needs-you") count++;
  return count;
}

export type HubEvent =
  | {type: "instances"; instances: HubInstance[]}
  | {type: "instance"; instance: HubInstance}
  | {type: "removed"; id: string};

// applyHubEvent returns the instances by ID after event.
export function applyHubEvent(instances: ReadonlyMap<string, HubInstance>, event: HubEvent): Map<string, HubInstance> {
  switch (event.type) {
    case "instances":
      return new Map(event.instances.map((instance) => [instance.id, instance]));
    case "instance": {
      const next = new Map(instances);
      next.set(event.instance.id, event.instance);
      return next;
    }
    case "removed": {
      const next = new Map(instances);
      next.delete(event.id);
      return next;
    }
  }
}

// hubRoot is the hub's address: the chat is served below it at basePath.
export function hubRoot(basePath: string, origin: string): string {
  const chat = new URL(basePath.endsWith("/") ? basePath : `${basePath}/`, origin);
  return new URL("..", chat).toString().replace(/\/$/, "");
}

// chatURL opens the instance id in the chat, through the hub's proxy.
export function chatURL(basePath: string, root: string, id: string): string {
  return `${basePath}/embed?url=${encodeURIComponent(`${root}/i/${encodeURIComponent(id)}`)}`;
}

// usagePercent is the fullest of the account's usage limits, if known.
export function usagePercent(summary: Summary | null | undefined): number | undefined {
  const limits = summary?.usage?.rate_limits ?? [];
  let fullest: number | undefined;
  for (const limit of limits) {
    if (limit.used_percent !== undefined) fullest = Math.max(fullest ?? 0, limit.used_percent);
  }
  return fullest;
}
