// The agent's status as status_change events report it, kept as one value
// so that an event that changes nothing renders nothing.

import type {AgentSettings, AgentUsage, LimitStop} from "./chat-api";
import type {Capabilities} from "./dispatch";
import {parseAgentType, type AgentType} from "./agents";
import {parseLoginRequired, type LoginRequired} from "./login";

export type ServerStatus = "stable" | "running" | "offline" | "unknown";

// A status_change event as the server sends it.
export interface StatusChangeEvent {
  status: string;
  lifecycle?: string;
  agent_type: string;
  // Missing from servers that predate it; GET /status says then.
  transport?: string;
  agentapi_version?: string;
  terminal_columns?: number;
  session_name?: string;
  terminal_prompt?: string;
  // Native transport only. The chat counts the pending requests itself,
  // from the requests it holds (pending_requests is ignored).
  capabilities?: Capabilities;
  login_required?: unknown;
  // Native agents with the settings capability.
  settings?: AgentSettings;
  // Native agents, once they report it.
  usage?: AgentUsage;
  // Set while a turn a usage limit stopped holds the queue.
  limit_stop?: LimitStop;
  // A shell terminal is available (native transport).
  terminal?: boolean;
}

export interface AgentStatus {
  serverStatus: ServerStatus;
  agentType: AgentType;
  agentapiVersion: string;
  terminalColumns: number;
  sessionName: string;
  // Bottom of the terminal while the agent shows an interactive prompt.
  terminalPrompt: string;
  capabilities: Capabilities | null;
  settings: AgentSettings | null;
  usage: AgentUsage | null;
  limitStop: LimitStop | null;
  terminalAvailable: boolean;
  loginRequired: LoginRequired | null;
  lifecycle: string;
}

export const initialAgentStatus: AgentStatus = {
  serverStatus: "unknown",
  agentType: "custom",
  agentapiVersion: "",
  terminalColumns: 0,
  sessionName: "",
  terminalPrompt: "",
  capabilities: null,
  settings: null,
  usage: null,
  limitStop: null,
  terminalAvailable: false,
  loginRequired: null,
  lifecycle: "",
};

// applyStatusChange returns the status after event. A part the event
// doesn't change keeps its object, and an event that changes nothing
// returns status itself.
export function applyStatusChange(status: AgentStatus, event: StatusChangeEvent): AgentStatus {
  return update(status, {
    serverStatus: event.status === "stable" || event.status === "running" ? event.status : "unknown",
    agentType: parseAgentType(event.agent_type),
    agentapiVersion: event.agentapi_version ?? "",
    terminalColumns: event.terminal_columns ?? 0,
    sessionName: event.session_name ?? "",
    terminalPrompt: event.terminal_prompt ?? "",
    capabilities: event.capabilities ?? null,
    settings: event.settings ?? null,
    usage: event.usage ?? null,
    limitStop: event.limit_stop ?? null,
    terminalAvailable: event.terminal ?? false,
    loginRequired: parseLoginRequired(event.login_required),
    lifecycle: event.lifecycle ?? "",
  });
}

// update returns status with changes; parts equal to what status has keep
// their objects, and status itself comes back when nothing differs.
export function update(status: AgentStatus, changes: Partial<AgentStatus>): AgentStatus {
  let next: AgentStatus | null = null;
  for (const key of Object.keys(changes) as (keyof AgentStatus)[]) {
    if (sameValue(status[key], changes[key])) continue;
    next ??= {...status};
    (next as Record<keyof AgentStatus, unknown>)[key] = changes[key];
  }
  return next ?? status;
}

// sameValue reports whether a and b are the same JSON value.
export function sameValue(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((item, i) => sameValue(item, b[i]));
  }
  const left = a as Record<string, unknown>;
  const right = b as Record<string, unknown>;
  const keys = Object.keys(left).filter((key) => left[key] !== undefined);
  return (
    keys.length === Object.keys(right).filter((key) => right[key] !== undefined).length &&
    keys.every((key) => sameValue(left[key], right[key]))
  );
}
