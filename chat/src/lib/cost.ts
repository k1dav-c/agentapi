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

// When a task's model calls were made. The server says when a task it
// derives ran: from its start until it ended (ran.until), or until now
// while it runs. A task only the chat knows (from a server that derives
// none) has the time of its prompt (from): its calls run until the next
// task's prompt. null for a task that has none: not sent yet, or steered
// into another task, whose calls count there.
export type TaskSpan = {ran: {from: string; until?: string}} | {from: string} | null;

// taskSpan returns the span of a task, as the server's task that its
// prompt started (serverTask) or its prompt says.
export function taskSpan(prompt: {id?: number; time?: string}, serverTask?: {id: number; started_at: string; ended_at?: string}): TaskSpan {
  if (prompt.id === undefined) return null;
  if (serverTask) return serverTask.id === prompt.id ? {ran: {from: serverTask.started_at, until: serverTask.ended_at}} : null;
  return prompt.time ? {from: prompt.time} : null;
}

// What each task spent, by the spans of the tasks in order. When the
// server says when tasks ran, a task's calls are those made while it ran;
// otherwise, those made from its prompt on until the next task's prompt,
// and calls before the first task count in it.
export function taskCosts(spans: TaskSpan[], calls: ModelCall[], now = Date.now()): (TaskCost | undefined)[] {
  const costs: (TaskCost | undefined)[] = spans.map(() => undefined);
  const add = (task: number, call: ModelCall) => {
    costs[task] = addCall(costs[task] ?? {usd: 0, calls: 0, tokens: noTokens, unpriced: false}, call);
  };
  const ran = spans.flatMap((span, task) => (span && "ran" in span ? [{task, from: Date.parse(span.ran.from), until: span.ran.until ? Date.parse(span.ran.until) : now}] : []));
  if (ran.length > 0) {
    ran.sort((a, b) => a.from - b.from);
    for (const call of calls) {
      const at = Date.parse(call.time);
      const span = ran[lastAtOrBefore(ran, at)];
      if (span && at <= span.until) add(span.task, call);
    }
    return costs;
  }
  const bounds = spans.map((span) => (span && "from" in span ? Date.parse(span.from) : NaN));
  const firstTask = bounds.findIndex((bound) => !Number.isNaN(bound));
  if (firstTask < 0) return costs;
  for (const call of calls) {
    const at = Date.parse(call.time);
    let task = firstTask;
    for (let index = firstTask + 1; index < bounds.length; index++) {
      if (!Number.isNaN(bounds[index]) && bounds[index] <= at) task = index;
    }
    add(task, call);
  }
  return costs;
}

// The index of the last span that starts at or before at, or -1.
function lastAtOrBefore(spans: {from: number}[], at: number): number {
  let low = 0;
  let high = spans.length - 1;
  let match = -1;
  while (low <= high) {
    const middle = (low + high) >> 1;
    if (spans[middle].from <= at) {
      match = middle;
      low = middle + 1;
    } else {
      high = middle - 1;
    }
  }
  return match;
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
