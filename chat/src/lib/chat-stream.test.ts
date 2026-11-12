import {describe, expect, test} from "bun:test";
import {ChatStream, EVENT_FLUSH_MS, REPLAY_FLUSH_MAX_MS, STALE_CONNECTION_MS, type ChatStreamHandlers} from "./chat-stream";
import {QUIET_RECONNECT_MS, type ConnectionState} from "./event-source";
import {fakeEnvironment} from "@/test/fake-event-source";
import type {NativeRequest} from "./native-requests";
import {conversationReducer, initialConversation, requestsAfter, tasksAfter, type ConversationState} from "./conversation-state";
import type {EventBatch} from "./pending-events";
import type {ServerTask} from "./server-tasks";
import {SyncTracker} from "./session-sync";

const BASE = "http://agentapi.test";

function stream(sync = new SyncTracker()) {
  const env = fakeEnvironment();
  const batches: EventBatch[] = [];
  const calls: string[] = [];
  const states: ConnectionState[] = [];
  const handlers: ChatStreamHandlers = {
    apply: (batch) => batches.push(batch),
    replayed: () => calls.push("replayed"),
    statusChange: (event) => calls.push(`status ${event.status}`),
    agentError: (event) => calls.push(`error ${event.message}`),
    opened: () => calls.push("opened"),
    failed: () => calls.push("failed"),
    connection: (state) => states.push(state),
  };
  const chat = new ChatStream(BASE, sync, handlers, {env});
  chat.start();
  return {env, sync, chat, batches, calls, states};
}

const message = (id: number, seq: number) => ({id, role: id % 2 ? "agent" : "user", message: `message ${id}`, time: "2026-10-27T10:00:00Z", seq});

describe("ChatStream", () => {
  test("a first connection gets the full state, applied at once when the replay is complete", () => {
    const {env, sync, batches, calls} = stream();
    const source = env.latest();
    expect(source.url).toBe(`${BASE}/events?sync=1`);
    source.open();
    expect(calls).toEqual(["opened"]);
    source.emit("session_sync", {epoch: "e1", seq: 3});
    source.emit("message_update", message(0, 1));
    source.emit("message_update", message(1, 2));
    // Held while the replay is on its way.
    env.clock.advance(REPLAY_FLUSH_MAX_MS - 1);
    expect(batches).toHaveLength(0);
    // The replay's last event carries an SSE id.
    source.emit("status_change", {status: "stable", agent_type: "claude", transport: "native", seq: 3}, "3");
    expect(calls).toEqual(["opened", "status stable"]);
    env.clock.advance(0);
    expect(batches).toHaveLength(1);
    expect(batches[0]).toMatchObject({replace: true, point: {epoch: "e1", seq: 3}});
    expect(batches[0].messages.map((m) => m.id)).toEqual([0, 1]);
    expect(sync.watermark).toBe(3);
    // A full replay isn't a resumed one.
    expect(calls).not.toContain("replayed");
  });

  test("a long replay still shows progress", () => {
    const {env, batches} = stream();
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "e1", seq: 9});
    source.emit("message_update", message(0, 1));
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    expect(batches).toHaveLength(1);
    expect(batches[0].replace).toBe(true);
  });

  test("a long full replay keeps the tasks the chat has until its own replace them", () => {
    // A cached conversation of another epoch: its first task has message 2
    // steered into it.
    const sync = new SyncTracker();
    sync.restore("old", 40);
    const {env, batches} = stream(sync);
    const cached: ServerTask = {id: 0, status: "completed", prompt: "message 0", started_at: "2026-10-27T10:00:00Z", errors: [], steered: [2], seq: 30};
    let tasks = [cached];
    const apply = () => {
      for (const batch of batches.splice(0)) tasks = tasksAfter(tasks, batch);
    };
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "new", full: true, seq: 9});
    source.emit("message_update", message(0, 1));
    source.emit("message_update", message(1, 2));
    source.emit("message_update", message(2, 3));
    // The replay shows its progress before the tasks, which come last.
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    expect(batches).toHaveLength(1);
    expect(batches[0].replace).toBe(true);
    apply();
    expect(tasks).toEqual([cached]);
    source.emit("task_update", {id: 0, status: "completed", prompt: "message 0", started_at: "2026-10-27T10:00:00Z", steered: [2], seq: 8});
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    apply();
    expect(tasks).toEqual([cached]);
    // The replay's last event: its tasks replace the chat's.
    source.emit("task_update", {id: 4, status: "running", prompt: "message 4", started_at: "2026-10-27T10:00:00Z", seq: 9}, "9");
    env.clock.advance(0);
    apply();
    expect(tasks.map((task) => [task.id, task.seq])).toEqual([[0, 8], [4, 9]]);
  });

  test("a long full replay keeps the decision cards until its own requests replace them", () => {
    // The chat waits on an approval of this conversation; the cache also
    // holds one of a conversation from before it.
    const sync = new SyncTracker();
    sync.restore("old", 40);
    const {env, batches} = stream(sync);
    const approval = (id: string, createdAt: string, seq: number) => ({
      id,
      kind: "approval" as const,
      title: `Run ${id}`,
      status: "pending" as const,
      created_at: createdAt,
      response_capability: "live" as const,
      seq,
    });
    const current: NativeRequest = approval("r1", "2026-10-27T10:00:05Z", 30);
    let requests: NativeRequest[] = [approval("r0", "2026-10-26T09:00:00Z", 20), current];
    const apply = () => {
      for (const batch of batches.splice(0)) requests = requestsAfter(requests, batch);
    };
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "new", full: true, seq: 9});
    source.emit("message_update", message(0, 1));
    source.emit("message_update", message(1, 2));
    // The replay shows its progress before the requests, which come after
    // the rich messages.
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    expect(batches).toHaveLength(1);
    expect(batches[0].replace).toBe(true);
    apply();
    expect(requests).toEqual([current]);
    source.emit("request_update", approval("r1", "2026-10-27T10:00:05Z", 8));
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    apply();
    expect(requests).toEqual([current]);
    // The replay's last event: its requests replace the chat's.
    source.emit("request_update", approval("r2", "2026-10-27T10:00:07Z", 9), "9");
    env.clock.advance(0);
    apply();
    expect(requests.map((request) => [request.id, request.seq])).toEqual([["r1", 8], ["r2", 9]]);
  });

  test("a full replay that comes the newest task first shows each task whole, in the conversation's order", () => {
    // Tasks 0, 2 and 4, each a prompt, a reply and its rich messages: the
    // server sends task 4 first, then 2, then 0.
    const {env, batches} = stream();
    let state: ConversationState = initialConversation;
    const apply = () => {
      for (const batch of batches.splice(0)) state = conversationReducer(state, {type: "batch", batch});
    };
    const richEvent = (id: string, task: number, seq: number) => ({message_id: id, role: "assistant", timestamp: "2026-10-27T10:00:01Z", content: [{type: "text", text: id}], task_id: task, seq});
    const shown = () => ({
      messages: state.messages.map((m) => m.id),
      rich: state.richMessages.map((m) => m.message_id),
      replaying: state.replaying,
    });
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "e1", full: true, seq: 20});
    source.emit("status_change", {status: "stable", agent_type: "claude", transport: "native", seq: 0});
    source.emit("message_update", message(4, 5));
    source.emit("message_update", message(5, 6));
    source.emit("rich_message_update", richEvent("r4a", 4, 11));
    source.emit("rich_message_update", richEvent("r4b", 4, 12));
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    apply();
    // The first progress already shows the newest task with its reply.
    expect(shown()).toEqual({messages: [4, 5], rich: ["r4a", "r4b"], replaying: true});

    source.emit("message_update", message(2, 3));
    source.emit("message_update", message(3, 4));
    source.emit("rich_message_update", richEvent("r2", 2, 10));
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    apply();
    expect(shown()).toEqual({messages: [2, 3, 4, 5], rich: ["r2", "r4a", "r4b"], replaying: true});

    source.emit("message_update", message(0, 1));
    source.emit("message_update", message(1, 2));
    source.emit("rich_message_update", richEvent("r0", 0, 9));
    source.emit("task_update", {id: 0, status: "completed", prompt: "message 0", started_at: "2026-10-27T10:00:00Z", seq: 13});
    source.emit("task_update", {id: 2, status: "completed", prompt: "message 2", started_at: "2026-10-27T10:00:00Z", seq: 14});
    source.emit("task_update", {id: 4, status: "completed", prompt: "message 4", started_at: "2026-10-27T10:00:00Z", seq: 20}, "20");
    env.clock.advance(0);
    apply();
    expect(shown()).toEqual({messages: [0, 1, 2, 3, 4, 5], rich: ["r0", "r2", "r4a", "r4b"], replaying: false});
    expect(state.tasks.map((task) => task.id)).toEqual([0, 2, 4]);

    // Later updates merge as ever: a rich message keeps its place, a new
    // one goes last.
    source.emit("rich_message_update", {...richEvent("r2", 2, 21), content: [{type: "text", text: "r2, grown"}]});
    source.emit("message_update", message(6, 22));
    source.emit("rich_message_update", richEvent("r6", 6, 23));
    env.clock.advance(EVENT_FLUSH_MS);
    apply();
    expect(shown()).toEqual({messages: [0, 1, 2, 3, 4, 5, 6], rich: ["r0", "r2", "r4a", "r4b", "r6"], replaying: false});
    expect(state.richMessages[1].content).toEqual([{type: "text", text: "r2, grown"}]);
  });

  test("a full replay in the conversation's order applies as it comes", () => {
    const {env, batches} = stream();
    let state: ConversationState = initialConversation;
    const apply = () => {
      for (const batch of batches.splice(0)) {
        expect(batch.richOrder).toBeUndefined();
        state = conversationReducer(state, {type: "batch", batch});
      }
    };
    const richEvent = (id: string, seq: number) => ({message_id: id, role: "assistant", timestamp: "2026-10-27T10:00:01Z", content: [], seq});
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "e1", full: true, seq: 9});
    for (let id = 0; id < 4; id++) source.emit("message_update", message(id, id + 1));
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    apply();
    expect(state.messages.map((m) => m.id)).toEqual([0, 1, 2, 3]);
    source.emit("status_change", {status: "stable", agent_type: "claude", transport: "native", seq: 0});
    // An earlier server's rich messages come in the order it has them,
    // which the chat keeps.
    source.emit("rich_message_update", richEvent("b", 6));
    source.emit("rich_message_update", richEvent("a", 7));
    source.emit("rich_message_update", richEvent("c", 9), "9");
    env.clock.advance(0);
    apply();
    expect(state.messages.map((m) => m.id)).toEqual([0, 1, 2, 3]);
    expect(state.richMessages.map((m) => m.message_id)).toEqual(["b", "a", "c"]);
  });

  test("an older server's full replay, without tasks, clears them once it is complete", () => {
    const sync = new SyncTracker();
    sync.restore("old", 40);
    const {env, batches} = stream(sync);
    let tasks: ServerTask[] = [{id: 0, status: "completed", prompt: "message 0", started_at: "2026-10-27T10:00:00Z", errors: [], steered: [], seq: 30}];
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "new", full: true, seq: 2});
    source.emit("message_update", message(0, 1));
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    for (const batch of batches.splice(0)) tasks = tasksAfter(tasks, batch);
    expect(tasks).toHaveLength(1);
    source.emit("message_update", message(1, 2), "2");
    env.clock.advance(0);
    for (const batch of batches.splice(0)) tasks = tasksAfter(tasks, batch);
    expect(tasks).toEqual([]);
  });

  test("a full replay says it is still arriving until it completes, through a reconnect", () => {
    const {env, batches} = stream();
    let state: ConversationState = initialConversation;
    const apply = () => {
      for (const batch of batches.splice(0)) state = conversationReducer(state, {type: "batch", batch});
    };
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "e1", seq: 9});
    source.emit("message_update", message(0, 1));
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    apply();
    expect(state.replaying).toBe(true);
    // The stream fails before the replay is complete: the next connection
    // replays from the same point, and its last event completes it.
    source.fail();
    const next = env.latest();
    expect(next).not.toBe(source);
    expect(next.url).toBe(`${BASE}/events?sync=1&since=0&epoch=e1`);
    next.open();
    next.emit("session_sync", {epoch: "e1", seq: 9});
    next.emit("message_update", message(1, 2));
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    apply();
    expect(state.replaying).toBe(true);
    next.emit("task_update", {id: 0, status: "completed", prompt: "message 0", started_at: "2026-10-27T10:00:00Z", seq: 9}, "9");
    env.clock.advance(0);
    apply();
    expect(state.replaying).toBe(false);
    expect(state.messages.map((m) => m.id)).toEqual([0, 1]);
    expect(state.tasks.map((task) => task.id)).toEqual([0]);
  });

  test("a reconnect with nothing to replay ends a replay the failed stream left", () => {
    const {env, batches} = stream();
    let state: ConversationState = initialConversation;
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "e1", seq: 9});
    source.emit("message_update", message(0, 1));
    env.clock.advance(REPLAY_FLUSH_MAX_MS);
    source.fail();
    const next = env.latest();
    next.open();
    // The conversation started over in the same epoch: nothing to send.
    next.emit("session_sync", {epoch: "e1", seq: 0});
    env.clock.advance(EVENT_FLUSH_MS);
    for (const batch of batches) state = conversationReducer(state, {type: "batch", batch});
    expect(batches[0].held).toBe(true);
    expect(state.replaying).toBe(false);
  });

  test("a connection that resumes gets what changed, and says when it is applied", () => {
    const sync = new SyncTracker();
    sync.restore("e1", 5);
    const {env, batches, calls} = stream(sync);
    const source = env.latest();
    expect(source.url).toBe(`${BASE}/events?sync=1&since=5&epoch=e1`);
    source.open();
    source.emit("session_sync", {epoch: "e1", seq: 8});
    source.emit("message_update", message(6, 8), "8");
    env.clock.advance(0);
    expect(batches).toHaveLength(1);
    expect(batches[0]).toMatchObject({replace: false, point: {epoch: "e1", seq: 8}});
    expect(calls.at(-1)).toBe("replayed");
  });

  test("a connection that resumes with nothing new says so too", () => {
    const sync = new SyncTracker();
    sync.restore("e1", 5);
    const {env, batches, calls} = stream(sync);
    env.latest().open();
    env.latest().emit("session_sync", {epoch: "e1", seq: 5});
    env.clock.advance(EVENT_FLUSH_MS);
    expect(batches).toHaveLength(1);
    expect(calls.at(-1)).toBe("replayed");
  });

  test("a resumed replay that is only its session_sync is complete at once", () => {
    // The seq moved on, but nothing this client gets changed: the
    // session_sync is the replay's last event, with its id.
    const sync = new SyncTracker();
    sync.restore("e1", 5);
    const {env, batches, calls} = stream(sync);
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "e1", seq: 8}, "8");
    // A heartbeat keeps that id; it mustn't be what the replay waits for.
    source.emit("heartbeat", {time: "2026-10-27T10:00:00Z"}, "8");
    env.clock.advance(EVENT_FLUSH_MS);
    expect(sync.replaying).toBe(false);
    expect(sync.watermark).toBe(8);
    expect(batches.at(-1)?.point).toEqual({epoch: "e1", seq: 8});
    expect(calls.at(-1)).toBe("replayed");
  });

  test("an older server's replay, without session_sync, replaces the state", () => {
    const sync = new SyncTracker();
    sync.restore("e1", 5);
    const {env, batches} = stream(sync);
    const source = env.latest();
    source.open();
    source.emit("message_update", {id: 0, role: "user", message: "hi", time: "2026-10-27T10:00:00Z"});
    source.emit("message_update", {id: 1, role: "agent", message: "hello", time: "2026-10-27T10:00:01Z"});
    expect(sync.epoch).toBe("");
    env.clock.advance(EVENT_FLUSH_MS);
    expect(batches).toHaveLength(1);
    expect(batches[0].replace).toBe(true);
    expect(batches[0].messages).toHaveLength(2);
    // Each connection's replay is the full state again.
    source.fail();
    const next = env.latest();
    expect(next.url).toBe(`${BASE}/events?sync=1`);
    next.open();
    next.emit("status_change", {status: "stable", agent_type: "claude"});
    env.clock.advance(EVENT_FLUSH_MS);
    expect(batches.at(-1)?.replace).toBe(true);
  });

  test("events after the replay apply in batches; a request at once", () => {
    const {env, batches} = stream();
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "e1", seq: 0});
    env.clock.advance(EVENT_FLUSH_MS);
    batches.length = 0;
    source.emit("message_update", message(0, 1), "1");
    source.emit("message_update", message(1, 2), "2");
    expect(batches).toHaveLength(0);
    env.clock.advance(EVENT_FLUSH_MS);
    expect(batches).toHaveLength(1);
    expect(batches[0].messages).toHaveLength(2);
    source.emit("request_update", {id: "r1", kind: "approval", title: "Run make", status: "pending", created_at: "2026-10-27T10:00:00Z", response_capability: "live", seq: 3}, "3");
    expect(batches).toHaveLength(2);
    expect(batches[1].requests.map((r) => r.id)).toEqual(["r1"]);
  });

  test("a stream the server closes comes back at once, from where the chat is, without showing", () => {
    const {env, batches, calls, states} = stream();
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "e1", seq: 4});
    source.emit("message_update", message(0, 4), "4");
    env.clock.advance(0);
    expect(batches).toHaveLength(1);
    const shown = states.length;
    source.fail();
    expect(env.sources).toHaveLength(2);
    expect(env.latest().url).toBe(`${BASE}/events?sync=1&since=4&epoch=e1`);
    expect(states).toHaveLength(shown);
    expect(calls).not.toContain("failed");
    env.clock.advance(QUIET_RECONNECT_MS);
    expect(states.at(-1)?.status).toBe("reconnecting");
  });

  test("a stream that fails to open waits for the backoff and checks the session", () => {
    const {env, calls, states} = stream();
    env.latest().fail();
    expect(calls).toEqual(["failed"]);
    expect(states.at(-1)).toMatchObject({status: "reconnecting", attempt: 1});
    env.clock.advance(1200);
    expect(env.sources).toHaveLength(2);
  });

  test("the watchdog reconnects a foreground stream that went quiet", () => {
    const {env} = stream();
    const source = env.latest();
    source.open();
    env.clock.advance(15_000);
    source.emit("heartbeat", {time: "2026-10-27T10:00:15Z"});
    env.clock.advance(STALE_CONNECTION_MS);
    expect(env.sources).toHaveLength(1);
    env.clock.advance(10_000);
    expect(source.closed).toBe(true);
    expect(env.sources).toHaveLength(2);
  });

  test("agent errors are passed on, and their events end a replay like any other", () => {
    const {env, batches, calls} = stream();
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "e1", seq: 2});
    source.emit("agent_error", {level: "warning", message: "the model is busy", time: "2026-10-27T10:00:00Z"}, "2");
    expect(calls).toContain("error the model is busy");
    env.clock.advance(0);
    expect(batches).toHaveLength(1);
  });

  test("stop applies what was received", () => {
    const {env, chat, batches} = stream();
    const source = env.latest();
    source.open();
    source.emit("session_sync", {epoch: "e1", seq: 0});
    env.clock.advance(EVENT_FLUSH_MS);
    source.emit("message_update", message(0, 1), "1");
    chat.stop();
    expect(batches.at(-1)?.messages).toHaveLength(1);
    expect(source.closed).toBe(true);
  });
});
