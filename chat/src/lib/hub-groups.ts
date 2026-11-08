// The groups someone put the instances in on the hub's page (the hub keeps
// each instance's), and what the instances of each spent and worked.

import {needsYou, sortInstances, type HubInstance} from "./hub";

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

// FigureRow is an instance's figures in the last 24 hours and 7 days (what
// it spent, or worked), to add up.
export type FigureRow = {instance: HubInstance; day: number; week: number};

// addUp adds up the rows' figures by nameOf their instances, in the order
// the names come.
export function addUp(rows: Iterable<FigureRow>, nameOf: (instance: HubInstance) => string): Map<string, GroupSubtotal> {
  const byName = new Map<string, GroupSubtotal>();
  for (const {instance, day, week} of rows) {
    const name = nameOf(instance);
    const subtotal = byName.get(name) ?? {name, day: 0, week: 0};
    subtotal.day += day;
    subtotal.week += week;
    byName.set(name, subtotal);
  }
  return byName;
}

// groupSubtotals adds up each instance's figures by its group, in
// groupInstances order, leaving out the groups with none this week. It is
// empty while no instance is in a group: the subtotal would be the total.
export function groupSubtotals(rows: Iterable<FigureRow>): GroupSubtotal[] {
  const byName = addUp(rows, groupOf);
  if (byName.size === 1 && byName.has("")) return [];
  return [...byName.values()].filter((group) => group.week > 0).sort((a, b) => groupOrder(a.name, b.name));
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
