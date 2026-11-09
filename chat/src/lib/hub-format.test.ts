import {describe, expect, test} from "bun:test";
import {idleInstance as instance, summary} from "./hub-fixtures";
import {sortInstances, stateLabel} from "./hub";
import {countFilters} from "./hub-filter";
import {activity, formatElapsed, formatTimeAndDay, shortError} from "./hub-format";

describe("what a row says", () => {
  test("a row says what the instance waits for or does, and since when", () => {
    const asked = instance("a", {state: "needs-you", summary: summary({attention: "request", pending_requests: 1, request: {id: "r1", kind: "approval", title: "Run: make release", created_at: "2026-10-26T10:00:00Z"}})});
    expect(activity(asked)).toEqual({label: "Approval", text: "Run: make release", tone: "needs", since: "2026-10-26T10:00:00Z"});
    expect(activity(instance("b", {state: "needs-you", summary: summary({attention: "login"})}))).toMatchObject({label: "Login", text: "Log in to continue"});
    const working = instance("c", {state: "working", summary: summary({status: "running", task: {id: 1, status: "running", prompt: "Migrate invoices", started_at: "2026-10-26T10:00:00Z", errors: [], steered: []}})});
    expect(activity(working)).toEqual({label: "Working", text: "Migrate invoices", tone: "working", since: "2026-10-26T10:00:00Z"});
    const retrying = instance("f", {state: "working", summary: summary({retry_wait: {message: "API Error: 502 Bad Gateway", api_status: 502, stopped_at: "2026-10-26T10:00:00Z", attempt: 1, attempts: 3, retry_at: "2026-10-26T10:00:30Z"}})});
    expect(activity(retrying)).toEqual({label: "Retrying", text: "The API returned 502 · try 1 of 3", tone: "working", since: "2026-10-26T10:00:00Z"});
    expect(activity(instance("d", {state: "unreachable", connection: "unreachable", error: "connection refused"})).tone).toBe("fault");
    expect(activity(instance("e")).text).toBe("No task yet");
  });

  test("running times are short", () => {
    const now = Date.parse("2026-10-26T10:00:00Z");
    expect(formatElapsed("2026-10-26T09:57:46Z", now)).toBe("2m 14s");
    expect(formatElapsed("2026-10-26T08:55:00Z", now)).toBe("1h 05m");
  });

  test("connection errors keep the part that says what happened", () => {
    expect(shortError('unreachable: Get "http://127.0.0.1:3308/events?only=summary": dial tcp 127.0.0.1:3308: connect: connection refused')).toBe("Connection refused");
    expect(shortError("the instance needs an API token (--instance-token)")).toBe("the instance needs an API token (--instance-token)");
    expect(shortError(undefined)).toBe("");
  });

  test("a muted instance waits out of the way until what it waits for changes", () => {
    const login = summary({attention: "login", login_required: {agent: "codex", command: "codex login", message: "Codex is not logged in."}});
    const muted = instance("a", {state: "muted", summary: login, muted: "login"});
    expect(stateLabel(muted)).toBe("Muted · Needs login");
    expect(activity(muted)).toMatchObject({label: "Muted", text: "Codex is not logged in.", tone: "muted"});
    expect(countFilters([muted])["needs-you"]).toBe(0);
    // Sorted after the idle ones.
    expect(sortInstances([muted, instance("b")]).map((i) => i.id)).toEqual(["b", "a"]);
  });
});

describe("formatTimeAndDay", () => {
  test("the time of day, with the day when it isn't today", () => {
    const at = new Date(2026, 10, 9, 10, 20);
    const now = new Date(2026, 10, 9, 15, 0).getTime();
    const time = at.toLocaleTimeString(undefined, {hour: "2-digit", minute: "2-digit"});
    expect(formatTimeAndDay(at.toISOString(), now)).toBe(time);
    expect(formatTimeAndDay(at.toISOString(), now + 86_400_000)).toBe(`${at.toLocaleDateString(undefined, {month: "short", day: "numeric"})} ${time}`);
    expect(formatTimeAndDay(undefined, now)).toBe("");
    expect(formatTimeAndDay("not a time", now)).toBe("");
  });
});
