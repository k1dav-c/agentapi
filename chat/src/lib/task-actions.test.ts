import {describe, expect, test} from "bun:test";
import {taskMatchesQuery, taskToMarkdown} from "./task-actions";

const task = {
  prompt: "Fix the upload flow",
  responses: ["Implemented progress feedback."],
  tools: [
    {name: "test", input: '{"suite":"upload"}', result: "all passed"},
  ],
};

describe("task actions", () => {
  test("searches prompts, responses, and tool activity", () => {
    expect(taskMatchesQuery(task, "UPLOAD")).toBe(true);
    expect(taskMatchesQuery(task, "progress")).toBe(true);
    expect(taskMatchesQuery(task, "all passed")).toBe(true);
    expect(taskMatchesQuery(task, "unrelated")).toBe(false);
  });

  test("exports a complete Markdown task", () => {
    const markdown = taskToMarkdown(task, 3);
    expect(markdown).toContain("# Task 3");
    expect(markdown).toContain("## Prompt");
    expect(markdown).toContain("## Agent output");
    expect(markdown).toContain("## Tool: test");
  });
});
