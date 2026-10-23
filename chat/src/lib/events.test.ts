import {describe, expect, test} from "bun:test";
import {messageFromEvent, queueFromEvent, requestFromEvent, richMessageFromEvent, taskFromEvent, type RequestUpdateEvent, type RichMessageUpdateEvent, type TaskUpdateEvent} from "./events";

describe("events as they arrive", () => {
  test("a message_update becomes a message with its seq", () => {
    expect(messageFromEvent({id: 3, role: "agent", message: "Done.", time: "2026-10-22T10:00:00Z", seq: 41})).toEqual({
      id: 3,
      role: "agent",
      content: "Done.",
      time: "2026-10-22T10:00:00Z",
      seq: 41,
    });
  });

  test("a rich_message_update without content has no blocks", () => {
    const event: RichMessageUpdateEvent = JSON.parse(
      '{"message_id":"m1","role":"user","content":null,"timestamp":"2026-10-22T10:00:01Z","seq":42}',
    );
    const message = richMessageFromEvent(event);
    expect(message.content).toEqual([]);
    expect(message.seq).toBe(42);
  });

  test("a request_update keeps its seq, with null answers emptied", () => {
    const event: RequestUpdateEvent = {
      id: "req-1",
      kind: "question",
      title: "Questions",
      status: "resolved",
      response_capability: "live",
      created_at: "2026-10-22T10:00:02Z",
      response: {answers: {pm: ["bun"], notes: null}},
      seq: 43,
    };
    const request = requestFromEvent(event);
    expect(request.seq).toBe(43);
    expect(request.response?.answers).toEqual({pm: ["bun"], notes: []});
  });

  test("a task_update becomes a task with its seq and empty lists", () => {
    const event: TaskUpdateEvent = JSON.parse('{"id":4,"status":"limited","prompt":"fix it","started_at":"2026-10-22T10:00:00Z","ended_at":"2026-10-22T10:05:00Z","seq":44}');
    const task = taskFromEvent(event);
    expect(task.seq).toBe(44);
    expect(task.status).toBe("limited");
    expect(task.errors).toEqual([]);
    expect(task.steered).toEqual([]);
  });

  test("a queue_update is its queue", () => {
    const queue = [{id: 2, content: "next", time: "2026-10-22T10:00:00Z"}];
    expect(queueFromEvent({queue, seq: 5})).toBe(queue);
    expect(queueFromEvent(JSON.parse('{"queue":null,"seq":6}'))).toEqual([]);
  });
});
