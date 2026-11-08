import {describe, expect, test} from "bun:test";
import {idleInstance as instance, summary} from "./hub-fixtures";
import type {HubInstance} from "./hub";
import {matchesSearch} from "./hub-filter";
import {compareVersions, instanceVersion, olderThanHub, parseVersion, versionTitle, versionTotals} from "./hub-version";

describe("versions", () => {
  const running = (id: string, version: string, overrides: Partial<HubInstance> = {}): HubInstance =>
    instance(id, {server_id: "s", agentapi_version: version, hostname: "dev-1", summary: summary({instance: {id, server_id: "s", agent_type: "claude", transport: "native", agentapi_version: version}}), ...overrides});

  test("versions: releases compare by number; development builds compare with nothing", () => {
    expect(parseVersion("2.1.0")).toEqual([2, 1, 0]);
    expect(parseVersion("v1.6.1")).toEqual([1, 6, 1]);
    for (const build of ["0.0.0-dev", "2.0.0-21-g61be34f", "2.0.0-21-g61be34f-dirty", "0.0.0", "2.1", "", undefined]) expect(parseVersion(build)).toBeUndefined();
    expect(compareVersions("1.8.1", "2.1.0")).toBeLessThan(0);
    expect(compareVersions("1.10.0", "1.9.3")).toBeGreaterThan(0);
    expect(compareVersions("2.1.0", "v2.1.0")).toBe(0);
    expect(compareVersions("0.0.0-dev", "2.1.0")).toBeUndefined();
    expect(compareVersions("2.1.0", "0.0.0-dev")).toBeUndefined();
  });

  test("versions: an instance's is its summary's, unless that summary is of an earlier run", () => {
    expect(instanceVersion(running("a", "2.0.0"))).toBe("2.0.0");
    // A summary is the instance's latest word.
    expect(instanceVersion(running("a", "2.0.0", {agentapi_version: "1.8.1"}))).toBe("2.0.0");
    // Restarted in place: the new run's event came, its first summary not yet.
    const restarted = running("a", "2.0.0", {server_id: "s2", agentapi_version: "2.1.0", connection: "connecting", state: "idle"});
    expect(instanceVersion(restarted)).toBe("2.1.0");
    expect(instanceVersion(instance("b", {agentapi_version: "1.6.1", summary: undefined, connection: "connecting", state: "connecting"}))).toBe("1.6.1");
    expect(instanceVersion(running("c", "1.8.1", {server_id: undefined, agentapi_version: undefined}))).toBe("1.8.1");
    expect(instanceVersion(instance("d"))).toBe("");
  });

  test("versions: older than the hub, only when both are releases and the hub follows it", () => {
    expect(olderThanHub(running("a", "1.6.1"), "2.1.0")).toBe(true);
    expect(olderThanHub(running("a", "2.1.0"), "2.1.0")).toBe(false);
    expect(olderThanHub(running("a", "2.2.0"), "2.1.0")).toBe(false);
    expect(olderThanHub(running("a", "0.0.0-dev"), "2.1.0")).toBe(false);
    expect(olderThanHub(running("a", "1.6.1"), "0.0.0-dev")).toBe(false);
    expect(olderThanHub(running("a", "1.6.1"), undefined)).toBe(false);
    expect(olderThanHub(running("a", "1.6.1", {connection: "stopped", state: "stopped"}), "2.1.0")).toBe(false);
    expect(versionTitle("1.6.1", "2.1.0")).toBe("AgentAPI v1.6.1, older than the hub (v2.1.0)");
    expect(versionTitle("0.0.0-dev", "2.1.0")).toBe("AgentAPI v0.0.0-dev, a development build");
    expect(versionTitle("1.6.1", "2.1.0", true)).toBe("Last known: AgentAPI v1.6.1");
  });

  test("versions: the overview counts each version of the instances the hub follows, and those older than the hub", () => {
    const rows = [
      running("a", "1.6.1", {hostname: "dev-2"}),
      running("b", "1.6.1", {hostname: "dev-1"}),
      running("c", "1.6.1", {hostname: "dev-2"}),
      running("d", "2.1.0"),
      running("e", "1.8.1", {hostname: "dev-3"}),
      running("f", "0.0.0-dev"),
      running("g", "1.10.0"),
      instance("h"),
      // Not followed now: its version is the last it ran.
      running("i", "1.2.0", {connection: "stopped", state: "stopped"}),
    ];
    const totals = versionTotals(rows, "2.1.0");
    expect(totals.versions.map((v) => [v.version, v.relation, v.instances, v.machines])).toEqual([
      ["2.1.0", "same", 1, ["dev-1"]],
      ["1.10.0", "older", 1, ["dev-1"]],
      ["1.8.1", "older", 1, ["dev-3"]],
      ["1.6.1", "older", 3, ["dev-1", "dev-2"]],
      ["0.0.0-dev", "unknown", 1, ["dev-1"]],
      ["", "unknown", 1, []],
    ]);
    expect(totals.older).toBe(5);
    expect(totals.counted).toBe(8);
    // A development build of the hub tells no instance older.
    expect(versionTotals(rows, "0.0.0-dev").older).toBe(0);
  });

  test("search finds the instances of a version", () => {
    const old = running("a", "1.6.1");
    expect(matchesSearch(old, "1.6.1")).toBe(true);
    expect(matchesSearch(old, "v1.6.1")).toBe(true);
    expect(matchesSearch(old, "1.8.1")).toBe(false);
  });
});
