import {describe, expect, test} from "bun:test";
import {buildTranscript, countTaskMatches, getTaskActivity, getTaskStatus, TimelineBuilder, type TaskSection, type ToolCall} from "./task-timeline";
import type {DraftMessage, Message, RichMessage} from "./conversation";
import type {ServerTask} from "./server-tasks";

const tool = (id: string, timestamp: string): ToolCall => ({id, name: "Bash", input: {command: "ls"}, timestamp});

const task = (overrides: Partial<TaskSection>): TaskSection => ({
  key: "task-1",
  prompt: {id: 0, role: "user", content: "List files", time: "2026-10-04T08:00:00Z"},
  responses: [{id: 1, role: "agent", content: "● Here are the files (cut off by the scre", time: "2026-10-04T08:00:09Z"}],
  steered: [],
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
    expect(getTaskActivity(task({}), {transcriptOnly: true})).toEqual([]);
  });

  test("a message steered into the task shows where it came", () => {
    const steer = {id: 5, role: "user" as const, content: "Use rg instead", time: "2026-10-04T08:00:04Z"};
    const richActivity: TaskSection["richActivity"] = [
      {type: "thinking", key: "th", content: "Listing", timestamp: "2026-10-04T08:00:01Z"},
      {type: "tool", key: "rich-tool-t1", toolCall: tool("t1", "2026-10-04T08:00:02Z")},
      {type: "message", key: "rm", message: {id: -1, role: "assistant", content: "Switching to rg.", time: "2026-10-04T08:00:06Z"}},
    ];
    expect(kinds(getTaskActivity(task({richActivity, steered: [steer]}), {transcriptOnly: true}))).toEqual([
      "thinking:th",
      "tool:rich-tool-t1",
      "prompt:steered-5",
      "message:rm",
    ]);
    // One nothing came after yet goes last.
    expect(kinds(getTaskActivity(task({richActivity: richActivity.slice(0, 2), steered: [steer]}), {transcriptOnly: true})).at(-1)).toBe("prompt:steered-5");
    // Without a session log, by time with the screen text.
    expect(kinds(getTaskActivity(task({steered: [steer], toolCalls: [tool("t2", "2026-10-04T08:00:02Z")]})))).toEqual([
      "tool:tool-t2",
      "prompt:steered-5",
      "message:response-1",
    ]);
  });

  test("search finds a task by a message steered into it", () => {
    const t = task({steered: [{id: 5, role: "user", content: "Use ripgrep instead", time: "2026-10-04T08:00:04Z"}]});
    expect(countTaskMatches(t, "ripgrep")).toBe(1);
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

  const serverTask = (status: ServerTask["status"]): ServerTask => ({id: 0, status, prompt: "List files", started_at: "2026-10-04T08:00:00Z", errors: [], steered: []});

  test("the server's task says how it is, over what the steps suggest", () => {
    const endsFailing = task({toolCalls: [failed], richActivity: [reply, {type: "tool", key: "t1", toolCall: failed}]});
    expect(getTaskStatus(endsFailing, 0, 1, "stable", serverTask("completed"))).toBe("completed");
    // A task the agent is still on, though the agent looks idle (it waits
    // for an answer's turn to start).
    expect(getTaskStatus(task({}), 0, 2, "stable", serverTask("running"))).toBe("running");
    expect(getTaskStatus(task({}), 0, 1, "stable", serverTask("limited"))).toBe("limited");
    expect(getTaskStatus(task({}), 0, 1, "running", serverTask("failed"))).toBe("failed");
  });

  test("a message not sent yet is the chat's own", () => {
    const draft = task({prompt: {role: "user", content: "next", clientId: "c1", deliveryStatus: "sending"}});
    expect(getTaskStatus(draft, 1, 2, "running", serverTask("completed"))).toBe("queued");
    const unsent = task({prompt: {role: "user", content: "next", clientId: "c1", deliveryStatus: "failed"}});
    expect(getTaskStatus(unsent, 1, 2, "stable", serverTask("completed"))).toBe("failed");
  });
});

describe("tool calls", () => {
  test("keep the stored images of a result", () => {
    const {toolCalls: [call]} = buildTranscript([], [
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

  test("go into the task they started in, also when their result comes in a later one", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), user(3, "2026-01-01T00:01:00Z")];
    const {timeline} = buildTranscript(messages, [
      rich("m1", "2026-01-01T00:00:01Z", [{type: "tool_use", tool_use_id: "t1", tool_name: "Bash", tool_input: {command: "sleep 90"}}]),
      rich("m2", "2026-01-01T00:01:01Z", [{type: "text", text: "Next."}]),
      {message_id: "r1", role: "user", timestamp: "2026-01-01T00:01:30Z", content: [{type: "tool_result", tool_use_id: "t1", text: "done"}]},
    ]);
    expect(timeline.tasks[0].toolCalls.map((call) => [call.id, call.result])).toEqual([["t1", "done"]]);
    expect(timeline.tasks[0].richActivity.map((item) => item.key)).toEqual(["rich-tool-t1"]);
    expect(timeline.tasks[1].toolCalls).toEqual([]);
    expect(timeline.tasks[1].richActivity.map((item) => item.key)).toEqual(["rich-message-m2-0"]);
  });
});

const user = (id: number, time: string) => ({id, role: "user" as const, content: `task ${id}`, time});
const agent = (id: number, content: string, time: string) => ({id, role: "agent" as const, content, time});
const rich = (id: string, time: string, blocks: RichMessage["content"]): RichMessage =>
  ({message_id: id, role: "assistant", timestamp: time, content: blocks});

describe("rebuilding as messages stream", () => {
  test("a rebuild with nothing new is the previous transcript", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), agent(2, "done", "2026-01-01T00:00:01Z")];
    const richMessages = [rich("m1", "2026-01-01T00:00:01Z", [{type: "tool_use", tool_use_id: "t1", tool_name: "Read", tool_input: {path: "/a"}}])];
    const builder = new TimelineBuilder();
    const first = builder.build(messages, richMessages);
    expect(builder.build(messages, richMessages)).toBe(first);
    // A reconnect sends everything again, as new objects.
    const again = builder.build(structuredClone(messages), structuredClone(richMessages));
    expect(again.timeline).toBe(first.timeline);
    expect(again.toolCalls).toBe(first.toolCalls);
  });

  test("only the task that changed is new", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), agent(2, "done", "2026-01-01T00:00:01Z"), user(3, "2026-01-01T00:01:00Z")];
    const builder = new TimelineBuilder();
    const first = builder.build(messages, []).timeline;
    const next = builder.build([...messages, agent(4, "working", "2026-01-01T00:01:01Z")], []).timeline;
    expect(next).not.toBe(first);
    expect(next.tasks[0]).toBe(first.tasks[0]);
    expect(next.tasks[1]).not.toBe(first.tasks[1]);
    expect(next.tasks[1].responses.map((m) => m.content)).toEqual(["working"]);
  });

  test("output before the first task streams in", () => {
    const builder = new TimelineBuilder();
    expect(builder.build([agent(0, "Starting", "2026-01-01T00:00:00Z")], []).timeline.prelude.map((m) => m.content)).toEqual(["Starting"]);
    const {timeline} = builder.build([agent(0, "Starting… ready", "2026-01-01T00:00:00Z")], []);
    expect(timeline.prelude.map((m) => m.content)).toEqual(["Starting… ready"]);
    expect(timeline.tasks).toEqual([]);
  });

  test("an agent's message that moves to a later task leaves the task it was in", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), user(2, "2026-01-01T00:01:00Z"), user(3, "2026-01-01T00:02:00Z")];
    const richMessages = [
      rich("a", "2026-01-01T00:00:10Z", [{type: "text", text: "first"}]),
      rich("b", "2026-01-01T00:01:10Z", [{type: "text", text: "second"}]),
      rich("c", "2026-01-01T00:02:10Z", [{type: "text", text: "third"}]),
    ];
    const builder = new TimelineBuilder();
    builder.build(messages, richMessages);
    const moved = [richMessages[0], {...richMessages[1], timestamp: "2026-01-01T00:02:20Z"}, richMessages[2]];
    expect(builder.build(messages, moved)).toEqual(buildTranscript(messages, moved));
    expect(builder.build(messages, moved).timeline.tasks.map((task) => task.richActivity.length)).toEqual([1, 0, 2]);
  });

  test("a rich message goes into the task the server says it began in, one without by its time", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), user(3, "2026-01-01T00:01:00Z")];
    // Stamped after the second prompt, but part of the first task (it ran
    // on, or the clocks differ).
    const late: RichMessage = {...rich("a", "2026-01-01T00:01:10Z", [{type: "text", text: "still on task 1"}]), task_id: 1};
    const untagged = rich("b", "2026-01-01T00:01:20Z", [{type: "text", text: "by time"}]);
    const unknown: RichMessage = {...rich("c", "2026-01-01T00:00:30Z", [{type: "text", text: "a task the chat doesn't have"}]), task_id: 99};
    const use: RichMessage = {...rich("d", "2026-01-01T00:01:30Z", [{type: "tool_use", tool_use_id: "t1", tool_name: "Bash", tool_input: {command: "ls"}}]), task_id: 1};
    const result: RichMessage = {message_id: "r", role: "user", timestamp: "2026-01-01T00:01:31Z", content: [{type: "tool_result", tool_use_id: "t1", text: "a.go"}]};
    const {timeline} = buildTranscript(messages, [late, untagged, unknown, use, result]);
    const keys = (index: number) => timeline.tasks[index].richActivity.map((item) => item.key);
    expect(keys(0)).toEqual(["rich-message-a-0", "rich-message-c-0", "rich-tool-t1"]);
    expect(keys(1)).toEqual(["rich-message-b-0"]);
    // The tool call goes where its use's message does.
    expect(timeline.tasks[0].toolCalls.map((call) => call.id)).toEqual(["t1"]);
    expect(timeline.tasks[1].toolCalls).toEqual([]);
  });

  test("a message that comes again with another task moves there", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), user(3, "2026-01-01T00:01:00Z")];
    const message = {...rich("a", "2026-01-01T00:01:10Z", [{type: "tool_use", tool_use_id: "t1", tool_name: "Bash", tool_input: {command: "ls"}}]), task_id: 3};
    const builder = new TimelineBuilder();
    expect(builder.build(messages, [message]).timeline.tasks.map((task) => task.toolCalls.length)).toEqual([0, 1]);
    const moved = [{...message, task_id: 1}];
    const built = builder.build(messages, moved);
    expect(built).toEqual(buildTranscript(messages, moved));
    expect(built.timeline.tasks.map((task) => task.toolCalls.length)).toEqual([1, 0]);
  });

  test("tool calls that come into a task are in the order they started", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), user(2, "2026-01-01T00:01:00Z")];
    const use = (id: string, time: string, path: string) => rich(`${id}-${time}`, time, [{type: "tool_use", tool_use_id: id, tool_name: "Read", tool_input: {path}}]);
    // b started after a, but its time is in the second task.
    const richMessages = [use("a", "2026-01-01T00:00:10Z", "/a"), use("b", "2026-01-01T00:01:10Z", "/b")];
    const builder = new TimelineBuilder();
    builder.build(messages, richMessages);
    // a is sent again later, so it moves into the second task, before b.
    const later = [...richMessages, use("a", "2026-01-01T00:01:20Z", "/a2")];
    const built = builder.build(messages, later);
    expect(built).toEqual(buildTranscript(messages, later));
    expect(built.timeline.tasks[1].toolCalls.map((call) => call.id)).toEqual(["a", "b"]);
  });

  test("tool calls of tasks that become one are in the order they started", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), user(2, "2026-01-01T00:01:00Z"), user(3, "2026-01-01T00:02:00Z")];
    const use = (id: string, time: string, path: string) => rich(`${id}-${time}`, time, [{type: "tool_use", tool_use_id: id, tool_name: "Read", tool_input: {path}}]);
    // y starts first, x second; y is sent again in the last task.
    const richMessages = [use("y", "2026-01-01T00:00:10Z", "/y"), use("x", "2026-01-01T00:01:10Z", "/x"), use("y", "2026-01-01T00:02:10Z", "/y2")];
    const builder = new TimelineBuilder();
    expect(builder.build(messages, richMessages).timeline.tasks.map((task) => task.toolCalls.map((call) => call.id))).toEqual([[], ["x"], ["y"]]);
    // The last task's prompt goes (taken back), so its calls join the task before.
    const fewer = messages.slice(0, 2);
    const built = builder.build(fewer, richMessages);
    expect(built).toEqual(buildTranscript(fewer, richMessages));
    expect(built.timeline.tasks[1].toolCalls.map((call) => call.id)).toEqual(["y", "x"]);
  });

  test("with the server's tasks, a steered message stays in the task it joined", () => {
    const messages = [
      user(1, "2026-01-01T00:00:00Z"),
      agent(2, "working", "2026-01-01T00:00:10Z"),
      user(3, "2026-01-01T00:00:20Z"),
      agent(4, "done", "2026-01-01T00:00:40Z"),
      user(5, "2026-01-01T00:01:00Z"),
    ];
    const richMessages = [
      {...rich("a", "2026-01-01T00:00:30Z", [{type: "text", text: "after the steer"}]), task_id: 1},
      rich("b", "2026-01-01T00:00:35Z", [{type: "text", text: "untagged, by time"}]),
    ];
    const {timeline} = buildTranscript(messages, richMessages, new Set([3]));
    expect(timeline.tasks.map((t) => t.prompt.id)).toEqual([1, 5]);
    expect(timeline.tasks[0].steered.map((m) => m.id)).toEqual([3]);
    expect(timeline.tasks[0].responses.map((m) => m.id)).toEqual([2, 4]);
    expect(timeline.tasks[0].richActivity.map((item) => item.key)).toEqual(["rich-message-a-0", "rich-message-b-0"]);
    // A server that derives no tasks: every message of the user's is a task.
    const old = buildTranscript(messages, richMessages).timeline;
    expect(old.tasks.map((t) => t.prompt.id)).toEqual([1, 3, 5]);
    expect(old.tasks[1].richActivity.map((item) => item.key)).toEqual(["rich-message-b-0"]);
  });

  test("a message the server steers after it came moves into the task it joined", () => {
    const messages = [user(1, "2026-01-01T00:00:00Z"), agent(2, "working", "2026-01-01T00:00:10Z"), user(3, "2026-01-01T00:00:20Z"), agent(4, "more", "2026-01-01T00:00:30Z")];
    const builder = new TimelineBuilder();
    const before = builder.build(messages, [], new Set()).timeline;
    expect(before.tasks).toHaveLength(2);
    const after = builder.build(messages, [], new Set([3]));
    expect(after).toEqual(buildTranscript(messages, [], new Set([3])));
    expect(after.timeline.tasks).toHaveLength(1);
    // The same tasks again keep their objects.
    expect(builder.build(messages, [], new Set([3])).timeline).toBe(after.timeline);
  });

  test("a steered message before any task starts one", () => {
    const {timeline} = buildTranscript([agent(0, "Starting", "2026-01-01T00:00:00Z"), user(3, "2026-01-01T00:00:20Z")], [], new Set([3]));
    expect(timeline.tasks.map((t) => t.prompt.id)).toEqual([3]);
    expect(timeline.prelude.map((m) => m.id)).toEqual([0]);
  });

  test("a tool call keeps its object until its result changes", () => {
    const use = rich("m1", "2026-01-01T00:00:01Z", [{type: "tool_use", tool_use_id: "t1", tool_name: "Bash", tool_input: {command: "ls"}}]);
    const builder = new TimelineBuilder();
    const [before] = builder.build([], [use]).toolCalls;
    expect(builder.build([], [use]).toolCalls[0]).toBe(before);
    const result = rich("m2", "2026-01-01T00:00:02Z", [{type: "tool_result", tool_use_id: "t1", text: "a.go"}]);
    const [after] = builder.build([], [use, result]).toolCalls;
    expect(after).not.toBe(before);
    expect(after.result).toBe("a.go");
  });

  // A seeded random walk through what a session sends: output before the
  // first task, new tasks and drafts (some failing, and staying among the
  // tasks), replies that grow, tool calls and their results (some in a
  // later task), messages from the past, messages sent again unchanged, a
  // full replay and a reset.
  for (const seed of [1, 7, 42, 1234, 99991]) {
    test(`builds what a fresh build does, whatever changes (walk ${seed})`, () => {
      walk(seed, 300);
    });
  }
});

function walk(seed: number, steps: number) {
  const random = (n: number) => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return Math.floor((seed / 2147483648) * n);
  };
  const time = (second: number) => new Date(Date.UTC(2026, 0, 1) + second * 1000).toISOString();
  const replaceAt = <T,>(list: T[], index: number, item: T) => list.map((old, i) => (i === index ? item : old));
  let messages: (Message | DraftMessage)[] = [];
  let richMessages: RichMessage[] = [];
  let clock = 0;
  let nextId = 0;
  let calls = 0;
  const open: string[] = [];
  // The task the server says a rich message began in: mostly the latest,
  // now and then an earlier one or one the chat doesn't have, or none (a
  // resumed session's messages).
  const tagged = (message: RichMessage): RichMessage => {
    const ids = messages.flatMap((m) => (m.role === "user" && m.id !== undefined ? [m.id] : []));
    const pick = random(6);
    const taskId = pick < 3 ? ids.at(-1) : pick === 3 && ids.length > 0 ? ids[random(ids.length)] : pick === 4 ? 9999 : undefined;
    return taskId === undefined ? message : {...message, task_id: taskId};
  };
  // The user's messages the server steered into the task before.
  let steered = new Set<number>();
  const builder = new TimelineBuilder();
  let previous = builder.build(messages, richMessages, steered);
  for (let step = 0; step < steps; step++) {
    clock += 1 + random(5);
    switch (random(16)) {
      case 0:
        // A task the server confirmed; drafts still being sent go.
        messages = [...messages.filter((m) => m.id !== undefined || (m as DraftMessage).deliveryStatus === "failed"), user(nextId++, time(clock))];
        break;
      case 1:
        messages = [...messages, {role: "user", content: "draft", clientId: `c${step}`, deliveryStatus: random(4) === 0 ? "failed" : "sending"}];
        break;
      case 2: {
        const last = messages.findLastIndex((message) => message.role === "agent");
        if (last >= 0 && random(2) === 0) {
          messages = replaceAt(messages, last, {...messages[last], content: `${messages[last].content} more`});
        } else {
          messages = [...messages.filter((m) => m.id !== undefined || (m as DraftMessage).deliveryStatus === "failed"), agent(nextId++, "reply", time(clock))];
        }
        break;
      }
      case 3: {
        const id = `t${calls++}`;
        open.push(id);
        richMessages = [...richMessages, tagged(rich(`m${step}`, time(clock), [
          {type: "thinking", thinking: `step ${step}`},
          {type: "tool_use", tool_use_id: id, tool_name: "Bash", tool_input: {command: `run ${step}`}, status: "running"},
        ]))];
        break;
      }
      case 4: {
        const id = open.splice(random(open.length + 1), 1)[0];
        if (!id) break;
        richMessages = [...richMessages, tagged({message_id: `r${step}`, role: "user", timestamp: time(clock), content: [
          {type: "tool_result", tool_use_id: id, text: `result ${step}`, is_error: random(3) === 0},
        ]})];
        break;
      }
      case 5: {
        const last = richMessages.findLastIndex((message) => message.role === "assistant");
        if (last < 0) break;
        const message = richMessages[last];
        richMessages = replaceAt(richMessages, last, {...message, content: [...message.content, {type: "text", text: `streamed ${step}`}]});
        break;
      }
      case 6:
        // A message sent again unchanged, as a new object.
        if (richMessages.length > 0) {
          const index = random(richMessages.length);
          richMessages = replaceAt(richMessages, index, structuredClone(richMessages[index]));
        }
        if (messages.length > 0) {
          const index = random(messages.length);
          messages = replaceAt(messages, index, {...messages[index]});
        }
        break;
      case 7:
        // A full replay: everything again, as new objects.
        if (random(4) === 0) {
          messages = structuredClone(messages);
          richMessages = structuredClone(richMessages);
        }
        break;
      case 8:
        // An agent's message from the past, and one without content.
        richMessages = [...richMessages, tagged(rich(`p${step}`, time(random(clock)), [{type: "text", text: `late ${step}`}])), rich(`e${step}`, time(clock), [])];
        break;
      case 9:
        // A tool call that starts again (the agent sends its use again).
        if (calls > 0) {
          const id = `t${random(calls)}`;
          richMessages = [...richMessages, tagged(rich(`u${step}`, time(clock), [{type: "tool_use", tool_use_id: id, tool_name: "Read", tool_input: {path: `/f${step}`}, status: "completed"}]))];
        }
        break;
      case 10:
        // A reset now and then.
        if (random(25) === 0) {
          messages = [];
          richMessages = [];
          steered = new Set();
          open.length = 0;
        }
        break;
      case 11:
        // A rich message that changed in the middle.
        if (richMessages.length > 0) {
          const index = random(richMessages.length);
          const message = richMessages[index];
          richMessages = replaceAt(richMessages, index, {...message, content: [...message.content, {type: "thinking", thinking: `later ${step}`}]});
        }
        break;
      case 12:
        // An agent's message that comes before the task it belongs to.
        richMessages = [...richMessages, tagged(rich(`f${step}`, time(clock + 1 + random(8)), [{type: "text", text: `early ${step}`}]))];
        break;
      case 13:
        // A rich message that moves in time.
        if (richMessages.length > 0) {
          const index = random(richMessages.length);
          richMessages = replaceAt(richMessages, index, {...richMessages[index], timestamp: time(random(clock + 5))});
        }
        break;
      case 14:
        // A rich message sent again with another task (or none).
        if (richMessages.length > 0) {
          const index = random(richMessages.length);
          const message = {...richMessages[index]};
          delete message.task_id;
          richMessages = replaceAt(richMessages, index, tagged(message));
        }
        break;
      case 15: {
        // A message of the user's steered into the task before, or no
        // longer (its task went); a new set, as each task_update makes.
        const ids = messages.flatMap((m) => (m.role === "user" && m.id !== undefined ? [m.id] : []));
        if (ids.length === 0) break;
        const id = ids[random(ids.length)];
        steered = new Set(steered);
        if (!steered.delete(id)) steered.add(id);
        break;
      }
    }
    const built = builder.build(messages, richMessages, steered);
    expect(built).toEqual(buildTranscript(messages, richMessages, steered));
    // What didn't change keeps its object.
    const previousTasks = new Map(previous.timeline.tasks.map((task) => [task.key, task]));
    for (const task of built.timeline.tasks) {
      const old = previousTasks.get(task.key);
      if (old && old !== task) expect(old).not.toEqual(task);
    }
    const previousCalls = new Map(previous.toolCalls.map((call) => [call.id, call]));
    for (const call of built.toolCalls) {
      const old = previousCalls.get(call.id);
      if (old && old !== call) expect(old).not.toEqual(call);
    }
    previous = built;
  }
}
