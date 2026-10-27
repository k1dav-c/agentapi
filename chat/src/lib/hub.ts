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
  // group is the group someone put it in on the hub's page; none when empty.
  group?: string;
  // muted is what it waited for when someone muted it (attentionKey); the
  // hub ends the mute once that changes.
  muted?: string;
  summary?: Summary | null;
}

// InstanceState is what a row says of an instance, the most pressing
// first: it needs you, works, finished a task you haven't seen, waits,
// waits for what you muted, or can't be reached.
export type InstanceState = "needs-you" | "working" | "unread" | "idle" | "muted" | "connecting" | "unreachable" | "stopped";

const stateRank: Record<InstanceState, number> = {
  "needs-you": 0,
  working: 1,
  unread: 2,
  idle: 3,
  muted: 4,
  connecting: 5,
  unreachable: 6,
  stopped: 7,
};

// attentionKey is what an instance waits for, as the hub mutes it: why it
// needs you and, for a request, which one; empty when it needs nothing.
export function attentionKey(summary: Summary | null | undefined): string {
  const attention = summary?.attention;
  if (!attention || attention === "none" || attention === "exited") return "";
  return summary.request ? `${attention}:${summary.request.id}` : attention;
}

export function instanceState(instance: HubInstance): InstanceState {
  if (instance.connection === "stopped") return "stopped";
  if (instance.connection === "unreachable") return "unreachable";
  const summary = instance.summary;
  if (!summary) return "connecting";
  if (summary.attention !== "none" && summary.attention !== "exited") {
    // Muted until what it waits for changes; the hub ends it then.
    return instance.muted && instance.muted === attentionKey(summary) ? "muted" : "needs-you";
  }
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
    case "muted":
      return `Muted · ${attentionLabel[instance.summary!.attention] || "Needs you"}`;
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

// chatURL opens the instance id in the chat, through the hub's proxy. In
// the hub's frame (hub=1) the chat draws the hub's bar for the instance
// instead of its status bar.
export function chatURL(basePath: string, root: string, id: string, framed = false): string {
  return `${basePath}/embed?url=${encodeURIComponent(`${root}/i/${encodeURIComponent(id)}`)}${framed ? "&hub=1" : ""}`;
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

// A group of rows: name is "" for the instances in no group.
export interface HubGroup {
  name: string;
  instances: HubInstance[];
  needsYou: number;
}

// groupInstances splits the rows into their groups, by name, with the
// instances in no group last; each group's rows in sortInstances order.
export function groupInstances(instances: Iterable<HubInstance>): HubGroup[] {
  const byName = new Map<string, HubInstance[]>();
  for (const instance of sortInstances(instances)) {
    const name = instance.group?.trim() ?? "";
    byName.set(name, [...(byName.get(name) ?? []), instance]);
  }
  return [...byName.entries()]
    .sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)))
    .map(([name, rows]) => ({name, instances: rows, needsYou: needsYou(rows)}));
}

// groupNames lists the groups in use, by name.
export function groupNames(instances: Iterable<HubInstance>): string[] {
  const names = new Set<string>();
  for (const instance of instances) if (instance.group?.trim()) names.add(instance.group.trim());
  return [...names].sort((a, b) => a.localeCompare(b));
}

// HubFilter narrows the list: everything, what needs you, what works, what
// finished unseen, or what can't be reached (unreachable or stopped).
export type HubFilter = "all" | "needs-you" | "working" | "unread" | "offline";

export function matchesFilter(instance: HubInstance, filter: HubFilter): boolean {
  const state = instanceState(instance);
  switch (filter) {
    case "all":
      return true;
    case "offline":
      return state === "unreachable" || state === "stopped";
    default:
      return state === filter;
  }
}

export function countFilters(instances: Iterable<HubInstance>): Record<HubFilter, number> {
  const counts: Record<HubFilter, number> = {all: 0, "needs-you": 0, working: 0, unread: 0, offline: 0};
  for (const instance of instances) {
    counts.all++;
    for (const filter of ["needs-you", "working", "unread", "offline"] as const) if (matchesFilter(instance, filter)) counts[filter]++;
  }
  return counts;
}

// matchesSearch finds an instance by any word of its name, ID, group,
// title, machine or agent.
export function matchesSearch(instance: HubInstance, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const haystack = [instance.name, instance.id, instance.group, instance.summary?.title, instance.hostname, instance.agent_type]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  return words.every((word) => haystack.includes(word));
}

const requestLabels: Record<string, string> = {approval: "Approval", question: "Question", plan: "Plan"};

// Activity is the line under a row's name: what the instance does or waits
// for, and the tone to show it in.
export interface Activity {
  // label names what it is, when that adds to the text: "Approval",
  // "Working", "Finished".
  label?: string;
  text: string;
  tone: "needs" | "working" | "ready" | "fault" | "muted";
  // since is when it began: the request, the task or its end.
  since?: string;
}

// waitsFor is what an instance that needs you waits for.
function waitsFor(summary: Summary | null | undefined): Activity {
  if (summary?.attention === "login") return {label: "Login", text: summary.login_required?.message || "Log in to continue", tone: "needs"};
  if (summary?.attention === "limit") return {label: "Usage limit", text: summary.limit_stop?.message || "The account reached its limit", tone: "needs", since: summary.limit_stop?.stopped_at};
  if (summary?.attention === "dialog") return {label: "Dialog", text: "Answer it in the agent's terminal", tone: "needs"};
  const request = summary?.request;
  if (request) return {label: requestLabels[request.kind] ?? "Request", text: request.title, tone: "needs", since: request.created_at};
  return {label: "Needs you", text: "Waiting for your answer", tone: "needs"};
}

export function activity(instance: HubInstance): Activity {
  const summary = instance.summary;
  const task = summary?.task;
  switch (instanceState(instance)) {
    case "needs-you":
      return waitsFor(summary);
    case "muted":
      // What it waits for, out of the way.
      return {...waitsFor(summary), label: "Muted", tone: "muted"};
    case "working":
      return {label: "Working", text: task?.prompt || summary?.title || "On a task", tone: "working", since: task?.started_at};
    case "unread":
      return {label: "Finished", text: summary?.title || task?.prompt || "A task", tone: "ready", since: task?.ended_at};
    case "unreachable":
      return {label: "Unreachable", text: shortError(instance.error) || "The hub can't reach it", tone: "fault", since: instance.last_seen};
    case "stopped":
      return {label: summary?.attention === "exited" ? "Exited" : "Stopped", text: summary?.title || "Not running", tone: "muted", since: instance.last_seen};
    case "connecting":
      return {text: "Connecting…", tone: "muted"};
    case "idle":
      return {text: summary?.title || "No task yet", tone: "muted", since: task?.ended_at};
  }
}

// shortError keeps what a person needs of a connection error: Go's chain
// "unreachable: Get "…": dial tcp …: connect: connection refused" ends in
// the part that says what happened.
export function shortError(error: string | undefined): string {
  if (!error) return "";
  if (!error.startsWith("unreachable:")) return error;
  const last = error.slice(error.lastIndexOf(": ") + 2).trim();
  return last ? last[0].toUpperCase() + last.slice(1) : error;
}

// formatAgo is a short age: "now", "45s", "12m", "3h", "2d".
export function formatAgo(since: string | undefined, now: number): string {
  if (!since) return "";
  const seconds = Math.max(0, Math.round((now - Date.parse(since)) / 1000));
  if (Number.isNaN(seconds)) return "";
  if (seconds < 10) return "now";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

// formatElapsed is a running duration: "45s", "2m 14s", "1h 05m".
export function formatElapsed(since: string | undefined, now: number): string {
  if (!since) return "";
  const seconds = Math.max(0, Math.floor((now - Date.parse(since)) / 1000));
  if (Number.isNaN(seconds)) return "";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s`;
  return `${Math.floor(seconds / 3600)}h ${String(Math.floor((seconds % 3600) / 60)).padStart(2, "0")}m`;
}
