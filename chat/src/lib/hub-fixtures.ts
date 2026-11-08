// What the hub sends, for the tests: each builder fills in every field the
// types require, as the hub and the AgentAPIs send them, so a test names
// only what it is about and a field the types add fails here, once.

import type {HubInstance, InstanceUpdate, Summary} from "./hub";

// summary is an AgentAPI's summary: ready, idle, no task yet.
export function summary(overrides: Partial<Summary> = {}): Summary {
  return {
    instance: {id: "x", server_id: "s", agent_type: "claude", transport: "native"},
    status: "stable",
    lifecycle: "ready",
    attention: "none",
    title: "",
    pending_requests: 0,
    queued_messages: 0,
    tasks: 0,
    unread: false,
    seen_at: "2026-11-01T00:00:00Z",
    ...overrides,
  };
}

// update is a summary's update: allowed, none under way.
export function update(overrides: Partial<InstanceUpdate> = {}): InstanceUpdate {
  return {allowed: true, state: "idle", keeps_conversation: true, keeps_session: true, ...overrides};
}

// hubInstance is an instance as the hub sends it: connected and idle, with
// no summary yet; its agent, run and version unknown ("" as the hub sends
// them).
export function hubInstance(id: string, overrides: Partial<HubInstance> = {}): HubInstance {
  return {
    id,
    url: `http://${id}`,
    server_id: "",
    agent_type: "",
    transport: "native",
    source: "webhook",
    last_seen: "2026-11-01T00:00:00Z",
    connection: "connected",
    state: "idle",
    ...overrides,
  };
}

// idleInstance is an instance whose AgentAPI the hub follows, idle: with
// its summary.
export function idleInstance(id: string, overrides: Partial<HubInstance> = {}): HubInstance {
  return hubInstance(id, {summary: summary(), ...overrides});
}
