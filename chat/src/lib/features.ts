// What the chat offers, decided in one place from the server's transport
// and what its agent can do. Components ask for a feature rather than test
// the transport themselves.

import type {Capabilities, Transport} from "./dispatch";
import type {AgentType} from "./agents";

// The agents whose earlier sessions AgentAPI reads from their session logs,
// and those of them that run subagents.
const SESSION_HISTORY_AGENTS: readonly AgentType[] = ["claude", "codex", "pi"];
const SUBAGENT_AGENTS: readonly AgentType[] = ["claude", "codex"];

export interface Features {
  // The agent's own terminal: TTY mode, terminal keys and the live screen
  // of a running task (PTY).
  agentTerminal: boolean;
  // A shell in the agent's working directory: the terminal panel and the
  // login card's "Run in terminal" (native, unless --terminal=false).
  shell: boolean;
  // The agent's requests (approvals, questions, plans) as decision cards,
  // and the login card (native).
  requests: boolean;
  // A task's activity comes from the agent's structured messages alone,
  // not its screen (native).
  structuredTranscript: boolean;
  // The chat's slash commands (native).
  slashCommands: boolean;
  // The mode, model and effort menus (native agents that report them).
  settings: boolean;
  // Adding a message to the running turn, and stopping it (native agents
  // that can).
  steer: boolean;
  interrupt: boolean;
  // The context and rate limits meter (native).
  usage: boolean;
  // A restart for an MCP change keeps the conversation: the agent resumes
  // its session (native).
  restartKeepsConversation: boolean;
  // The agent's earlier sessions, to open one or a copy of it (native
  // Claude Code, Codex and Pi).
  sessionHistory: boolean;
  // The subagents the agent ran, and what each did (native Claude Code and
  // Codex).
  subagents: boolean;
}

export function features(transport: Transport, capabilities: Capabilities | null, shellAvailable: boolean, agentType?: AgentType): Features {
  const native = transport === "native";
  return {
    agentTerminal: transport === "pty",
    shell: native && shellAvailable,
    requests: native,
    structuredTranscript: native,
    slashCommands: native,
    settings: native && Boolean(capabilities?.settings),
    steer: native && Boolean(capabilities?.steer),
    interrupt: native && Boolean(capabilities?.interrupt),
    usage: native,
    restartKeepsConversation: native,
    sessionHistory: native && agentType !== undefined && SESSION_HISTORY_AGENTS.includes(agentType),
    subagents: native && agentType !== undefined && SUBAGENT_AGENTS.includes(agentType),
  };
}
