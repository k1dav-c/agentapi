// The machines the instances run on, by host name: the list's cards (By
// machine), their tabs, the start most machines' names share, and what
// each machine's instances spent and worked.

import {displayName, instanceState, sortInstances, stateRank, type HubInstance} from "./hub";
import {agentOf, hubAgents} from "./hub-filter";
import {addUp, type FigureRow, type GroupSubtotal} from "./hub-groups";
import {instanceVersion} from "./hub-version";

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

// machineKey tells a machine from the others: its host name, or, for an
// instance that names none, the instance, a machine of its own.
function machineKey(instance: HubInstance): string {
  const hostname = machineOf(instance);
  return hostname ? `host ${hostname}` : `instance ${instance.id}`;
}

// machinesInOrder puts the instances on the same machine together, in the
// order of each machine's first one in rows, its own in their order there.
// An instance that names no machine is one of its own.
export function machinesInOrder(rows: Iterable<HubInstance>): HubMachine[] {
  const byKey = new Map<string, HubMachine>();
  for (const instance of rows) {
    const key = machineKey(instance);
    const machine = byKey.get(key) ?? {key, hostname: machineOf(instance), instances: []};
    machine.instances.push(instance);
    byKey.set(key, machine);
  }
  return [...byKey.values()];
}

// groupByMachine puts the instances on the same machine together, each
// machine's in sortInstances order: the machine whose agent needs you the
// most first, ties by name. An instance that names no machine is one of its
// own.
export function groupByMachine(instances: Iterable<HubInstance>): HubMachine[] {
  const rank = (machine: HubMachine) => stateRank[instanceState(machine.instances[0])];
  const name = (machine: HubMachine) => machine.hostname || displayName(machine.instances[0]);
  return machinesInOrder(sortInstances(instances)).sort((a, b) => rank(a) - rank(b) || name(a).localeCompare(name(b)) || a.key.localeCompare(b.key));
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
export function machineSubtotals(rows: Iterable<FigureRow>, limit = 8): MachineSubtotal[] {
  const byName: Map<string, MachineSubtotal> = addUp(rows, machineOf);
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
