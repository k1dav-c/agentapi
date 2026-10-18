import {describe, expect, test} from "bun:test";
import {filterTasks, timelineSignature} from "./task-filter";
import {buildTimeline, collectToolCalls} from "./task-timeline";
import type {Message, RichMessage} from "./conversation";

const messages: Message[] = [
  {id: 0, role: "user", content: "build the parser", time: "2026-10-18T09:00:00Z"},
  {id: 1, role: "agent", content: "Parser built.", time: "2026-10-18T09:00:05Z"},
  {id: 2, role: "user", content: "run the tests", time: "2026-10-18T09:01:00Z"},
  {id: 3, role: "agent", content: "Running…", time: "2026-10-18T09:01:05Z"},
];
const rich: RichMessage[] = [
  {
    message_id: "m1",
    role: "assistant",
    timestamp: "2026-10-18T09:01:02Z",
    content: [
      {type: "tool_use", tool_use_id: "t1", tool_name: "Bash", tool_input: {command: "go test"}, status: "failed"},
      {type: "tool_result", tool_use_id: "t1", text: "FAIL parser_test.go", is_error: true},
    ],
  },
];
const timeline = buildTimeline(messages, rich, collectToolCalls(rich));

describe("task filter", () => {
  test("no query and no filter keeps every task, with its place and status", () => {
    const all = filterTasks(timeline.tasks, "running", "all", "");
    expect(all.map(({index, status}) => [index, status])).toEqual([
      [0, "completed"],
      [1, "running"],
    ]);
  });

  test("a status filter, and tool errors", () => {
    expect(filterTasks(timeline.tasks, "running", "completed", "").map(({index}) => index)).toEqual([0]);
    expect(filterTasks(timeline.tasks, "stable", "tool-error", "").map(({index}) => index)).toEqual([1]);
  });

  test("a query keeps the tasks that contain it, and counts its matches", () => {
    const found = filterTasks(timeline.tasks, "stable", "all", "parser");
    expect(found.map(({index}) => index)).toEqual([0, 1]);
    expect(found[0].matchCount).toBeGreaterThan(0);
    expect(filterTasks(timeline.tasks, "stable", "all", "nowhere")).toEqual([]);
  });

  test("the signature changes with what shows, not otherwise", () => {
    const same = buildTimeline(messages.map((message) => ({...message})), rich, collectToolCalls(rich));
    expect(timelineSignature(same)).toBe(timelineSignature(timeline));
    const grown = buildTimeline([...messages.slice(0, 3), {...messages[3], content: "Running… done"}], rich, collectToolCalls(rich));
    expect(timelineSignature(grown)).not.toBe(timelineSignature(timeline));
  });
});
