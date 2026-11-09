// The hub's view of the AgentAPIs it watches (agentapi hub serve): each
// instance as GET /api/instances and the /api/events stream give it, its
// state and name, the stream's events, and the hub's addresses. What the
// list does with them is beside it: hub-groups, hub-machines (and
// hub-order, the list's order), hub-filter, hub-format (what a row says),
// hub-spend, hub-work and hub-version.

import type {components} from "./api-schema";
import {parseFavorites, type Favorite} from "./hub-favorites";
import type {components as hubSchema} from "./hub-schema";

// HubWire are the hub's wire types, generated from its schema
// (lib/hub/hub-schema.json, by make gen and bun run gen:api).
export type HubWire = hubSchema["schemas"];

// InstanceUpdate is the summary's update: whether the AgentAPI takes updates
// through its API (POST /update) and how far one got; AgentAPIs that can't
// update this way leave it out. Its state stays open, as a newer AgentAPI
// may add states.
export type InstanceUpdate = Omit<components["schemas"]["UpdateStatus"], "state"> & {state: string};

export type Summary = Omit<components["schemas"]["SummaryBody"], "update"> & {update?: InstanceUpdate};
export type HubConnection = HubWire["Connection"];

// InstanceState is what a row says of an instance, the most pressing
// first: it needs you, works, finished a task you haven't seen, waits,
// waits for what you muted, or can't be reached. The hub works it out
// (instanceapi.StateOf); unknown is one this page doesn't know, from a
// newer hub than the page a browser kept.
export type InstanceState = HubWire["State"] | "unknown";

// HubInstance is an instance as the hub sends it (Instance in its schema),
// with its summary in the AgentAPI's own types. Its state and transport
// stay open: a newer hub may send a state this page doesn't know
// (instanceState reads it), an AgentAPI 2 the transport "pty".
export type HubInstance = Omit<HubWire["Instance"], "summary" | "state" | "transport"> & {
  summary?: Summary;
  state: string;
  transport: string;
};

// hasRun says whether an instance has run: a task in its conversation, or
// work the hub saw this week.
export function hasRun(instance: HubInstance): boolean {
  const work = instance.work;
  return (instance.summary?.tasks ?? 0) > 0 || !!work?.working_since || (work?.last_7d_seconds ?? 0) > 0;
}

// RECENTLY_FINISHED is how long an idle agent that has run reads as just
// finished, with a green mark, before it greys like one idle for long.
export const RECENTLY_FINISHED = 30 * 60_000;

// finishedAt is when an idle agent that has run last changed, in ms: when
// it finished, seen or not; undefined for any other agent.
export function finishedAt(instance: HubInstance): number | undefined {
  if (instanceState(instance) !== "idle" || !hasRun(instance) || !instance.changed_at) return undefined;
  const at = Date.parse(instance.changed_at);
  return Number.isNaN(at) ? undefined : at;
}

// finishedLately says whether an agent that finished at at (finishedAt)
// did so less than RECENTLY_FINISHED before now.
export function finishedLately(at: number | undefined, now: number): boolean {
  return at !== undefined && now >= at && now - at < RECENTLY_FINISHED;
}

// HubWork is how long the hub saw an instance working, as of when it sent
// it: the seconds of the last 24 hours and 7 days from the spans that
// ended, and since when it works now. The page adds the time since then
// (workSeconds), so the hub doesn't send it every second.
export type HubWork = HubWire["WorkTime"];

// stateRank orders the states, the most pressing first.
export const stateRank: Readonly<Record<InstanceState, number>> = {
  "needs-you": 0,
  working: 1,
  unread: 2,
  idle: 3,
  muted: 4,
  connecting: 5,
  unreachable: 6,
  stopped: 7,
  unknown: 8,
};

export function instanceState(instance: HubInstance): InstanceState {
  const state = instance.state as InstanceState;
  return state !== "unknown" && Object.hasOwn(stateRank, state) ? state : "unknown";
}

const attentionLabel: Record<Summary["attention"], string> = {
  login: "Needs login",
  request: "Needs your answer",
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
    case "unknown":
      return "Unknown";
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

// HubEventName is an event of the hub's stream (/api/events); the page
// listens for these, not the heartbeat.
export type HubEventName = Exclude<keyof HubWire["HubEvents"], "heartbeat">;
export const hubEventNames = ["instances", "instance", "removed", "favorites", "dispatch"] as const satisfies readonly HubEventName[];

// HubEvent is an event of the stream as the page reads it. The first,
// instances, also has the hub's own version (hubVersion), which the page
// compares the instances' with; favorites has all of them, on every change
// (a hub before 3.5 sends none); dispatch, the issue dispatch's machines
// and jobs, whole, on every change.
export type HubEvent =
  | {type: "instances"; instances: HubInstance[]; hubVersion: string}
  | {type: "instance"; instance: HubInstance}
  | {type: "removed"; id: string}
  | {type: "favorites"; favorites: Favorite[]}
  | {type: "dispatch"; board: DispatchBoard};

// DispatchBoard is the issue dispatch's machines and jobs.
export type DispatchBoard = {jobs: HubWire["DispatchJob"][]; hosts: HubWire["DispatchHost"][]};

// objectsWith are the items of value that are objects with a string field
// key that isn't empty.
export function objectsWith<T>(value: unknown, key: string): T[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item) => !!item && typeof item === "object" && typeof item[key] === "string" && item[key] !== "") as T[];
}

// isInstance tells what the hub sent for an instance from anything else:
// an object with an ID.
function isInstance(value: unknown): value is HubInstance {
  return !!value && typeof value === "object" && typeof (value as {id?: unknown}).id === "string" && (value as {id: string}).id !== "";
}

// parseHubEvent reads the data of the stream's event type: undefined when
// it isn't what that event has (it doesn't parse, or misses its fields).
export function parseHubEvent(type: HubEventName, data: string): HubEvent | undefined {
  let parsed: unknown;
  try {
    parsed = JSON.parse(data);
  } catch {
    return undefined;
  }
  if (!parsed || typeof parsed !== "object") return undefined;
  switch (type) {
    case "instances": {
      const body = parsed as Partial<Record<keyof HubWire["InstanceList"], unknown>>;
      if (!Array.isArray(body.instances)) return undefined;
      return {type, instances: body.instances.filter(isInstance), hubVersion: typeof body.hub_version === "string" ? body.hub_version : ""};
    }
    case "instance":
      return isInstance(parsed) ? {type, instance: parsed} : undefined;
    case "removed": {
      const {id} = parsed as Partial<Record<keyof HubWire["RemovedEvent"], unknown>>;
      return typeof id === "string" && id ? {type, id} : undefined;
    }
    case "favorites": {
      const favorites = parseFavorites((parsed as Partial<Record<keyof HubWire["FavoritesBody"], unknown>>).favorites);
      return favorites ? {type, favorites} : undefined;
    }
    case "dispatch": {
      const body = parsed as Partial<Record<keyof HubWire["DispatchBoard"], unknown>>;
      return {type, board: {jobs: objectsWith(body.jobs, "key"), hosts: objectsWith(body.hosts, "hostname")}};
    }
  }
}

// applyHubEvent returns the instances by ID after event.
export function applyHubEvent(instances: ReadonlyMap<string, HubInstance>, event: Exclude<HubEvent, {type: "favorites" | "dispatch"}>): Map<string, HubInstance> {
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
