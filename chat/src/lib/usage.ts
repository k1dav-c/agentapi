import type { ContextUsage, RateLimit } from "./chat-api";

// How much of the context window is used, 0 to 100.
export function contextPercent(context: ContextUsage): number {
  if (context.size <= 0) return 0;
  return Math.min(100, Math.max(0, (context.used / context.size) * 100));
}

// A token count the way people read it: 950, 41k, 1.2M.
export function formatTokens(tokens: number): string {
  if (tokens < 1000) return String(tokens);
  if (tokens < 1_000_000) return `${Math.round(tokens / 1000)}k`;
  return `${(tokens / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
}

export type UsageTone = "ok" | "warning" | "danger";

// How worrying a share of a limit is: from 80% on a warning, at 95% or a
// limit the agent says it reached a danger.
export function usageTone(percent: number | undefined, status?: string): UsageTone {
  if (status === "rejected" || (percent ?? 0) >= 95) return "danger";
  if (status === "allowed_warning" || (percent ?? 0) >= 80) return "warning";
  return "ok";
}

// When a limit resets, relative to now: "in 2h 13m", "in 3d 4h".
export function formatResetIn(resetsAt: string | undefined, now: number): string {
  if (!resetsAt) return "";
  const ms = Date.parse(resetsAt) - now;
  if (!Number.isFinite(ms)) return "";
  if (ms <= 0) return "resetting";
  const minutes = Math.ceil(ms / 60_000);
  if (minutes < 60) return `in ${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `in ${hours}h ${minutes % 60}m`;
  return `in ${Math.floor(hours / 24)}d ${hours % 24}h`;
}

// The limit to show on the meter when there is no context usage: the
// fullest one.
export function fullestLimit(limits: RateLimit[] | undefined): RateLimit | undefined {
  let fullest: RateLimit | undefined;
  for (const limit of limits ?? []) {
    if (!fullest || (limit.used_percent ?? -1) > (fullest.used_percent ?? -1)) fullest = limit;
  }
  return fullest;
}
