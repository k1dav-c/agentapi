// The list's filters: by state, by agent and by a search.

import {instanceState, type HubInstance} from "./hub";
import {instanceVersion} from "./hub-version";

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

// stoppedOf are the instances that stopped, which Forget all stopped
// forgets; not those unreachable, which may come back by themselves.
export function stoppedOf(instances: Iterable<HubInstance>): HubInstance[] {
  return [...instances].filter((instance) => instanceState(instance) === "stopped");
}

// stoppedShown are the stopped ones of those the list shows, which the
// Offline filter offers to forget; none under another filter.
export function stoppedShown(filter: HubFilter, shown: Iterable<HubInstance>): HubInstance[] {
  return filter === "offline" ? stoppedOf(shown) : [];
}

// forgotText is the toast once the hub forgot forgot of the asked stopped
// agents: all, some (the others started again), or none.
export function forgotText(asked: number, forgot: number): {ok: boolean; title: string; description?: string} {
  const agents = (n: number) => `${n} ${n === 1 ? "agent" : "agents"}`;
  if (forgot === asked) return {ok: true, title: `Forgot ${agents(forgot)}`};
  const left = asked - forgot;
  return {
    ok: forgot > 0,
    title: forgot > 0 ? `Forgot ${forgot} of ${agents(asked)}` : `Couldn't forget ${agents(asked)}`,
    description: `${agents(left)} ${left === 1 ? "isn't" : "aren't"} stopped now, so the hub kept ${left === 1 ? "it" : "them"}.`,
  };
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
