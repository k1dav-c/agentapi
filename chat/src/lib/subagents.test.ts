import {describe, expect, test} from "bun:test";
import {isSubagentTool, subagentCounts, subagentTask} from "./subagents";
import type {RichMessage} from "./conversation";
import {buildTranscript} from "./task-timeline";

const use = (id: string, name = "Agent"): RichMessage => ({
  message_id: `m-${id}`, role: "assistant", timestamp: "2026-10-18T10:00:00Z",
  content: [{type: "tool_use", tool_use_id: id, tool_name: name}],
});
const result = (id: string, status?: "running" | "completed"): RichMessage => ({
  message_id: `r-${id}`, role: "user", timestamp: "2026-10-18T10:01:00Z",
  content: [{type: "tool_result", tool_use_id: id, status}],
});

describe("subagents", () => {
  test("subagent tools", () => {
    expect(isSubagentTool("Agent")).toBe(true);
    expect(isSubagentTool("Task")).toBe(true);
    expect(isSubagentTool("spawn_agent")).toBe(true);
    expect(isSubagentTool("Bash")).toBe(false);
    expect(isSubagentTool(undefined)).toBe(false);
  });

  test("counts the subagents and the running ones", () => {
    const messages = [use("a"), use("b"), use("c"), use("x", "Bash"), result("a"), result("b", "running")];
    expect(subagentCounts(buildTranscript([], messages).toolCalls)).toEqual({total: 3, running: 2});
    expect(subagentCounts([])).toEqual({total: 0, running: 0});
  });

  test("a run becomes a task", () => {
    const task = subagentTask({
      subagent: {id: "s1", status: "completed", started_at: "2026-10-18T10:00:00Z", updated_at: "2026-10-18T10:02:00Z", description: "List"},
      prompt: "List the files",
      messages: [
        {message_id: "m1", role: "assistant", timestamp: "2026-10-18T10:00:01Z", content: [{type: "text", text: "Done."}]},
      ],
    });
    expect(task.key).toBe("subagent-s1");
    expect(task.prompt.content).toBe("List the files");
    expect(task.richActivity.map((item) => item.type)).toEqual(["message"]);
  });
});
