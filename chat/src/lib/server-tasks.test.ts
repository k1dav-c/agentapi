import {describe, expect, test} from "bun:test";
import {latestTaskEnd, mergeTasks, normalizeTask, steeredMessages, taskStatusDetail, tasksByMessage, type ServerTask} from "./server-tasks";

const task = (id: number, status: ServerTask["status"], seq: number, steered: number[] = []): ServerTask => ({
  id,
  status,
  prompt: `task ${id}`,
  started_at: "2026-10-22T10:00:00Z",
  errors: [],
  steered,
  seq,
});

describe("server tasks", () => {
  test("the latest task's end is what the user hasn't seen", () => {
    expect(latestTaskEnd([])).toBeUndefined();
    const ended = {...task(0, "completed", 2), ended_at: "2026-10-22T10:01:00Z"};
    expect(latestTaskEnd([ended])).toBe("2026-10-22T10:01:00Z");
    expect(latestTaskEnd([ended, task(4, "running", 5)])).toBeUndefined();
  });

  test("missing or null lists are empty", () => {
    const normalized = normalizeTask(JSON.parse('{"id":3,"status":"failed","prompt":"x","started_at":"2026-10-22T10:00:00Z","errors":null,"seq":9}'));
    expect(normalized.errors).toEqual([]);
    expect(normalized.steered).toEqual([]);
    expect(normalized.seq).toBe(9);
  });

  test("a status a newer server added is one this chat knows", () => {
    const parse = (fields: string) =>
      normalizeTask(JSON.parse(`{"id":3,"status":"paused","prompt":"x","started_at":"2026-10-22T10:00:00Z",${fields}"seq":9}`)).status;
    expect(parse("")).toBe("running");
    expect(parse('"ended_at":"2026-10-22T10:01:00Z",')).toBe("completed");
    expect(parse('"ended_at":"2026-10-22T10:01:00Z","errors":["the agent exited"],')).toBe("failed");
    expect(parse('"errors":null,')).toBe("running");
    expect(normalizeTask(task(1, "limited", 2)).status).toBe("limited");
  });

  test("updates replace a task in place, a new one comes last, and an older seq doesn't win", () => {
    const merged = mergeTasks([task(0, "completed", 2), task(4, "running", 5)], [task(4, "completed", 7), task(8, "running", 8), task(0, "running", 1)]);
    expect(merged.map((t) => [t.id, t.status])).toEqual([[0, "completed"], [4, "completed"], [8, "running"]]);
  });

  test("a failed task tells its first error, a limited one its limit", () => {
    expect(taskStatusDetail({...task(1, "failed", 2), errors: ["the agent exited", "later"]})).toBe("the agent exited");
    expect(taskStatusDetail({...task(1, "limited", 2), limit: {message: "You've hit your limit · resets 3pm", stopped_at: "2026-10-22T10:00:00Z"}})).toBe(
      "You've hit your limit · resets 3pm",
    );
    expect(taskStatusDetail(task(1, "completed", 2))).toBeUndefined();
    expect(taskStatusDetail(undefined)).toBeUndefined();
  });

  test("a task is found by the message that started it and those steered into it", () => {
    const running = task(4, "running", 5, [6, 7]);
    const byMessage = tasksByMessage([task(0, "completed", 2), running]);
    expect(byMessage.get(4)).toBe(running);
    expect(byMessage.get(7)).toBe(running);
    expect(byMessage.get(5)).toBeUndefined();
  });

  test("the messages steered into a task", () => {
    expect([...steeredMessages([task(0, "completed", 2, [1]), task(4, "running", 5, [6, 7])])]).toEqual([1, 6, 7]);
    expect(steeredMessages([]).size).toBe(0);
  });
});
