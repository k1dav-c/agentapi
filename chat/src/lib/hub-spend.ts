// What the instances spent and the tokens they used, as their summaries
// report them.

import type {HubInstance, Summary} from "./hub";
import {formatTokens, formatUSDShort} from "./format";
import {providerAmount} from "./provider-usage";

export type Spend = NonNullable<Summary["spend"]>;

// currentSpend is what an instance the hub follows spent recently; none for
// one it doesn't (stopped, unreachable, connecting), whose summary is the
// last it had.
export function currentSpend(instance: HubInstance): Spend | undefined {
  return instance.connection === "connected" ? (instance.summary?.spend ?? undefined) : undefined;
}

// countedSpends are the spends that add up to what the instances spent:
// AgentAPIs of one agent in one directory of a machine report the same
// sessions, with one scope, which counts once, with its most recent figure.
// An older AgentAPI reports no scope: it counts on its own.
export function countedSpends(instances: Iterable<HubInstance>): {instance: HubInstance; spend: Spend}[] {
  const scopes = new Map<string, {instance: HubInstance; spend: Spend}>();
  for (const instance of instances) {
    const spend = currentSpend(instance);
    if (!spend) continue;
    const key = spend.scope ? `scope ${spend.scope}` : `instance ${instance.id}`;
    const counted = scopes.get(key);
    if (!counted || updatedAt(spend) > updatedAt(counted.spend)) scopes.set(key, {instance, spend});
  }
  return [...scopes.values()];
}

// updatedAt is when a spend's amounts last changed; 0 when it doesn't say.
function updatedAt(spend: Spend): number {
  const at = Date.parse(spend.updated_at);
  return Number.isNaN(at) ? 0 : at;
}

// What the instances spent recently, as their summaries report it: the
// API-equivalent cost of their sessions, at list prices, each scope once.
export interface SpendTotals {
  day: number;
  week: number;
  // How many connected instances report a spend; the others (older
  // AgentAPIs, those the hub doesn't follow now) don't count.
  counted: number;
}

export function spendTotals(instances: Iterable<HubInstance>): SpendTotals {
  const all = [...instances];
  const totals: SpendTotals = {day: 0, week: 0, counted: all.filter((instance) => currentSpend(instance)).length};
  for (const {spend} of countedSpends(all)) {
    totals.day += spend.day_usd;
    totals.week += spend.week_usd;
  }
  return totals;
}

// spendTitle says what a spend is, for a tooltip; lastKnown, that it is an
// instance's last, which doesn't count while the hub doesn't follow it;
// dayTokens, the tokens of its last 24 hours, where it says them.
export function spendTitle(day: number, week: number, lastKnown = false, dayTokens?: number): string {
  let amounts = `${formatUSDShort(day)} in the last 24 hours, ${formatUSDShort(week)} in the last 7 days (API-equivalent, at list prices; a subscription doesn't charge it)`;
  if (dayTokens !== undefined) amounts += `; ${formatTokens(dayTokens)} tokens in the last 24 hours`;
  return lastKnown ? `Last known, not counted: ${amounts}` : amounts;
}

// The tokens of the calls a spend adds up, as AgentAPIs since this release
// report them: the same calls, each once, models without a price included.

export type Tokens = NonNullable<Spend["day_tokens"]>;

// The kinds of tokens, in the order the page lists them.
export const tokenKinds: {key: Exclude<keyof Tokens, "total">; label: string}[] = [
  {key: "input", label: "Input"},
  {key: "output", label: "Output"},
  {key: "cache_write", label: "Cache write"},
  {key: "cache_read", label: "Cache read"},
];

const noTokens = (): Tokens => ({input: 0, output: 0, cache_write: 0, cache_read: 0, total: 0});

function addTokens(sum: Tokens, tokens: Tokens): void {
  sum.input += tokens.input;
  sum.output += tokens.output;
  sum.cache_write += tokens.cache_write;
  sum.cache_read += tokens.cache_read;
  sum.total += tokens.total;
}

// countedTokens are the spends whose tokens add up to what the instances
// used: those that say their tokens, each scope once with its most recent
// figure, as countedSpends counts them.
export function countedTokens(instances: Iterable<HubInstance>): {instance: HubInstance; spend: Spend; day: Tokens; week: Tokens}[] {
  const reporting = [...instances].filter((instance) => currentSpend(instance)?.week_tokens);
  return countedSpends(reporting).map(({instance, spend}) => ({instance, spend, day: spend.day_tokens ?? noTokens(), week: spend.week_tokens ?? noTokens()}));
}

// What the instances used recently, each kind and in all.
export interface TokenTotals {
  day: Tokens;
  week: Tokens;
  // How many connected instances report their tokens; the others (earlier
  // AgentAPIs, those the hub doesn't follow now) don't count.
  counted: number;
}

export function tokenTotals(instances: Iterable<HubInstance>): TokenTotals {
  const all = [...instances];
  const totals: TokenTotals = {day: noTokens(), week: noTokens(), counted: all.filter((instance) => currentSpend(instance)?.week_tokens).length};
  for (const {day, week} of countedTokens(all)) {
    addTokens(totals.day, day);
    addTokens(totals.week, week);
  }
  return totals;
}

// tokensTitle says what a count of tokens is, for a tooltip.
export function tokensTitle(day: number, week: number): string {
  return `${formatTokens(day)} tokens in the last 24 hours, ${formatTokens(week)} in the last 7 days`;
}

// What the instances spent on each model provider, as AgentAPIs since this
// release report it: the agent's own (default) and the AgentAPI providers
// their agents switched to, each scope once. A summary has only the
// providers with calls in the last 7 days, and no price to say a model had
// none: an amount of nothing with tokens is calls without a known price.

export type ProviderSpend = NonNullable<Spend["providers"]>[number];

export interface ProviderTotal {
  provider: string;
  day: number;
  week: number;
  dayTokens: number;
  weekTokens: number;
}

export interface ProviderTotals {
  // The most spent this week first, then the most tokens.
  providers: ProviderTotal[];
  // How many connected instances report their providers; the others
  // (earlier AgentAPIs, those without calls this week, those the hub
  // doesn't follow now) don't count.
  counted: number;
}

export function providerTotals(instances: Iterable<HubInstance>): ProviderTotals {
  const reporting = [...instances].filter((instance) => currentSpend(instance)?.providers);
  const totals = new Map<string, ProviderTotal>();
  for (const {spend} of countedSpends(reporting)) {
    for (const each of spend.providers ?? []) {
      const total = totals.get(each.provider) ?? {provider: each.provider, day: 0, week: 0, dayTokens: 0, weekTokens: 0};
      total.day += each.day_usd;
      total.week += each.week_usd;
      total.dayTokens += each.day_tokens.total;
      total.weekTokens += each.week_tokens.total;
      totals.set(each.provider, total);
    }
  }
  const providers = [...totals.values()].sort((a, b) => b.week - a.week || b.weekTokens - a.weekTokens);
  return {providers, counted: reporting.length};
}

// providerPriced says whether an amount of a provider is one: calls that
// cost nothing used no tokens, else their models have no known price.
export function providerPriced(usd: number, tokens: number): boolean {
  return usd > 0 || tokens === 0;
}

// providerTitle says what a provider's figures are, for a tooltip.
export function providerTitle(total: ProviderTotal): string {
  const amount = (usd: number, tokens: number) => (providerPriced(usd, tokens) ? formatUSDShort(usd) : "no known price");
  return (
    `${formatTokens(total.dayTokens)} tokens (${amount(total.day, total.dayTokens)}) in the last 24 hours, ` +
    `${formatTokens(total.weekTokens)} tokens (${amount(total.week, total.weekTokens)}) in the last 7 days (API-equivalent, at list prices)`
  );
}

// providerPeriod says a provider's figures of a period at a glance: its
// tokens, and what they cost or that they have no known price.
export function providerPeriod(usd: number, tokens: number): string {
  return `${formatTokens(tokens)} tokens, ${providerAmount(usd, providerPriced(usd, tokens), formatUSDShort)}`;
}
