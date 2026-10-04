import {describe, expect, test} from "bun:test";
import {getTaskActivity, type TaskSection, type ToolCall} from "./task-timeline";

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
