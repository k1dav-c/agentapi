// What the chat offers, decided in one place from what the agent can do.
// Components ask for a feature rather than test the agent themselves.

import type {Capabilities} from "./dispatch";
import type {AgentType} from "./agents";

// The agents whose earlier sessions AgentAPI reads from their session logs,
// and those of them that run subagents.
const SESSION_HISTORY_AGENTS: readonly AgentType[] = ["claude", "codex", "pi"];
const SUBAGENT_AGENTS: readonly AgentType[] = ["claude", "codex"];

export interface Features {
  // A shell in the agent's working directory: the terminal panel and the
  // login card's "Run in terminal" (unless --terminal=false).
  shell: boolean;
  // The mode, model and effort menus (agents that report them).
  settings: boolean;
  // Adding a message to the running turn, and stopping it (agents that
  // can).
  steer: boolean;
  interrupt: boolean;
  // The agent's earlier sessions, to open one or a copy of it (Claude Code,
  // Codex and Pi).
  sessionHistory: boolean;
  // The subagents the agent ran, and what each did (Claude Code and Codex).
  subagents: boolean;
  // What sessions and tasks spent, from the session logs (Claude Code,
  // Codex and Pi).
  cost: boolean;
}

export function features(capabilities: Capabilities | null, shellAvailable: boolean, agentType?: AgentType): Features {
  const logs = agentType !== undefined && SESSION_HISTORY_AGENTS.includes(agentType);
  return {
    shell: shellAvailable,
    settings: Boolean(capabilities?.settings),
    steer: Boolean(capabilities?.steer),
    interrupt: Boolean(capabilities?.interrupt),
    sessionHistory: logs,
    subagents: agentType !== undefined && SUBAGENT_AGENTS.includes(agentType),
    cost: logs,
  };
}
