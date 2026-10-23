import {describe, expect, test} from "bun:test";
import {computeTokenTotals, formatTokenCount, getAgentState, getStatusMeta, taskProgress, TokenCounter, waitingFor} from "./session-status";
import type {RichMessage} from "./conversation";
import {buildTranscript} from "./task-timeline";

const connected = {connectionStatus: "connected"} as const;

describe("getAgentState", () => {
  test("follows the activity status", () => {
    expect(getAgentState({...connected, serverStatus: "running"})).toBe("working");
    expect(getAgentState({...connected, serverStatus: "stable"})).toBe("ready");
    expect(getAgentState({...connected, serverStatus: "unknown"})).toBe("connecting");
  });

  test("an open terminal prompt needs the user, even while stable", () => {
    expect(getAgentState({...connected, serverStatus: "stable", terminalPrompt: "Do you want to proceed?\n❯ 1. Yes"})).toBe("needs-you");
  });

  test("a request the agent waits on (native transport) needs the user", () => {
    expect(getAgentState({...connected, serverStatus: "stable", pendingRequestCount: 1})).toBe("needs-you");
  });

  test("a login the agent waits for needs the user, like a request", () => {
    // While it waits at startup the agent reports running (starting).
    expect(getAgentState({...connected, serverStatus: "running", loginRequired: true})).toBe("needs-you");
    expect(getAgentState({...connected, serverStatus: "stable", loginRequired: true})).toBe("needs-you");
  });

  test("the connection still comes first", () => {
    expect(getAgentState({serverStatus: "running", connectionStatus: "reconnecting", loginRequired: true})).toBe("reconnecting");
    expect(getAgentState({serverStatus: "running", connectionStatus: "offline", pendingRequestCount: 1})).toBe("offline");
    expect(getAgentState({serverStatus: "offline", connectionStatus: "connected"})).toBe("offline");
  });
});

describe("getStatusMeta", () => {
  test("says what the agent waits for", () => {
    expect(getStatusMeta("needs-you", true).detail).toBe("Agent is waiting for you to log in");
    expect(getStatusMeta("needs-you").detail).toBe("Agent is waiting for your answer");
    expect(getStatusMeta("working", true)).toMatchObject({kind: "working", label: "Working", detail: "Agent is working"});
  });
});

describe("formatTokenCount", () => {
  test("rounds to the unit people read", () => {
    expect(formatTokenCount(950)).toBe("950");
    expect(formatTokenCount(4_210)).toBe("4.2k");
    expect(formatTokenCount(9_960)).toBe("10k");
    expect(formatTokenCount(41_499)).toBe("41k");
    expect(formatTokenCount(999_600)).toBe("1.0M");
    expect(formatTokenCount(1_250_000)).toBe("1.3M");
  });
});

describe("what the agent waits for", () => {
  test("a login comes before an answer, and only while it needs you", () => {
    expect(waitingFor("needs-you", true)).toBe("login");
    expect(waitingFor("needs-you", false)).toBe("answer");
    expect(waitingFor("working", true)).toBeNull();
  });
});

describe("token totals", () => {
  const message = (id: string, input: number, output: number): RichMessage => ({
    message_id: id,
    role: "assistant",
    timestamp: "2026-10-22T10:00:00Z",
    content: [],
    usage: {input_tokens: input, output_tokens: output, cache_read_input_tokens: 0, cache_creation_input_tokens: 0},
  });

  test("count a message ID once", () => {
    expect(computeTokenTotals([message("a", 10, 5), message("a", 10, 5), message("b", 1, 1)])).toEqual({input: 11, output: 6, cacheRead: 0, cacheCreation: 0, total: 17});
  });

  test("counted as messages stream are what counting them all gives", () => {
    const counter = new TokenCounter();
    let messages: RichMessage[] = [];
    let seed = 3;
    const random = (n: number) => {
      seed = (seed * 1103515245 + 12345) % 2147483648;
      return Math.floor((seed / 2147483648) * n);
    };
    for (let step = 0; step < 300; step++) {
      const id = `m${random(40)}`;
      switch (random(4)) {
        case 0:
        case 1:
          messages = [...messages, message(id, random(100), random(100))];
          break;
        case 2:
          // The last message grows: the same object no more.
          if (messages.length > 0) messages = [...messages.slice(0, -1), message(messages.at(-1)!.message_id, random(100), random(100))];
          break;
        case 3:
          // A message in the middle changes, or goes.
          if (messages.length > 0) {
            const at = random(messages.length);
            messages = random(2) ? messages.map((m, i) => (i === at ? message(id, 1, 2) : m)) : messages.filter((_, i) => i !== at);
          }
          break;
      }
      const before = counter.count(messages);
      expect(before).toEqual(computeTokenTotals(messages));
      // Nothing new keeps the totals' object.
      expect(counter.count(messages)).toBe(before);
    }
  });
});

describe("taskProgress", () => {
  const use = (id: string, time: string, name: string, input: unknown): RichMessage =>
    ({message_id: id, role: "assistant", timestamp: time, content: [{type: "tool_use", tool_use_id: id, tool_name: name, tool_input: input}]});
  const prompt = {id: 0, role: "user", content: "fix it", time: "2026-10-22T10:00:00Z"};

  test("says the tool call the agent waits on, else what it thinks", () => {
    const thinking: RichMessage = {message_id: "th", role: "assistant", timestamp: "2026-10-22T10:00:01Z", content: [{type: "thinking", thinking: "**Reading the parser**\n\nIt drops a token."}]};
    const read = use("t1", "2026-10-22T10:00:02Z", "Read", {path: "/parser.go"});
    const done: RichMessage = {message_id: "r1", role: "user", timestamp: "2026-10-22T10:00:03Z", content: [{type: "tool_result", tool_use_id: "t1", text: "package parser"}]};
    const test = use("t2", "2026-10-22T10:00:04Z", "Bash", {command: "go test ./...\n"});
    const progress = (rich: RichMessage[]) => taskProgress(buildTranscript([prompt], rich).timeline.tasks[0]);
    expect(progress([thinking, read, done, test])).toEqual({activity: "$ go test ./...", latestTool: "Bash"});
    expect(progress([thinking, read, done])).toEqual({activity: "Thinking · Reading the parser", latestTool: "Read"});
    expect(progress([thinking, read])).toEqual({activity: "Read", latestTool: "Read"});
    expect(taskProgress(undefined)).toEqual({activity: "", latestTool: null});
  });
});
