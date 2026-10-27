import {describe, expect, test} from "bun:test";
import {ChatStream, EVENT_FLUSH_MS, REPLAY_FLUSH_MAX_MS, STALE_CONNECTION_MS, type ChatStreamHandlers} from "./chat-stream";
import {QUIET_RECONNECT_MS, type ConnectionState} from "./event-source";
import {fakeEnvironment} from "./fake-event-source";
import type {EventBatch} from "./pending-events";
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
    transport: (transport) => calls.push(`transport ${transport}`),
    askTransport: () => calls.push("ask transport"),
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
    expect(calls).toEqual(["opened", "status stable", "transport native"]);
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

  test("a status without the transport asks for it once per connection", () => {
    const {env, calls} = stream();
    const source = env.latest();
    source.open();
    source.emit("status_change", {status: "stable", agent_type: "claude"});
    source.emit("status_change", {status: "running", agent_type: "claude"});
    expect(calls.filter((call) => call === "ask transport")).toHaveLength(1);
    source.fail();
    env.latest().open();
    env.latest().emit("status_change", {status: "stable", agent_type: "claude"});
    expect(calls.filter((call) => call === "ask transport")).toHaveLength(2);
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
