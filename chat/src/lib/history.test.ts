import {afterEach, describe, expect, test} from "bun:test";
import {createChatAPI} from "./chat-api";
import {resetAuthState} from "./auth";
import {conversationReducer, initialConversation, type ConversationState} from "./conversation-state";
import {earlierTasks, prependPage, type HistoryPage} from "./history";
import type {RichMessage} from "./conversation";
import type {ServerTask} from "./server-tasks";
import {json, mockFetch, restoreFetch, routeFetch} from "@/test/fetch";

afterEach(() => {
  restoreFetch();
  resetAuthState();
});

const task = (id: number, number?: number): ServerTask => ({
  id,
  number,
  status: "completed",
  prompt: `task ${id}`,
  started_at: "2026-11-10T00:00:00Z",
  errors: [],
  steered: [],
});
const rich = (id: string, taskId: number): RichMessage => ({message_id: id, role: "assistant", content: [{type: "text", text: id}], timestamp: "2026-11-10T00:00:00Z", task_id: taskId});

// The state the full replay leaves: tasks 21 and 22 (ids 40, 42) of 22.
const recent: ConversationState = {
  ...initialConversation,
  messages: [
    {id: 40, role: "user", content: "task 40"},
    {id: 41, role: "agent", content: "reply"},
    {id: 42, role: "user", content: "task 42"},
  ],
  richMessages: [rich("r40", 40), rich("r42", 42)],
  tasks: [task(40, 21), task(42, 22)],
  synced: {epoch: "e", seq: 99},
};

const page: HistoryPage = {
  before: 40,
  tasks: [task(36, 19), task(38, 20)],
  messages: [
    {id: 36, role: "user", content: "task 36"},
    {id: 37, role: "agent", content: "reply"},
    {id: 38, role: "user", content: "task 38"},
    {id: 39, role: "agent", content: "reply"},
  ],
  richMessages: [rich("r36", 36), rich("r38", 38)],
  hasMore: true,
};

describe("earlierTasks", () => {
  test("counts the tasks before the first held", () => {
    expect(earlierTasks(recent.tasks)).toBe(20);
    expect(earlierTasks([task(0, 1)])).toBe(0);
    expect(earlierTasks([])).toBe(0);
  });

  test("is none for a server without task numbers", () => {
    expect(earlierTasks([task(40)])).toBe(0);
  });
});

describe("prependPage", () => {
  test("puts the page before what the state holds", () => {
    const next = conversationReducer(recent, {type: "earlierLoaded", page});
    expect(next.tasks.map((t) => t.id)).toEqual([36, 38, 40, 42]);
    expect(next.messages.map((m) => m.id)).toEqual([36, 37, 38, 39, 40, 41, 42]);
    expect(next.richMessages.map((m) => m.message_id)).toEqual(["r36", "r38", "r40", "r42"]);
    expect(next.synced).toBe(recent.synced);
    expect(earlierTasks(next.tasks)).toBe(18);
  });

  test("drops a page that doesn't end where the state starts", () => {
    expect(prependPage(recent, {...page, before: 38})).toBe(recent);
    const once = prependPage(recent, page);
    expect(prependPage(once, page)).toBe(once);
  });

  test("keeps the drafts, after the server's messages", () => {
    const draft = {role: "user" as const, content: "next", clientId: "c1", deliveryStatus: "sending" as const};
    const next = prependPage({...recent, messages: [...recent.messages, draft]}, page);
    expect(next.messages.at(-1)).toBe(draft);
  });
});

describe("getHistoryPage", () => {
  test("reads the tasks, messages and rich messages before a task", async () => {
    const calls = routeFetch({
      "/tasks": () => json({tasks: [{id: 36, number: 19, status: "completed", prompt: "task 36", started_at: "2026-11-10T00:00:00Z"}], has_more: true, next_before: 36}),
      "/messages": () => json({messages: [{id: 36, role: "user", content: "task 36", time: "2026-11-10T00:00:00Z"}], has_more: true}),
      "/rich-messages": () => json({messages: [{message_id: "r36", role: "assistant", content: null, timestamp: "2026-11-10T00:00:00Z", task_id: 36}], has_more: true}),
    });
    const result = await createChatAPI("http://agentapi.test").getHistoryPage(40, 20);
    expect(calls.map((call) => call.url).sort()).toEqual([
      "http://agentapi.test/messages?before=40&limit=20",
      "http://agentapi.test/rich-messages?before=40&limit=20",
      "http://agentapi.test/tasks?before=40&limit=20",
    ]);
    expect(result.before).toBe(40);
    expect(result.hasMore).toBe(true);
    expect(result.tasks[0]).toMatchObject({id: 36, number: 19, errors: [], steered: []});
    expect(result.richMessages[0].content).toEqual([]);
  });
});

describe("exportTimeline", () => {
  test("streams the server's JSON Lines", async () => {
    const calls = mockFetch(new Response('{"id":1}\n{"id":2}\n'));
    expect(await createChatAPI("http://agentapi.test").exportTimeline()).toBe('{"id":1}\n{"id":2}\n');
    expect(calls[0].url).toBe("http://agentapi.test/timeline/export?subagents=true");
  });

  test("reads GET /timeline from a server without the export", async () => {
    mockFetch(new Response("Not Found", {status: 404}), json({events: [{id: 1}, {id: 2}]}));
    expect(await createChatAPI("http://agentapi.test").exportTimeline()).toBe('{"id":1}\n{"id":2}\n');
  });
});
