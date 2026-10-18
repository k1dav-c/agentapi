// The agents AgentAPI can run, as servers report them, and their names.

export type AgentType = "claude" | "goose" | "aider" | "gemini" | "amp" | "codex" | "cursor" | "cursor-agent" | "copilot" | "auggie" | "amazonq" | "opencode" | "kimi" | "pi" | "custom" | "unknown";

export type AgentColorDisplayNamePair = {
  displayName: string;
}

export const AgentType: Record<Exclude<AgentType, "unknown">, AgentColorDisplayNamePair> = {
  claude: {displayName: "Claude Code"},
  goose: {displayName: "Goose"},
  aider: {displayName: "Aider"},
  gemini: { displayName: "Gemini"},
  amp: {displayName: "Amp"},
  codex: {displayName: "Codex"},
  cursor: { displayName: "Cursor Agent"},
  "cursor-agent": { displayName: "Cursor Agent"},
  copilot: {displayName: "Copilot"},
  auggie: {displayName: "Auggie"},
  amazonq: {displayName: "Amazon Q"},
  opencode: {displayName: "Opencode"},
  kimi: {displayName: "Kimi Code"},
  pi: {displayName: "Pi"},
  custom: { displayName: "Custom"}
}

// parseAgentType is the agent type a server reports: "unknown" for none,
// and "custom" for one this build of the chat doesn't know (a newer
// server), so looking up its name can't fail.
export function parseAgentType(value: string | undefined): AgentType {
  if (!value) return "unknown";
  return Object.hasOwn(AgentType, value) ? (value as AgentType) : "custom";
}

// agentDisplayName names an agent type, or says fallback for "unknown".
export function agentDisplayName(type: AgentType, fallback: string): string {
  return type === "unknown" ? fallback : (AgentType[type]?.displayName ?? fallback);
}
