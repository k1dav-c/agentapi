import {describe, expect, test} from "bun:test";
import {buildTimeline, collectToolCalls, getTaskActivity, getTaskStatus, shareTimeline, shareToolCalls, type TaskSection, type ToolCall} from "./task-timeline";
import type {RichMessage} from "@/components/chat-provider";

const tool = (id: string, timestamp: string): ToolCall => ({id, name: "Bash", input: {command: "ls"}, timestamp});

const task = (overrides: Partial<TaskSection>): TaskSection => ({
  key: "task-1",
  prompt: {id: 0, role: "user", content: "List files", time: "2026-10-04T08:00:00Z"},
  responses: [{id: 1, role: "agent", content: "● Here are the files (cut off by the scre", time: "2026-10-04T08:00:09Z"}],
  toolCalls: [],
  richActivity: [],
  ...overrides,
});

const kinds = (items: ReturnType<typeof getTaskActivity>) => items.map((item) => item.type === "message" ? `message:${item.key}` : `${item.type}:${item.key}`);

describe("getTaskActivity", () => {
  test("a structured transcript replaces the screen text", () => {
    const t = task({
      toolCalls: [tool("t1", "2026-10-04T08:00:02Z")],
      richActivity: [
        {type: "thinking", key: "th", content: "Listing", timestamp: "2026-10-04T08:00:01Z"},
        {type: "tool", key: "rich-tool-t1", toolCall: tool("t1", "2026-10-04T08:00:02Z")},
        {type: "message", key: "rm", message: {id: -1, role: "assistant", content: "Here are the files.", time: "2026-10-04T08:00:08Z"}},
      ],
    });
    expect(kinds(getTaskActivity(t))).toEqual(["thinking:th", "tool:rich-tool-t1", "message:rm"]);
  });

  test("while running, the live screen text stays at the end", () => {
    const t = task({richActivity: [{type: "thinking", key: "th", content: "…", timestamp: "2026-10-04T08:00:01Z"}]});
    expect(kinds(getTaskActivity(t, {live: true}))).toEqual(["thinking:th", "message:response-1"]);
  });

  test("tools only in the session log (no text yet) still show the screen text", () => {
    const t = task({richActivity: [{type: "tool", key: "rich-tool-t1", toolCall: tool("t1", "2026-10-04T08:00:02Z")}]});
    expect(kinds(getTaskActivity(t))).toEqual(["tool:rich-tool-t1", "message:response-1"]);
  });

  test("without a session log, screen text and tools are in time order", () => {
    const t = task({toolCalls: [tool("t2", "2026-10-04T08:00:05Z")]});
    expect(kinds(getTaskActivity(t))).toEqual(["tool:tool-t2", "message:response-1"]);
  });

  test("the native transport shows only the transcript, never the joined agent text", () => {
    const t = task({
      toolCalls: [tool("t2", "2026-10-04T08:00:05Z")],
      richActivity: [{type: "thinking", key: "th", content: "…", timestamp: "2026-10-04T08:00:01Z"}],
    });
    expect(kinds(getTaskActivity(t, {live: true, transcriptOnly: true}))).toEqual(["thinking:th", "tool:tool-t2"]);
    // A steered message's task holds only the agent text of the reply,
    // which belongs to the task before it.
    expect(getTaskActivity(task({}), {transcriptOnly: true})).toEqual([]);
  });
});

import {splitThinking} from "./thinking";

describe("splitThinking", () => {
  test("uses the bold heading as the title", () => {
    expect(splitThinking("**Listing test files**\n\nI'll use rg.")).toEqual({title: "Listing test files", body: "I'll use rg."});
  });
  test("falls back to the first line", () => {
    expect(splitThinking("Need to check the queue first.\nThen the status.")).toEqual({title: "Need to check the queue first.", body: "Need to check the queue first.\nThen the status."});
  });
});

describe("getTaskStatus", () => {
  const failed: ToolCall = {...tool("t1", "2026-10-04T08:00:02Z"), isError: true, status: "failed", result: "Exit code: 2"};
  const reply = {type: "message" as const, key: "m1", message: {id: 2, role: "assistant" as const, content: "The directory doesn't exist.", time: "2026-10-04T08:00:03Z"}};

  test("a failed command the agent went on from doesn't fail the task", () => {
    const t = task({toolCalls: [failed], richActivity: [{type: "tool", key: "t1", toolCall: failed}, reply]});
    expect(getTaskStatus(t, 0, 1, "stable")).toBe("completed");
  });
  test("a task that ends on a failed command failed", () => {
    const t = task({toolCalls: [failed], richActivity: [reply, {type: "tool", key: "t1", toolCall: failed}]});
    expect(getTaskStatus(t, 0, 1, "stable")).toBe("failed");
  });
  test("the latest task is running while the agent works, even right after a failed command", () => {
    const t = task({toolCalls: [failed], richActivity: [{type: "tool", key: "t1", toolCall: failed}]});
    expect(getTaskStatus(t, 0, 1, "running")).toBe("running");
  });
  test("without a session log, the last tool call decides", () => {
    expect(getTaskStatus(task({toolCalls: [failed, tool("t2", "2026-10-04T08:00:04Z")]}), 0, 1, "stable")).toBe("completed");
    expect(getTaskStatus(task({toolCalls: [failed]}), 0, 1, "stable")).toBe("failed");
  });
});

describe("collectToolCalls", () => {
  test("keeps the stored images of a result", () => {
    const [call] = collectToolCalls([
      {message_id: "a", role: "assistant", timestamp: "2026-10-11T00:00:00Z", content: [
        {type: "tool_use", tool_use_id: "t1", tool_name: "Read", tool_input: {file_path: "/tmp/shot.png"}},
      ]},
      {message_id: "u", role: "user", timestamp: "2026-10-11T00:00:01Z", content: [
        {type: "tool_result", tool_use_id: "t1", images: [{media_type: "image/png", url: "/uploads/0123456789abcdef/image.png"}, {media_type: "image/png"}]},
      ]},
    ]);
    expect(call.images).toEqual(["/uploads/0123456789abcdef/image.png"]);
    expect(call.result).toBe("");
  });
});

describe("sharing what didn't change", () => {
  const user = (id: number, time: string) => ({id, role: "user" as const, content: `task ${id}`, time});
  const agent = (id: number, content: string, time: string) => ({id, role: "agent" as const, content, time});
  const rich = (id: string, time: string, blocks: RichMessage["content"]) =>
    ({message_id: id, role: "assistant", timestamp: time, content: blocks}) as unknown as RichMessage;

  test("a rebuild with nothing new is the previous timeline", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), agent(2, "done", "2026-01-01T00:00:01Z")];
    const richMessages = [rich("m1", "2026-01-01T00:00:01Z", [{type: "tool_use", tool_use_id: "t1", tool_name: "Read", tool_input: {path: "/a"}} as never])];
    const calls = collectToolCalls(richMessages);
    const first = buildTimeline(messages, richMessages, calls);
    const again = shareTimeline(first, buildTimeline(messages, richMessages, shareToolCalls(calls, collectToolCalls(richMessages))));
    expect(again).toBe(first);
  });

  test("only the task that changed is new", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), agent(2, "done", "2026-01-01T00:00:01Z"), user(3, "2026-01-01T00:01:00Z")];
    const first = buildTimeline(messages, [], []);
    const streamed = [...messages, agent(4, "working", "2026-01-01T00:01:01Z")];
    const next = shareTimeline(first, buildTimeline(streamed, [], []));
    expect(next).not.toBe(first);
    expect(next.tasks[0]).toBe(first.tasks[0]);
    expect(next.tasks[1]).not.toBe(first.tasks[1]);
    expect(next.tasks[1].responses.map((m) => m.content)).toEqual(["working"]);
  });

  test("a tool call keeps its object until its result changes", () => {
    const use = rich("m1", "2026-01-01T00:00:01Z", [{type: "tool_use", tool_use_id: "t1", tool_name: "Bash", tool_input: {command: "ls"}} as never]);
    const before = collectToolCalls([use]);
    expect(shareToolCalls(before, collectToolCalls([use]))[0]).toBe(before[0]);
    const result = rich("m2", "2026-01-01T00:00:02Z", [{type: "tool_result", tool_use_id: "t1", text: "a.go"} as never]);
    const after = shareToolCalls(before, collectToolCalls([use, result]));
    expect(after[0]).not.toBe(before[0]);
    expect(after[0].result).toBe("a.go");
  });
});
