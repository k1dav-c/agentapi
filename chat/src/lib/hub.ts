// The hub's view of the AgentAPIs it watches (agentapi hub serve): each
// instance as GET /api/instances and the /api/events stream give it, how a
// row shows it, and in which order.

import type {components} from "./api-schema";
import {secondsBetween} from "./clock";

// InstanceUpdate is the summary's update: whether the AgentAPI takes updates
// through its API (POST /update) and how far one got; AgentAPIs that can't
// update this way leave it out. Its state stays open, as a newer AgentAPI
// may add states.
export type InstanceUpdate = Omit<components["schemas"]["UpdateStatus"], "state"> & {state: string};

export type Summary = Omit<components["schemas"]["SummaryBody"], "update"> & {update?: InstanceUpdate};
export type HubConnection = "connecting" | "connected" | "unreachable" | "stopped";

// InstanceState is what a row says of an instance, the most pressing
// first: it needs you, works, finished a task you haven't seen, waits,
// waits for what you muted, or can't be reached. The hub works it out
// (instanceapi.StateOf); unknown is one this page doesn't know, from a
// newer hub than the page a browser kept.
export type InstanceState = "needs-you" | "working" | "unread" | "idle" | "muted" | "connecting" | "unreachable" | "stopped" | "unknown";

export interface HubInstance {
  id: string;
  name?: string;
  url: string;
  server_id?: string;
  hostname?: string;
  port?: number;
  pid?: number;
  cwd?: string;
  agent_type?: string;
  transport?: string;
  agentapi_version?: string;
  workspace_url?: string;
  source: "webhook" | "manual";
  last_seen: string;
  // changed_at is when the hub last saw it change what its row shows it
  // does, or first saw it; none from a hub that doesn't record it, or for
  // one it kept before it did, until its next change.
  changed_at?: string;
  connection: HubConnection;
  error?: string;
  // group is the group someone put it in on the hub's page; none when empty.
  group?: string;
  // muted is what it waited for when someone muted it; the hub ends the
  // mute once that changes.
  muted?: string;
  // pin is its place among the instances pinned to the top of the list,
  // from 1; none when it isn't pinned. The hub keeps them, numbered without
  // gaps.
  pin?: number;
  summary?: Summary | null;
  // state is as the hub sends it; instanceState reads it.
  state: string;
  // work is how long the hub saw it working; none from an older hub.
  work?: HubWork;
}

// hasRun says whether an instance has run: a task in its conversation, or
// work the hub saw this week.
export function hasRun(instance: HubInstance): boolean {
  const work = instance.work;
  return (instance.summary?.tasks ?? 0) > 0 || !!work?.working_since || (work?.last_7d_seconds ?? 0) > 0;
}

// HubWork is how long the hub saw an instance working, as of when it sent
// it: the seconds of the last 24 hours and 7 days from the spans that
// ended, and since when it works now. The page adds the time since then
// (workSeconds), so the hub doesn't send it every second.
export interface HubWork {
  last_24h_seconds: number;
  last_7d_seconds: number;
  working_since?: string;
}

const stateRank: Record<InstanceState, number> = {
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

// The first event, instances, also has the hub's own version (hubVersion),
// which the page compares the instances' with.
export type HubEvent =
  | {type: "instances"; instances: HubInstance[]; hubVersion?: string}
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

// groupOf is the group an instance is in, "" for none.
export function groupOf(instance: HubInstance): string {
  return instance.group?.trim() ?? "";
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
    const name = groupOf(instance);
    byName.set(name, [...(byName.get(name) ?? []), instance]);
  }
  return [...byName.entries()]
    .sort(([a], [b]) => groupOrder(a, b))
    .map(([name, rows]) => ({name, instances: rows, needsYou: needsYou(rows)}));
}

// groupOrder orders group names by name, with "" (no group) last.
function groupOrder(a: string, b: string): number {
  return a === "" ? 1 : b === "" ? -1 : a.localeCompare(b);
}

// A group's figures in the last 24 hours and 7 days: name is "" for the
// instances in no group.
export interface GroupSubtotal {
  name: string;
  day: number;
  week: number;
}

// groupSubtotals adds up each instance's figures by its group, in
// groupInstances order, leaving out the groups with none this week. It is
// empty while no instance is in a group: the subtotal would be the total.
export function groupSubtotals(rows: Iterable<{instance: HubInstance; day: number; week: number}>): GroupSubtotal[] {
  const byName = new Map<string, GroupSubtotal>();
  for (const {instance, day, week} of rows) {
    const name = groupOf(instance);
    const group = byName.get(name) ?? {name, day: 0, week: 0};
    group.day += day;
    group.week += week;
    byName.set(name, group);
  }
  if (byName.size === 1 && byName.has("")) return [];
  return [...byName.values()].filter((group) => group.week > 0).sort((a, b) => groupOrder(a.name, b.name));
}

// machineOf is the machine an instance runs on, its host name; "" when it
// doesn't say.
export function machineOf(instance: HubInstance): string {
  return instance.hostname?.trim() ?? "";
}

// hostPrefix is the start that at least half of the machines' names share
// (two at least), up to a "-" or ".", which the list dims so the rest tells
// them apart: "coder-dave-" of coder-dave-api and coder-dave-web. It is ""
// when they share none, and a name that is the start alone keeps it whole.
export function hostPrefix(hostnames: Iterable<string>): string {
  const names = [...new Set(hostnames)].filter(Boolean);
  if (names.length < 2) return "";
  const counts = new Map<string, number>();
  for (const name of names) {
    for (const {index} of name.matchAll(/[-.]/g)) {
      // Something must be left after it.
      if (index === name.length - 1) continue;
      const start = name.slice(0, index + 1);
      counts.set(start, (counts.get(start) ?? 0) + 1);
    }
  }
  let best = "";
  for (const [start, count] of counts) {
    if (count >= 2 && count * 2 >= names.length && start.length > best.length) best = start;
  }
  return best;
}

// A machine's agents in the list: hostname is "" for an instance that
// doesn't name its machine, which is one of its own (key tells them apart).
export interface HubMachine {
  key: string;
  hostname: string;
  instances: HubInstance[];
}

// groupByMachine puts the instances on the same machine together, each
// machine's in sortInstances order: the machine whose agent needs you the
// most first, ties by name. An instance that names no machine is one of its
// own.
export function groupByMachine(instances: Iterable<HubInstance>): HubMachine[] {
  const byKey = new Map<string, HubMachine>();
  for (const instance of sortInstances(instances)) {
    const hostname = machineOf(instance);
    const key = hostname ? `host ${hostname}` : `instance ${instance.id}`;
    const machine = byKey.get(key) ?? {key, hostname, instances: []};
    machine.instances.push(instance);
    byKey.set(key, machine);
  }
  const rank = (machine: HubMachine) => stateRank[instanceState(machine.instances[0])];
  const name = (machine: HubMachine) => machine.hostname || displayName(machine.instances[0]);
  return [...byKey.values()].sort((a, b) => rank(a) - rank(b) || name(a).localeCompare(name(b)) || a.key.localeCompare(b.key));
}

// machinesInOrder puts the instances on the same machine together, in the
// order of each machine's first one in rows, its own in their order there.
// An instance that names no machine is one of its own.
export function machinesInOrder(rows: readonly HubInstance[]): HubMachine[] {
  const byKey = new Map<string, HubMachine>();
  for (const instance of rows) {
    const hostname = machineOf(instance);
    const key = hostname ? `host ${hostname}` : `instance ${instance.id}`;
    const machine = byKey.get(key) ?? {key, hostname, instances: []};
    machine.instances.push(instance);
    byKey.set(key, machine);
  }
  return [...byKey.values()];
}

// tabOrder orders a machine's agents as its tabs show them: by agent, in
// the filter's order, then by name; unlike sortInstances, it doesn't move
// as their states change.
export function tabOrder(instances: Iterable<HubInstance>): HubInstance[] {
  return [...instances].sort(
    (a, b) =>
      hubAgents.indexOf(agentOf(a)) - hubAgents.indexOf(agentOf(b)) ||
      (a.agent_type ?? "").localeCompare(b.agent_type ?? "") ||
      displayName(a).localeCompare(displayName(b)) ||
      a.id.localeCompare(b.id),
  );
}

// sameMachine is the agents on instance's machine, itself among them, in
// tabOrder; none when it doesn't name its machine or is the only one there.
export function sameMachine(instances: Iterable<HubInstance>, instance: HubInstance): HubInstance[] {
  const machine = machineOf(instance);
  if (!machine) return [];
  const agents = [...instances].filter((other) => machineOf(other) === machine);
  return agents.length > 1 ? tabOrder(agents) : [];
}

// activeAgent is the agent a machine's card shows: the one picked on it,
// else the one open beside the list, else the one that needs you the most
// (the first); picked and selected count only while the card has them.
export function activeAgent(machine: HubMachine, picked?: string, selected?: string): string {
  const has = (id: string | undefined) => !!id && machine.instances.some((instance) => instance.id === id);
  if (has(picked)) return picked!;
  if (has(selected)) return selected!;
  return machine.instances[0].id;
}

// A machine's figures in the last 24 hours and 7 days: name is "" for the
// instances that don't name theirs; others, when set, is the rest of the
// machines, added up.
export interface MachineSubtotal extends GroupSubtotal {
  others?: number;
}

// machineSubtotals adds up each instance's figures by its machine, the most
// this week first, leaving out the machines with none this week; past the
// first limit, the rest add up in one, others. It is empty while the
// instances run on one machine: the subtotal would be the total.
export function machineSubtotals(rows: Iterable<{instance: HubInstance; day: number; week: number}>, limit = 8): MachineSubtotal[] {
  const byName = new Map<string, MachineSubtotal>();
  for (const {instance, day, week} of rows) {
    const name = machineOf(instance);
    const machine = byName.get(name) ?? {name, day: 0, week: 0};
    machine.day += day;
    machine.week += week;
    byName.set(name, machine);
  }
  if (byName.size <= 1) return [];
  const machines = [...byName.values()]
    .filter((machine) => machine.week > 0)
    .sort((a, b) => b.week - a.week || Number(!a.name) - Number(!b.name) || a.name.localeCompare(b.name));
  if (machines.length <= limit) return machines;
  const shown = machines.slice(0, limit - 1);
  const rest = machines.slice(limit - 1);
  return [...shown, {name: "", day: rest.reduce((sum, m) => sum + m.day, 0), week: rest.reduce((sum, m) => sum + m.week, 0), others: rest.length}];
}

// sharedVersion is the AgentAPI version all of a machine's agents run, ""
// when they don't all say the same one.
export function sharedVersion(instances: readonly HubInstance[]): string {
  const versions = new Set(instances.map(instanceVersion));
  return versions.size === 1 ? [...versions][0] : "";
}

// groupNames lists the groups in use, by name.
export function groupNames(instances: Iterable<HubInstance>): string[] {
  const names = new Set<string>();
  for (const instance of instances) {
    const name = groupOf(instance);
    if (name) names.add(name);
  }
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

// HubAgent is the agent an instance runs, as the list filters it: Claude
// Code, Codex, Pi, or other (an agent an AgentAPI 2 ran, e.g. aider,
// native-demo, or none reported).
export type HubAgent = "claude" | "codex" | "pi" | "other";

// hubAgents are the agents in the order the filter shows them.
export const hubAgents: readonly HubAgent[] = ["claude", "codex", "pi", "other"];

export function agentOf(instance: HubInstance): HubAgent {
  const type = instance.agent_type;
  return type === "claude" || type === "codex" || type === "pi" ? type : "other";
}

// matchesAgents tells whether the instance runs one of the agents; none
// picked is every agent.
export function matchesAgents(instance: HubInstance, agents: ReadonlySet<HubAgent>): boolean {
  return agents.size === 0 || agents.has(agentOf(instance));
}

// agentsIn lists the agents the instances run, in the filter's order.
export function agentsIn(instances: Iterable<HubInstance>): HubAgent[] {
  const found = new Set<HubAgent>();
  for (const instance of instances) found.add(agentOf(instance));
  return hubAgents.filter((agent) => found.has(agent));
}

// shownAgents is the part of the picked agents the list filters by: those
// the instances run, and none while they run one agent or none, when the
// page shows no agent filter.
export function shownAgents(picked: ReadonlySet<HubAgent>, present: readonly HubAgent[]): Set<HubAgent> {
  if (present.length < 2) return new Set();
  return new Set(present.filter((agent) => picked.has(agent)));
}

// parseAgents reads the picked agents the browser kept: the known ones, once
// each; anything else is none picked.
export function parseAgents(value: unknown): Set<HubAgent> {
  if (!Array.isArray(value)) return new Set();
  return new Set(hubAgents.filter((agent) => value.includes(agent)));
}

const agentNames: Record<HubAgent, string> = {claude: "Claude Code", codex: "Codex", pi: "Pi", other: "other"};

// agentLabel names an agent on its chip.
export function agentLabel(agent: HubAgent): string {
  return agent === "other" ? "Other" : agentNames[agent];
}

// nothingMatches says why the list is empty: the search, the agents picked
// and the filter.
export function nothingMatches(query: string, filter: HubFilter, agents: ReadonlySet<HubAgent>): string {
  const search = query.trim();
  if (search) return `No agent matches “${search}”${filter !== "all" || agents.size > 0 ? " with these filters" : ""}`;
  const names = hubAgents.filter((agent) => agents.has(agent)).map((agent) => agentNames[agent]);
  const which = names.length === 0 ? "agent" : `${names.length > 1 ? `${names.slice(0, -1).join(", ")} or ${names.at(-1)}` : names[0]} agent`;
  return `No ${which} ${filter === "all" ? "is here" : "is in this state"} right now`;
}

// toggleAgent picks the agent, or takes it out when it's picked.
export function toggleAgent(picked: ReadonlySet<HubAgent>, agent: HubAgent): Set<HubAgent> {
  const next = new Set(picked);
  if (!next.delete(agent)) next.add(agent);
  return next;
}

// countAgents counts the instances that pass the filter, by agent.
export function countAgents(instances: Iterable<HubInstance>, filter: HubFilter): Record<HubAgent, number> {
  const counts: Record<HubAgent, number> = {claude: 0, codex: 0, pi: 0, other: 0};
  for (const instance of instances) if (matchesFilter(instance, filter)) counts[agentOf(instance)]++;
  return counts;
}

// matchesSearch finds an instance by any word of its name, ID, group,
// title, machine, agent or version.
export function matchesSearch(instance: HubInstance, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const version = instanceVersion(instance);
  const haystack = [instance.name, instance.id, instance.group, instance.summary?.title, instance.hostname, instance.agent_type, version && `v${version}`]
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
      if (summary?.retry_wait) {
        // A failed turn AgentAPI tries again by itself.
        const wait = summary.retry_wait;
        const failed = wait.api_status ? `The API returned ${wait.api_status}` : "The API request failed";
        return {label: "Retrying", text: `${failed} · try ${wait.attempt} of ${wait.attempts}`, tone: "working", since: wait.stopped_at};
      }
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
    case "unknown":
      return {text: "Reload the page to see what it does", tone: "muted"};
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

export type Spend = NonNullable<Summary["spend"]>;

// currentSpend is what an instance the hub follows spent recently; none for
// one it doesn't (stopped, unreachable, connecting), whose summary is the
// last it had.
export function currentSpend(instance: HubInstance): Spend | undefined {
  return instance.connection === "connected" ? (instance.summary?.spend ?? undefined) : undefined;
}

// countedSpends are the spends that add up to what the instances spent:
// AgentAPIs of one agent in one directory of a machine report the same
// sessions, with one scope, which counts once, with its most recent figure.
// An older AgentAPI reports no scope: it counts on its own.
export function countedSpends(instances: Iterable<HubInstance>): {instance: HubInstance; spend: Spend}[] {
  const scopes = new Map<string, {instance: HubInstance; spend: Spend}>();
  for (const instance of instances) {
    const spend = currentSpend(instance);
    if (!spend) continue;
    const key = spend.scope ? `scope ${spend.scope}` : `instance ${instance.id}`;
    const counted = scopes.get(key);
    if (!counted || updatedAt(spend) > updatedAt(counted.spend)) scopes.set(key, {instance, spend});
  }
  return [...scopes.values()];
}

// updatedAt is when a spend's amounts last changed; 0 when it doesn't say.
function updatedAt(spend: Spend): number {
  const at = Date.parse(spend.updated_at);
  return Number.isNaN(at) ? 0 : at;
}

// What the instances spent recently, as their summaries report it: the
// API-equivalent cost of their sessions, at list prices, each scope once.
export interface SpendTotals {
  day: number;
  week: number;
  // How many connected instances report a spend; the others (older
  // AgentAPIs, those the hub doesn't follow now) don't count.
  counted: number;
}

export function spendTotals(instances: Iterable<HubInstance>): SpendTotals {
  const all = [...instances];
  const totals: SpendTotals = {day: 0, week: 0, counted: all.filter((instance) => currentSpend(instance)).length};
  for (const {spend} of countedSpends(all)) {
    totals.day += spend.day_usd;
    totals.week += spend.week_usd;
  }
  return totals;
}

// formatUSD is a short amount: "$0", "$4.20", "$110", "$1.2k".
export function formatUSD(usd: number): string {
  if (usd < 0.005) return "$0";
  if (usd < 10) return `$${usd.toFixed(2)}`;
  if (usd < 1000) return `$${Math.round(usd)}`;
  if (usd < 999_500) return `$${(usd / 1000).toFixed(usd < 9_950 ? 1 : 0)}k`;
  return `$${(usd / 1_000_000).toFixed(1)}M`;
}

// spendTitle says what a spend is, for a tooltip; lastKnown, that it is an
// instance's last, which doesn't count while the hub doesn't follow it;
// dayTokens, the tokens of its last 24 hours, where it says them.
export function spendTitle(day: number, week: number, lastKnown = false, dayTokens?: number): string {
  let amounts = `${formatUSD(day)} in the last 24 hours, ${formatUSD(week)} in the last 7 days (API-equivalent, at list prices; a subscription doesn't charge it)`;
  if (dayTokens !== undefined) amounts += `; ${formatTokens(dayTokens)} tokens in the last 24 hours`;
  return lastKnown ? `Last known, not counted: ${amounts}` : amounts;
}

// The tokens of the calls a spend adds up, as AgentAPIs since this release
// report them: the same calls, each once, models without a price included.

export type Tokens = NonNullable<Spend["day_tokens"]>;

// The kinds of tokens, in the order the page lists them.
export const tokenKinds: {key: Exclude<keyof Tokens, "total">; label: string}[] = [
  {key: "input", label: "Input"},
  {key: "output", label: "Output"},
  {key: "cache_write", label: "Cache write"},
  {key: "cache_read", label: "Cache read"},
];

const noTokens = (): Tokens => ({input: 0, output: 0, cache_write: 0, cache_read: 0, total: 0});

function addTokens(sum: Tokens, tokens: Tokens): void {
  sum.input += tokens.input;
  sum.output += tokens.output;
  sum.cache_write += tokens.cache_write;
  sum.cache_read += tokens.cache_read;
  sum.total += tokens.total;
}

// countedTokens are the spends whose tokens add up to what the instances
// used: those that say their tokens, each scope once with its most recent
// figure, as countedSpends counts them.
export function countedTokens(instances: Iterable<HubInstance>): {instance: HubInstance; spend: Spend; day: Tokens; week: Tokens}[] {
  const reporting = [...instances].filter((instance) => currentSpend(instance)?.week_tokens);
  return countedSpends(reporting).map(({instance, spend}) => ({instance, spend, day: spend.day_tokens ?? noTokens(), week: spend.week_tokens ?? noTokens()}));
}

// What the instances used recently, each kind and in all.
export interface TokenTotals {
  day: Tokens;
  week: Tokens;
  // How many connected instances report their tokens; the others (earlier
  // AgentAPIs, those the hub doesn't follow now) don't count.
  counted: number;
}

export function tokenTotals(instances: Iterable<HubInstance>): TokenTotals {
  const all = [...instances];
  const totals: TokenTotals = {day: noTokens(), week: noTokens(), counted: all.filter((instance) => currentSpend(instance)?.week_tokens).length};
  for (const {day, week} of countedTokens(all)) {
    addTokens(totals.day, day);
    addTokens(totals.week, week);
  }
  return totals;
}

// formatTokens is a short count: "950", "1.2k", "12k", "3.4M", "1.2B".
export function formatTokens(tokens: number): string {
  const n = Math.max(0, Math.round(tokens));
  if (n < 1000) return String(n);
  const units: [number, string][] = [
    [1e3, "k"],
    [1e6, "M"],
    [1e9, "B"],
    [1e12, "T"],
  ];
  for (const [i, [unit, suffix]] of units.entries()) {
    const scaled = n / unit;
    if (scaled < 999.5 || i === units.length - 1) return `${scaled.toFixed(scaled < 9.95 ? 1 : 0).replace(/\.0$/, "")}${suffix}`;
  }
  return String(n);
}

// tokensTitle says what a count of tokens is, for a tooltip.
export function tokensTitle(day: number, week: number): string {
  return `${formatTokens(day)} tokens in the last 24 hours, ${formatTokens(week)} in the last 7 days`;
}

// The work time: how long the hub saw the instances working (the state
// working, while it followed them), in the last 24 hours and 7 days. The
// hub counts only what it saw: not while it was down, or couldn't reach an
// instance.

const DAY_MS = 24 * 3600 * 1000;
const WEEK_MS = 7 * DAY_MS;

export interface WorkSeconds {
  day: number;
  week: number;
}

// workSeconds is an instance's work time at now: what the hub sent, and the
// span going on, within each window.
export function workSeconds(instance: HubInstance, now: number): WorkSeconds {
  const work = instance.work;
  if (!work) return {day: 0, week: 0};
  const seconds = {day: work.last_24h_seconds, week: work.last_7d_seconds};
  const since = work.working_since ? Date.parse(work.working_since) : NaN;
  if (!Number.isNaN(since)) {
    seconds.day += secondsBetween(Math.max(since, now - DAY_MS), now);
    seconds.week += secondsBetween(Math.max(since, now - WEEK_MS), now);
  }
  return seconds;
}

// What the instances worked: each one's own time, so all of them add up.
export interface WorkTotals extends WorkSeconds {
  // How many worked in the last 7 days.
  counted: number;
}

export function workTotals(instances: Iterable<HubInstance>, now: number): WorkTotals {
  const totals: WorkTotals = {day: 0, week: 0, counted: 0};
  for (const instance of instances) {
    const seconds = workSeconds(instance, now);
    totals.day += seconds.day;
    totals.week += seconds.week;
    if (seconds.week > 0) totals.counted++;
  }
  return totals;
}

// formatWork is a short duration: "0m", "<1m", "45m", "2.3h", "31h".
// Rounded down, so a figure grows as it runs.
export function formatWork(seconds: number): string {
  if (seconds <= 0) return "0m";
  if (seconds < 60) return "<1m";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 36_000) return `${Math.floor(seconds / 360) / 10}h`;
  return `${Math.floor(seconds / 3600)}h`;
}

// workTitle says what a work time is, for a tooltip.
export function workTitle(day: number, week: number): string {
  return `Worked ${formatWork(day)} in the last 24 hours, ${formatWork(week)} in the last 7 days (as the hub saw it)`;
}

// The AgentAPI versions: the hub's own and each instance's, to tell the
// instances that run an older release than the hub, whose machines need
// an update.

// parseVersion reads a release's version: major, minor and patch, with or
// without a leading v. A development build (0.0.0-dev, or
// 2.0.0-21-g61be34f from a commit after a release) and anything else read
// as none, and compare with nothing.
export function parseVersion(version: string | undefined): [number, number, number] | undefined {
  const match = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(version?.trim() ?? "");
  if (!match) return undefined;
  const parts: [number, number, number] = [Number(match[1]), Number(match[2]), Number(match[3])];
  return parts.some((part) => part > 0) ? parts : undefined;
}

// compareVersions is below 0 when release a is older than release b, above
// 0 when it is newer and 0 when they are the same; undefined when either
// isn't a release.
export function compareVersions(a: string | undefined, b: string | undefined): number | undefined {
  const x = parseVersion(a);
  const y = parseVersion(b);
  if (!x || !y) return undefined;
  return x[0] - y[0] || x[1] - y[1] || x[2] - y[2];
}

// currentSummary reports whether an instance's summary is of the run the
// hub knows: one that restarted keeps its last summary until the new run's
// first arrives, and the new run's event already named the new run.
export function currentSummary(instance: HubInstance): boolean {
  const reported = instance.summary?.instance;
  return !instance.server_id || !reported?.server_id || reported.server_id === instance.server_id;
}

// instanceVersion is the AgentAPI version an instance runs, "" when it
// doesn't say. Its summary's is the instance's own latest word, unless the
// summary is of an earlier run, whose version the new run's event replaced.
export function instanceVersion(instance: HubInstance): string {
  return ((currentSummary(instance) && instance.summary?.instance?.agentapi_version) || instance.agentapi_version || "").trim();
}

// VersionRelation is how a version compares with the hub's: unknown when
// either is a development build, or the instance doesn't say.
export type VersionRelation = "older" | "same" | "newer" | "unknown";

export function versionRelation(version: string | undefined, hubVersion: string | undefined): VersionRelation {
  const order = compareVersions(version, hubVersion);
  return order === undefined ? "unknown" : order < 0 ? "older" : order > 0 ? "newer" : "same";
}

// olderThanHub reports whether an instance the hub follows runs an older
// release than the hub. One it doesn't follow now (stopped, unreachable,
// connecting) shows the version it ran last, which says nothing of the one
// it runs next.
export function olderThanHub(instance: HubInstance, hubVersion: string | undefined): boolean {
  return instance.connection === "connected" && versionRelation(instanceVersion(instance), hubVersion) === "older";
}

// versionTitle says what an instance's version is, for a tooltip;
// lastKnown, that it is the one an instance the hub doesn't follow now ran
// last.
export function versionTitle(version: string, hubVersion: string | undefined, lastKnown = false): string {
  const name = `AgentAPI v${version}`;
  if (lastKnown) return `Last known: ${name}`;
  switch (versionRelation(version, hubVersion)) {
    case "older":
      return `${name}, older than the hub (v${hubVersion})`;
    case "newer":
      return `${name}, newer than the hub (v${hubVersion})`;
    case "same":
      return `${name}, as the hub`;
    case "unknown":
      return parseVersion(version) ? name : `${name}, a development build`;
  }
}

// A version the instances run, as the overview counts them.
export interface VersionCount {
  // version is as the instances say it; "" for those that don't.
  version: string;
  relation: VersionRelation;
  instances: number;
  // machines are the hosts that run it, by name.
  machines: string[];
}

export interface VersionTotals {
  // versions has the newest release first, then the development builds,
  // then the instances that don't say.
  versions: VersionCount[];
  // older counts the instances that run an older release than the hub.
  older: number;
  // counted is how many instances count: those the hub follows now.
  counted: number;
}

export function versionTotals(instances: Iterable<HubInstance>, hubVersion: string | undefined): VersionTotals {
  const byVersion = new Map<string, {instances: number; machines: Set<string>}>();
  let counted = 0;
  for (const instance of instances) {
    if (instance.connection !== "connected") continue;
    counted++;
    const version = instanceVersion(instance);
    const entry = byVersion.get(version) ?? {instances: 0, machines: new Set<string>()};
    entry.instances++;
    if (instance.hostname) entry.machines.add(instance.hostname);
    byVersion.set(version, entry);
  }
  const versions: VersionCount[] = [...byVersion].map(([version, entry]) => ({
    version,
    relation: versionRelation(version, hubVersion),
    instances: entry.instances,
    machines: [...entry.machines].sort((a, b) => a.localeCompare(b)),
  }));
  versions.sort((a, b) => newestFirst(a.version, b.version));
  const older = versions.reduce((sum, count) => sum + (count.relation === "older" ? count.instances : 0), 0);
  return {versions, older, counted};
}

// newestFirst orders versions: releases from the newest, then the others
// by name, then none.
function newestFirst(a: string, b: string): number {
  const order = compareVersions(b, a);
  if (order !== undefined) return order;
  const release = Number(!!parseVersion(b)) - Number(!!parseVersion(a));
  return release || Number(!a) - Number(!b) || a.localeCompare(b);
}
