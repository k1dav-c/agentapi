// What was spent on each model provider: the agent's own (default) and the
// AgentAPI providers it switched to, such as a LiteLLM gateway (GET
// /sessions, the summary's spend.providers). A gateway's models often have
// no known price: their calls count tokens and no cost, which says "no
// price" rather than a cost of nothing.

import {formatUSD} from "./format";
import {DEFAULT_PROVIDER} from "./session-settings";

// providerUsageLabel names a provider in a list of what each spent: the
// agent's own as such, another by its name where names has it, else by its
// ID.
export function providerUsageLabel(provider: string, names?: ReadonlyMap<string, string>): string {
  if (provider === DEFAULT_PROVIDER) return "Own provider";
  return names?.get(provider) || provider;
}

// usesOtherProviders says whether some calls went to a provider other than
// the agent's own; a list with only its own isn't worth showing.
export function usesOtherProviders(providers: readonly {provider: string}[] | null | undefined): boolean {
  return (providers ?? []).some(({provider}) => provider !== DEFAULT_PROVIDER);
}

// providerAmount is what a provider's calls cost: "no price" when none of
// them had a known price, with a "+" when only some did (the cost leaves
// the others out).
export function providerAmount(usd: number, priced: boolean, format: (usd: number) => string = formatUSD): string {
  if (priced) return format(usd);
  return usd > 0 ? `${format(usd)}+` : "no price";
}
