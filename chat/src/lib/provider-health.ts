// How each model provider answered the agents' calls over the last hour,
// as AgentAPI's provider proxy counts them (GET /settings'
// options.providers[].health, the summary's provider_health): the chat's
// model menu warns of a provider that is failing now, and the hub's
// Providers settings add up the instances' figures.

import type {ProviderFailure, ProviderHealth} from "./chat-api";
import {formatAgo} from "./format";
import type {HubInstance} from "./hub";

// failureRate is the share of the hour's calls the provider failed.
export function failureRate(health: ProviderHealth): number {
  return health.requests > 0 ? health.failures / health.requests : 0;
}

// providerFailing says whether a provider fails now: its latest call
// failed, or half its calls of the hour did.
export function providerFailing(health: ProviderHealth | null | undefined): boolean {
  if (!health || health.requests === 0 || health.failures === 0) return false;
  const failed = health.last_failure ? Date.parse(health.last_failure.at) : NaN;
  const succeeded = health.last_success_at ? Date.parse(health.last_success_at) : NaN;
  const latestFailed = !Number.isNaN(failed) && (Number.isNaN(succeeded) || failed >= succeeded);
  return latestFailed || failureRate(health) >= 0.5;
}

// formatPercent is a share in whole percent: "<1%" for a few calls of many.
export function formatPercent(rate: number): string {
  if (rate > 0 && rate < 0.01) return "<1%";
  return `${Math.round(rate * 100)}%`;
}

// formatLatency is a time in milliseconds at a glance: "850 ms", "1.2 s",
// "12 s".
export function formatLatency(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  const seconds = ms / 1000;
  return `${seconds.toFixed(seconds < 9.95 ? 1 : 0).replace(/\.0$/, "")} s`;
}

// failureText says how a call failed: the status with the provider's
// message, or what kept it from answering.
export function failureText(failure: ProviderFailure): string {
  const message = failure.message;
  switch (failure.kind) {
    case "status":
      if (!failure.status || message.startsWith(String(failure.status))) return message;
      return `${failure.status} ${message}`;
    case "timeout":
      return `timed out: ${message}`;
    case "network":
      return `unreachable: ${message}`;
    case "stream":
      return message;
    default:
      return message;
  }
}

// providerHealthTitle says what a provider's health of the hour is, for a
// tooltip: how many calls failed, how fast it answers, and the last error
// and when.
export function providerHealthTitle(health: ProviderHealth, now: number): string {
  const calls = health.requests === 1 ? "call" : "calls";
  let text = `${health.failures} of ${health.requests} ${calls} failed in the last hour (${formatPercent(failureRate(health))})`;
  if (health.client_errors > 0) text += `, ${health.client_errors} refused`;
  text += ".";
  if (health.first_byte_p50_ms > 0) text += ` First byte in ${formatLatency(health.first_byte_p50_ms)} (median), ${formatLatency(health.first_byte_p95_ms)} (p95).`;
  if (health.last_failure) text += ` Last error ${formatAgo(health.last_failure.at, now)}: ${failureText(health.last_failure)}.`;
  return text;
}

// ProviderHealthTotal is a provider's health over the instances the hub
// follows: their calls of the last hour added up, the median first-byte
// time of their answers (their medians, weighted by answers; the 95th
// percentile is the highest of theirs), and the latest success and
// failure.
export type ProviderHealthTotal = ProviderHealth & {provider: string};

// later is whichever of two times is the later; a missing one loses.
function later<T extends string | undefined>(a: T, b: T): T {
  if (!a) return b;
  if (!b) return a;
  return Date.parse(b) > Date.parse(a) ? b : a;
}

// providerHealthTotals adds up the health each connected instance reports
// of each provider. Each AgentAPI counts its own agent's calls: nothing is
// counted twice.
export function providerHealthTotals(instances: Iterable<HubInstance>): Map<string, ProviderHealthTotal> {
  const totals = new Map<string, ProviderHealthTotal>();
  const medians = new Map<string, {p50: number; answers: number}[]>();
  for (const instance of instances) {
    if (instance.connection !== "connected") continue;
    for (const each of instance.summary?.provider_health ?? []) {
      const total = totals.get(each.provider) ?? {provider: each.provider, requests: 0, failures: 0, client_errors: 0, first_byte_p50_ms: 0, first_byte_p95_ms: 0};
      total.requests += each.requests;
      total.failures += each.failures;
      total.client_errors += each.client_errors;
      total.first_byte_p95_ms = Math.max(total.first_byte_p95_ms, each.first_byte_p95_ms);
      total.last_success_at = later(total.last_success_at, each.last_success_at);
      if (each.last_failure && later(total.last_failure?.at, each.last_failure.at) === each.last_failure.at) total.last_failure = each.last_failure;
      const answers = each.requests - each.failures;
      if (answers > 0 && each.first_byte_p50_ms > 0) medians.set(each.provider, [...(medians.get(each.provider) ?? []), {p50: each.first_byte_p50_ms, answers}]);
      totals.set(each.provider, total);
    }
  }
  for (const [provider, list] of medians) {
    list.sort((a, b) => a.p50 - b.p50);
    const half = list.reduce((sum, each) => sum + each.answers, 0) / 2;
    let seen = 0;
    totals.get(provider)!.first_byte_p50_ms = list.find((each) => (seen += each.answers) >= half)?.p50 ?? 0;
  }
  return totals;
}

// providerHealthLine says a provider's health of the hour at a glance:
// "1h 120 calls · 3% failed · p50 850 ms · 529 Overloaded 5 min ago".
export function providerHealthLine(health: ProviderHealth, now: number): string {
  const parts = [`1h ${health.requests} ${health.requests === 1 ? "call" : "calls"}`, `${formatPercent(failureRate(health))} failed`];
  if (health.first_byte_p50_ms > 0) parts.push(`p50 ${formatLatency(health.first_byte_p50_ms)}`);
  if (health.last_failure) parts.push(`${failureText(health.last_failure)} ${formatAgo(health.last_failure.at, now)}`);
  return parts.join(" · ");
}
