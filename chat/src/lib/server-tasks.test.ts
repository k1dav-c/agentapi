import {describe, expect, test} from "bun:test";
import {mergeTasks, normalizeTask, taskStatusDetail, tasksByMessage, type ServerTask} from "./server-tasks";

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
  test("missing or null lists are empty", () => {
    const normalized = normalizeTask(JSON.parse('{"id":3,"status":"failed","prompt":"x","started_at":"2026-10-22T10:00:00Z","errors":null,"seq":9}'));
    expect(normalized.errors).toEqual([]);
    expect(normalized.steered).toEqual([]);
    expect(normalized.seq).toBe(9);
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
});
