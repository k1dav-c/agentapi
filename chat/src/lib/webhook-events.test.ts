import {describe, expect, test} from "bun:test";
import {otherPatterns, patternsFor, selectedGroups, webhookEventMatches} from "./webhook-events";

describe("webhook events", () => {
  test("patterns match as on the server", () => {
    expect(webhookEventMatches("*", "task.completed")).toBe(true);
    expect(webhookEventMatches("task.*", "task.completed")).toBe(true);
    expect(webhookEventMatches("task.*", "tool.completed")).toBe(false);
    expect(webhookEventMatches("task", "task.completed")).toBe(false);
  });

  test("the groups the patterns select whole", () => {
    expect([...selectedGroups(["run.status_changed"])]).toEqual(["status"]);
    expect([...selectedGroups(["*"])].length).toBe(7);
    // Only some of the group's events: not the group.
    expect(selectedGroups(["request.*", "login.*"]).has("needs-you")).toBe(false);
    expect(selectedGroups(["request.*", "login.*", "limit.*", "task.*"])).toEqual(new Set(["needs-you", "tasks"]));
  });

  test("patterns beyond the whole groups are kept", () => {
    expect(otherPatterns(["task.completed", "instance.*"])).toEqual(["task.completed"]);
    expect(otherPatterns(["*"])).toEqual([]);
    expect(patternsFor(new Set(["instance"]), ["task.completed"])).toEqual(["instance.*", "task.completed"]);
  });

  test("all groups are *", () => {
    expect(patternsFor(new Set(["tasks", "needs-you", "instance", "status", "errors", "messages", "tools"]))).toEqual(["*"]);
    expect(patternsFor(new Set(["tasks", "needs-you"]))).toEqual(["task.*", "request.*", "login.*", "limit.*"]);
    expect(patternsFor(new Set())).toEqual([]);
  });
});
