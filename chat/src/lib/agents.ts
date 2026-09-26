export type SubAgentStatus = "running" | "completed" | "interrupted" | "failed";

// Mirrors jsonlwatcher.SubAgent (GET /agents, SSE agents_update).
export interface SubAgent {
  thread_id: string;
  parent_thread_id: string;
  path: string;
  nickname?: string;
  role?: string;
  depth: number;
  status: SubAgentStatus;
  activity?: string;
  last_message?: string;
  total_tokens?: number;
  started_at: string;
  updated_at: string;
}

export function countRunningAgents(agents: SubAgent[]): number {
  return agents.filter((agent) => agent.status === "running").length;
}

// Last segment of the agent path ("/root/research/worker" -> "worker").
export function agentName(agent: SubAgent): string {
  const segments = agent.path.split("/").filter(Boolean);
  return segments[segments.length - 1] ?? agent.thread_id;
}

// Running agents first, then most recently active.
export function sortAgentsForDisplay(agents: SubAgent[]): SubAgent[] {
  return [...agents].sort((a, b) => {
    const running = Number(b.status === "running") - Number(a.status === "running");
    if (running !== 0) return running;
    return Date.parse(b.updated_at) - Date.parse(a.updated_at);
  });
}

// Compact elapsed time, e.g. "45s", "3m 05s", "1h 02m".
export function formatElapsed(fromISO: string, toMs: number): string {
  const start = Date.parse(fromISO);
  if (Number.isNaN(start)) return "";
  const seconds = Math.max(0, Math.floor((toMs - start) / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${String(seconds % 60).padStart(2, "0")}s`;
  return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, "0")}m`;
}

export function formatTokens(tokens: number | undefined): string {
  if (!tokens) return "";
  if (tokens < 1000) return `${tokens} tokens`;
  return `${(tokens / 1000).toFixed(tokens < 10_000 ? 1 : 0)}k tokens`;
}
