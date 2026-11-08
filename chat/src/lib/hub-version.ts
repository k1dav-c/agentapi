// The AgentAPI versions: the hub's own and each instance's, to tell the
// instances that run an older release than the hub, whose machines need
// an update.

import type {HubInstance} from "./hub";

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

