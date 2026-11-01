// The hub list's two orders: in its groups, what needs you first in each
// (groupInstances), or one list by the latest change, the most recent
// first, whatever the group. Each browser keeps its choice.

import {activity, displayName, groupInstances, matchesFilter, matchesSearch, needsYou, type HubFilter, type HubGroup, type HubInstance} from "./hub";

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

// ListView is how the list shows the instances: which ones (the filter and
// the search) and in what order.
export interface ListView {
  filter: HubFilter;
  query: string;
  order: ListOrder;
}

// listGroups is what the list shows: the instances that pass the filter and
// the search, in their groups, or in the recent order as one block with no
// name, which gets no header; none when none passes.
export function listGroups(instances: Iterable<HubInstance>, {filter, query, order}: ListView): HubGroup[] {
  const visible = [...instances].filter((instance) => matchesFilter(instance, filter) && matchesSearch(instance, query));
  if (order === "groups") return groupInstances(visible);
  if (visible.length === 0) return [];
  const rows = sortRecent(visible);
  return [{name: "", instances: rows, needsYou: needsYou(rows)}];
}
