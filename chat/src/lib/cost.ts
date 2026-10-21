// What sessions and tasks spent: the tokens of the agent's model calls at
// the model providers' list prices (GET /sessions, GET /cost). It is the
// API-equivalent cost, which a subscription doesn't charge.

import type {HourCost, ModelCall, Tokens} from "./chat-api";

// "$12.34", "$0.004" below a cent, "$1,234" from a thousand on.
export function formatUSD(usd: number): string {
  if (usd === 0) return "$0.00";
  if (usd < 0.01) return `$${usd.toFixed(3)}`;
  if (usd >= 1000) return `$${Math.round(usd).toLocaleString("en-US")}`;
  return `$${usd.toFixed(2)}`;
}

// "1.2M", "34k", "512": a token count at a glance.
export function formatTokens(count: number): string {
  if (count >= 1e6) return `${(count / 1e6).toFixed(count >= 1e7 ? 0 : 1)}M`;
  if (count >= 1e3) return `${(count / 1e3).toFixed(count >= 1e4 ? 0 : 1)}k`;
  return String(count);
}

// All the input of some calls: uncached, cached and written to the cache.
export function inputTokens(tokens: Tokens): number {
  return tokens.input + tokens.cache_read + tokens.cache_write + (tokens.cache_write_1h ?? 0);
}

export interface TaskCost {
  usd: number;
  calls: number;
  tokens: Tokens;
  // Some calls were to a model without a known price: usd leaves them out.
  unpriced: boolean;
}

const noTokens: Tokens = {input: 0, cache_read: 0, cache_write: 0, cache_write_1h: 0, output: 0};

function addCall(cost: TaskCost, call: ModelCall): TaskCost {
  const t = call.tokens;
  return {
    usd: cost.usd + call.usd,
    calls: cost.calls + 1,
    tokens: {
      input: cost.tokens.input + t.input,
      cache_read: cost.tokens.cache_read + t.cache_read,
      cache_write: cost.tokens.cache_write + t.cache_write,
      cache_write_1h: (cost.tokens.cache_write_1h ?? 0) + (t.cache_write_1h ?? 0),
      output: cost.tokens.output + t.output,
    },
    unpriced: cost.unpriced || !call.priced,
  };
}

// What each task spent: a task's calls are those made from its prompt on,
// until the next task's prompt. starts are the tasks' prompt times in
// order; a task without one (not sent yet) gets no cost. Calls before the
// first task count in it.
export function taskCosts(starts: (string | undefined)[], calls: ModelCall[]): (TaskCost | undefined)[] {
  const bounds = starts.map((start) => (start ? Date.parse(start) : NaN));
  const costs: (TaskCost | undefined)[] = starts.map(() => undefined);
  const firstTask = bounds.findIndex((bound) => !Number.isNaN(bound));
  if (firstTask < 0) return costs;
  for (const call of calls) {
    const at = Date.parse(call.time);
    let task = firstTask;
    for (let index = firstTask + 1; index < bounds.length; index++) {
      if (!Number.isNaN(bounds[index]) && bounds[index] <= at) task = index;
    }
    costs[task] = addCall(costs[task] ?? {usd: 0, calls: 0, tokens: noTokens, unpriced: false}, call);
  }
  return costs;
}

export interface PeriodTotals {
  today: number;
  week: number;
  month: number;
  all: number;
}

// What was spent today (since local midnight), in the last 7 and 30 days
// (counting today) and in all, from hourly totals. An hour counts in a
// period if it started in it.
export function periodTotals(hourly: HourCost[], now = new Date()): PeriodTotals {
  const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const daysAgo = (days: number) => new Date(midnight.getFullYear(), midnight.getMonth(), midnight.getDate() - days).getTime();
  const today = midnight.getTime();
  const week = daysAgo(6);
  const month = daysAgo(29);
  const totals: PeriodTotals = {today: 0, week: 0, month: 0, all: 0};
  for (const {hour, usd} of hourly) {
    const at = Date.parse(hour);
    totals.all += usd;
    if (at >= month) totals.month += usd;
    if (at >= week) totals.week += usd;
    if (at >= today) totals.today += usd;
  }
  return totals;
}

// The tooltip of a cost: its tokens, and that it is the API-equivalent.
export function costDetails(cost: {usd: number; calls: number; tokens: Tokens}, unpriced = false): string {
  const t = cost.tokens;
  const lines = [
    `${formatUSD(cost.usd)} API-equivalent cost (list prices; a subscription doesn't charge it)`,
    `${cost.calls} model ${cost.calls === 1 ? "call" : "calls"}`,
    `Input ${formatTokens(inputTokens(t))}: ${formatTokens(t.input)} uncached, ${formatTokens(t.cache_read)} cached, ${formatTokens(t.cache_write + (t.cache_write_1h ?? 0))} written to the cache`,
    `Output ${formatTokens(t.output)}`,
  ];
  if (unpriced) lines.push("Some calls were to a model without a known price and aren't in the cost");
  return lines.join("\n");
}
