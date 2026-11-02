// The hub list's two orders: in its groups, what needs you first in each
// (groupInstances), or one list by the latest change, the most recent
// first, whatever the group. Each browser keeps its choice. The instances
// pinned on the hub come first in both, in their own order.

import {activity, displayName, groupInstances, groupNames, matchesFilter, matchesSearch, needsYou, type HubFilter, type HubGroup, type HubInstance} from "./hub";

export type ListOrder = "groups" | "recent";

// parseListOrder reads the order a browser stored: anything but "recent" is
// the groups, the default.
export function parseListOrder(stored: string | null | undefined): ListOrder {
  return stored === "recent" ? "recent" : "groups";
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

// sortRecent orders the rows by changedAt, the most recent first, those
// with none last; ties by name, then ID.
export function sortRecent(instances: Iterable<HubInstance>): HubInstance[] {
  return [...instances]
    .map((instance) => ({instance, at: timeOf(changedAt(instance))}))
    .sort((a, b) => recentFirst(a.at, b.at) || displayName(a.instance).localeCompare(displayName(b.instance)) || a.instance.id.localeCompare(b.instance.id))
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

// ListView is how the list shows the instances: which ones (the filter and
// the search) and in what order.
export interface ListView {
  filter: HubFilter;
  query: string;
  order: ListOrder;
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
}

// listSections is what the list shows: the instances that pass the filter
// and the search, the pinned ones first, then the others in their groups or
// by their latest change; none when none passes. Once there is a group, the
// groups get headers; below pinned ones every section does, so that the
// pinned ones read apart from the rest.
export function listSections(instances: Iterable<HubInstance>, {filter, query, order}: ListView): ListSection[] {
  const all = [...instances];
  const visible = all.filter((instance) => matchesFilter(instance, filter) && matchesSearch(instance, query));
  const pinned = sortPinned(visible);
  const rest = visible.filter((instance) => pinOf(instance) === undefined);
  const headers = pinned.length > 0 || (order === "groups" && groupNames(all).length > 0);
  const sections: ListSection[] = [];
  if (pinned.length > 0) sections.push({key: pinnedKey, label: "Pinned", name: "", pinned: true, instances: pinned, needsYou: needsYou(pinned)});
  if (order === "groups") {
    for (const group of groupInstances(rest)) sections.push({...group, key: group.name, label: headers ? group.name || "Ungrouped" : ""});
  } else if (rest.length > 0) {
    const rows = sortRecent(rest);
    sections.push({key: recentKey, label: headers ? "Recent changes" : "", name: "", instances: rows, needsYou: needsYou(rows)});
  }
  return sections;
}
