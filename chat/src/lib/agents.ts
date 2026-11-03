// The agents AgentAPI runs, as servers report them, and their names.

export type AgentType = "claude" | "codex" | "pi" | "custom" | "unknown";

export type AgentColorDisplayNamePair = {
  displayName: string;
}

// AgentAPI 3 runs Claude Code, Codex and Pi; "custom" is native-demo.
export const AgentType: Record<Exclude<AgentType, "unknown">, AgentColorDisplayNamePair> = {
  claude: {displayName: "Claude Code"},
  codex: {displayName: "Codex"},
  pi: {displayName: "Pi"},
  custom: {displayName: "Custom"},
}

// parseAgentType is the agent type a server reports: "unknown" for none,
// and "custom" for one this build of the chat doesn't know (a newer
// server), so looking up its name can't fail.
export function parseAgentType(value: string | undefined): AgentType {
  if (!value) return "unknown";
  return Object.hasOwn(AgentType, value) ? (value as AgentType) : "custom";
}

// updatesOnRestart reports whether restarting the agent updates it to its
// latest release first, as AgentAPI does for the Codex and Pi CLIs (`codex
// update`, `pi update --self`); Claude Code updates itself.
export function updatesOnRestart(type: AgentType): boolean {
  return type === "codex" || type === "pi";
}

// agentDisplayName names an agent type, or says fallback for "unknown".
export function agentDisplayName(type: AgentType, fallback: string): string {
  return type === "unknown" ? fallback : (AgentType[type]?.displayName ?? fallback);
}
