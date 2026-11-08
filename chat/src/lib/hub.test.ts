import {describe, expect, test} from "bun:test";
import {idleInstance as instance, summary} from "./hub-fixtures";
import {applyHubEvent, chatURL, finishedAt, finishedLately, hasRun, hubRoot, RECENTLY_FINISHED, instanceState, needsYou, parseHubEvent, sortInstances, stateLabel, usagePercent} from "./hub";
import {countFilters} from "./hub-filter";
import {activity} from "./hub-format";

describe("hub", () => {
  test("a row shows the state the hub worked out", () => {
    expect(instanceState(instance("a", {state: "needs-you"}))).toBe("needs-you");
    expect(stateLabel(instance("a", {state: "needs-you", summary: summary({attention: "login"})}))).toBe("Needs login");
    expect(stateLabel(instance("a", {state: "working"}))).toBe("Working");
    expect(stateLabel(instance("a", {state: "unread"}))).toBe("Finished");
    expect(stateLabel(instance("a", {state: "connecting", summary: undefined, connection: "connecting"}))).toBe("Connecting");
    expect(stateLabel(instance("a", {state: "stopped", connection: "stopped"}))).toBe("Stopped");
    expect(stateLabel(instance("a", {state: "stopped", summary: summary({attention: "exited"})}))).toBe("Agent exited");
  });

  test("a state from a newer hub is unknown, and waits at the end", () => {
    const newer = instance("a", {state: "rebooting"});
    expect(instanceState(newer)).toBe("unknown");
    expect(instanceState(instance("b", {state: "toString"}))).toBe("unknown");
    expect(stateLabel(newer)).toBe("Unknown");
    expect(activity(newer)).toEqual({text: "Reload the page to see what it does", tone: "muted"});
    expect(sortInstances([newer, instance("b", {state: "stopped", connection: "stopped"})]).map((i) => i.id)).toEqual(["b", "a"]);
    expect(countFilters([newer])).toEqual({all: 1, "needs-you": 0, working: 0, unread: 0, offline: 0});
  });

  test("rows that need you come first, then by name", () => {
    const rows = sortInstances([
      instance("idle", {name: "b"}),
      instance("gone", {state: "stopped", connection: "stopped"}),
      instance("asks", {state: "needs-you", summary: summary({attention: "request"})}),
      instance("busy", {state: "working", summary: summary({status: "running"})}),
      instance("idle2", {name: "a"}),
    ]);
    expect(rows.map((row) => row.id)).toEqual(["asks", "busy", "idle2", "idle", "gone"]);
    expect(needsYou(rows)).toBe(1);
  });

  test("events replace, change and remove instances", () => {
    let map = applyHubEvent(new Map(), {type: "instances", instances: [instance("a"), instance("b")], hubVersion: ""});
    map = applyHubEvent(map, {type: "instance", instance: instance("a", {name: "renamed"})});
    map = applyHubEvent(map, {type: "removed", id: "b"});
    expect([...map.values()].map((i) => i.name ?? i.id)).toEqual(["renamed"]);
  });

  test("the stream's events read as their names say, and others not at all", () => {
    const a = instance("a");
    expect(parseHubEvent("instances", JSON.stringify({instances: [a, {name: "no id"}], hub_version: "3.7.3"}))).toEqual({type: "instances", instances: [a], hubVersion: "3.7.3"});
    expect(parseHubEvent("instances", JSON.stringify({instances: []}))).toEqual({type: "instances", instances: [], hubVersion: ""});
    expect(parseHubEvent("instance", JSON.stringify(a))).toEqual({type: "instance", instance: a});
    expect(parseHubEvent("removed", JSON.stringify({id: "a"}))).toEqual({type: "removed", id: "a"});
    expect(parseHubEvent("favorites", JSON.stringify({favorites: [{id: "f", text: "Hi"}, {text: "no id"}]}))).toEqual({type: "favorites", favorites: [{id: "f", text: "Hi"}]});
    for (const [type, data] of [
      ["instances", "{}"],
      ["instances", "not json"],
      ["instance", JSON.stringify({name: "no id"})],
      ["instance", "null"],
      ["removed", JSON.stringify({id: 7})],
      ["favorites", JSON.stringify({favorites: "all"})],
    ] as const) {
      expect(parseHubEvent(type, data)).toBeUndefined();
    }
  });

  test("the chat opens an instance through the hub", () => {
    expect(hubRoot("/chat", "https://hub.vpn:3290")).toBe("https://hub.vpn:3290");
    expect(hubRoot("/apps/hub/chat", "https://coder.example.com")).toBe("https://coder.example.com/apps/hub");
    expect(chatURL("/chat", "https://hub.vpn:3290", "dev-1:3284")).toBe("/chat/embed?url=https%3A%2F%2Fhub.vpn%3A3290%2Fi%2Fdev-1%253A3284");
    expect(chatURL("/chat", "https://hub.vpn:3290", "dev-1:3284", true)).toBe(
      "/chat/embed?url=https%3A%2F%2Fhub.vpn%3A3290%2Fi%2Fdev-1%253A3284&hub=1",
    );
  });

  test("the fullest usage limit", () => {
    expect(usagePercent(summary())).toBeUndefined();
    expect(
      usagePercent(summary({usage: {rate_limits: [{id: "five_hour", label: "5h", used_percent: 40}, {id: "seven_day", label: "7d", used_percent: 72}]}})),
    ).toBe(72);
  });

  test("an instance has run with a task, or work the hub saw this week", () => {
    expect(hasRun(instance("a"))).toBe(false);
    expect(hasRun(instance("a", {work: {last_24h_seconds: 0, last_7d_seconds: 0}}))).toBe(false);
    expect(hasRun(instance("a", {work: {last_24h_seconds: 0, last_7d_seconds: 60}}))).toBe(true);
    expect(hasRun(instance("a", {work: {last_24h_seconds: 0, last_7d_seconds: 0, working_since: "2026-11-01T09:00:00Z"}}))).toBe(true);
  });

  test("an idle agent that has run finished when it last changed, green for half an hour", () => {
    const ran = {summary: summary({tasks: 2}), changed_at: "2026-11-08T10:00:00Z"};
    const at = Date.parse("2026-11-08T10:00:00Z");
    expect(finishedAt(instance("a", ran))).toBe(at);
    // Not one that never ran, nor one that works or needs you.
    expect(finishedAt(instance("a", {summary: summary({tasks: 0}), changed_at: "2026-11-08T10:00:00Z"}))).toBeUndefined();
    expect(finishedAt(instance("a", {...ran, state: "working"}))).toBeUndefined();
    expect(finishedLately(at, at + 60_000)).toBe(true);
    expect(finishedLately(at, at + RECENTLY_FINISHED - 1)).toBe(true);
    expect(finishedLately(at, at + RECENTLY_FINISHED)).toBe(false);
    expect(finishedLately(undefined, at)).toBe(false);
    // Outside a clock the time is 0: never lately.
    expect(finishedLately(at, 0)).toBe(false);
  });

});
