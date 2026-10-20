import type { AgentUsage, ContextUsage, RateLimit } from "./chat-api";

// How much of the context window is used, 0 to 100.
export function contextPercent(context: ContextUsage): number {
  if (context.size <= 0) return 0;
  return Math.min(100, Math.max(0, (context.used / context.size) * 100));
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

// How much context makes a cold prompt cache worth compacting for: below
// it, reading the context again costs little.
export const COLD_CACHE_MIN_TOKENS = 50_000;

// A prompt cache that expired with a context worth compacting: the next
// message has the model provider read all of it again, at the full price
// and against the usage limits. expiredAt is when it expired.
export interface ColdCache {
  expiredAt: number;
  tokens: number;
}

// coldCache returns the agent's prompt cache if it expired by now and the
// context is large; null otherwise, or when the agent says nothing of its
// cache (Claude Code only does).
export function coldCache(usage: AgentUsage | null | undefined, now: number): ColdCache | null {
  const cache = usage?.prompt_cache;
  const context = usage?.context;
  if (!cache || !context) return null;
  const expiredAt = Date.parse(cache.expires_at);
  if (!Number.isFinite(expiredAt) || now < expiredAt || context.used < COLD_CACHE_MIN_TOKENS) return null;
  return {expiredAt, tokens: context.used};
}

// How long ago a time was: "just now", "12 min ago", "3 h ago".
export function formatAgo(at: number, now: number): string {
  const minutes = Math.floor((now - at) / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} d ago`;
}
