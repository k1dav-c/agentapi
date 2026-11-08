// The hub list's two orders: in its groups, what needs you first in each
// (groupInstances), or one list by the latest change, the most recent
// first, whatever the group. By machine, in either order, puts each
// machine's agents together in one card. Each browser keeps its choices.
// The instances pinned on the hub come first, in their own order.

import {displayName, instanceState, needsYou, type HubInstance} from "./hub";
import {matchesAgents, matchesFilter, matchesSearch, type HubAgent, type HubFilter} from "./hub-filter";
import {activity} from "./hub-format";
import {groupInstances, groupNames, type HubGroup} from "./hub-groups";
import {groupByMachine, machinesInOrder, type HubMachine} from "./hub-machines";

export type ListOrder = "groups" | "recent";

// parseListOrder reads the order a browser stored: anything but "recent" is
// the groups, the default.
export function parseListOrder(stored: string | null | undefined): ListOrder {
  return stored === "recent" ? "recent" : "groups";
}

// storedView reads the order and By machine a browser stored (order,
// byMachine: "true" or "false"). An order of "machines", which a build
// before By machine kept, is the groups by machine.
export function storedView(order: string | null | undefined, byMachine: string | null | undefined): {order: ListOrder; byMachine: boolean} {
  if (order === "machines") return {order: "groups", byMachine: true};
  return {order: parseListOrder(order), byMachine: byMachine === "true"};
}

// timeOf reads a timestamp: none for one that is missing or doesn't parse,
// and for Go's zero time (0001-01-01), the last_seen of an instance the hub
// never heard from.
function timeOf(at: string | null | undefined): number | undefined {
  if (!at) return undefined;
  const time = Date.parse(at);
  return Number.isNaN(time) || time <= 0 ? undefined : time;
}

// changedAt is when an instance last changed what its row shows, which the
// recent order sorts by and its rows show: as the hub recorded it
// (changed_at), else since when the row says what it does, else when the
// hub last heard of it. A hub that records none (an older one), or an
// instance it kept before it did, falls back until its next change.
export function changedAt(instance: HubInstance): string | undefined {
  for (const at of [instance.changed_at, activity(instance).since, instance.last_seen]) {
    if (timeOf(at) !== undefined) return at;
  }
  return undefined;
}

// recentFirst compares two times, the most recent first and none last.
function recentFirst(a: number | undefined, b: number | undefined): number {
  if (a === b) return 0;
  if (a === undefined) return 1;
  if (b === undefined) return -1;
  return b - a;
}

// active reports whether an instance belongs at the top of the recent
// order: it has had a task (one runs, finished, or ran before), and isn't
// muted. One with no task yet, or muted, has nothing to look at, however
// recently it changed (it reconnected, it stopped).
function active(instance: HubInstance): boolean {
  if (instanceState(instance) === "muted") return false;
  const summary = instance.summary;
  return (summary?.tasks ?? 0) > 0 || summary?.task != null;
}

// sortRecent orders the rows: those with tasks first, then those with none
// or muted, each by changedAt, the most recent first, those with none last;
// ties by name, then ID.
export function sortRecent(instances: Iterable<HubInstance>): HubInstance[] {
  return [...instances]
    .map((instance) => ({instance, active: active(instance), at: timeOf(changedAt(instance))}))
    .sort(
      (a, b) =>
        Number(b.active) - Number(a.active) ||
        recentFirst(a.at, b.at) ||
        displayName(a.instance).localeCompare(displayName(b.instance)) ||
        a.instance.id.localeCompare(b.instance.id),
    )
    .map(({instance}) => instance);
}

// pinOf is an instance's place among the pinned ones, from 1; none when it
// isn't pinned, or the hub sent something else.
export function pinOf(instance: HubInstance): number | undefined {
  const pin = instance.pin;
  return typeof pin === "number" && Number.isInteger(pin) && pin > 0 ? pin : undefined;
}

// sortPinned returns the pinned instances in the hub's order: by place, ties
// by ID, as the hub numbers them. Two share a place for a moment while the
// hub's events of an order arrive one by one.
export function sortPinned(instances: Iterable<HubInstance>): HubInstance[] {
  return [...instances]
    .filter((instance) => pinOf(instance) !== undefined)
    .sort((a, b) => pinOf(a)! - pinOf(b)! || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

// movePin moves the pinned instance id one place up (step -1) or down (1)
// past the next pinned one the list shows (shown: a filter or a search may
// hide some), and returns the new order of all the pinned ones (order), for
// the hub; none when it is at that end already, or isn't among them.
export function movePin(order: readonly string[], shown: readonly string[], id: string, step: -1 | 1): string[] | undefined {
  const visible = new Set(shown);
  if (!visible.has(id) || !order.includes(id)) return undefined;
  let neighbor = order.indexOf(id) + step;
  while (neighbor >= 0 && neighbor < order.length && !visible.has(order[neighbor])) neighbor += step;
  if (neighbor < 0 || neighbor >= order.length) return undefined;
  const moved = order.filter((other) => other !== id);
  const at = moved.indexOf(order[neighbor]);
  moved.splice(step < 0 ? at : at + 1, 0, id);
  return moved;
}

// ListView is how the list shows the instances: which ones (the filter, the
// agents and the search), in what order, and whether by machine.
export interface ListView {
  filter: HubFilter;
  // agents are the agents picked; none is every agent.
  agents: ReadonlySet<HubAgent>;
  query: string;
  order: ListOrder;
  byMachine?: boolean;
}

// The keys of the pinned section and of the recent order's rows, as the
// browser keeps the sections it folded, beside the groups' names: a group's
// name has no space at either end.
export const pinnedKey = " pinned";
export const recentKey = " recent";

// A section of the list: the pinned instances, a group (name; "" for the
// instances in no group), or the recent order's rows.
export interface ListSection extends HubGroup {
  // key tells it from the others, as the browser keeps it folded.
  key: string;
  // label is its header; "" when the list shows none.
  label: string;
  pinned?: true;
  // machines are its instances by machine, by machine; instances are then
  // in the order the machines show them.
  machines?: HubMachine[];
}

// listSections is what the list shows: the instances that pass the filter,
// the agents and the search, the pinned ones first, then the others in their
// groups or by their latest change; none when none passes. By machine, each
// machine's are together: in a group, the machine that needs you the most
// first (groupByMachine); in the recent order, the machine of the latest
// change first, each one's agents by their latest change. Once there is a
// group, the groups get headers; below pinned ones every section does, so
// that the pinned ones read apart from the rest. The pinned ones stay one by
// one, in the order they were pinned.
export function listSections(instances: Iterable<HubInstance>, {filter, agents, query, order, byMachine = false}: ListView): ListSection[] {
  const all = [...instances];
  const visible = all.filter((instance) => matchesFilter(instance, filter) && matchesAgents(instance, agents) && matchesSearch(instance, query));
  const pinned = sortPinned(visible);
  const rest = visible.filter((instance) => pinOf(instance) === undefined);
  const headers = pinned.length > 0 || (order !== "recent" && groupNames(all).length > 0);
  const sections: ListSection[] = [];
  if (pinned.length > 0) sections.push({key: pinnedKey, label: "Pinned", name: "", pinned: true, instances: pinned, needsYou: needsYou(pinned)});
  if (order === "groups" && !byMachine) {
    for (const group of groupInstances(rest)) sections.push({...group, key: group.name, label: headers ? group.name || "Ungrouped" : ""});
  } else if (order === "groups") {
    for (const group of groupInstances(rest)) {
      const machines = groupByMachine(group.instances);
      const instances = machines.flatMap((machine) => machine.instances);
      sections.push({...group, instances, machines, key: group.name, label: headers ? group.name || "Ungrouped" : ""});
    }
  } else if (rest.length > 0) {
    const rows = sortRecent(rest);
    const section: ListSection = {key: recentKey, label: headers ? "Recent changes" : "", name: "", instances: rows, needsYou: needsYou(rows)};
    if (byMachine) {
      section.machines = machinesInOrder(rows);
      section.instances = section.machines.flatMap((machine) => machine.instances);
    }
    sections.push(section);
  }
  return sections;
}

// recentAgent is the agent a machine's card shows in the recent order: the
// one open beside the list when the card has it, else the one picked on it
// if that was at its latest change or after (picked, at when; a pick that
// doesn't say when is older than any change), else the latest change (the
// first, as machinesInOrder keeps them).
export function recentAgent(machine: HubMachine, picked?: {id: string; at?: number}, selected?: string): string {
  const has = (id: string | undefined) => !!id && machine.instances.some((instance) => instance.id === id);
  if (has(selected)) return selected!;
  const latest = machine.instances[0];
  if (picked && has(picked.id) && picked.at !== undefined && picked.at >= (timeOf(changedAt(latest)) ?? 0)) return picked.id;
  return latest.id;
}
