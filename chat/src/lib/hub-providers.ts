// Model providers are OpenAI- or Anthropic-style endpoints, such as a
// LiteLLM gateway, that Claude Code and Codex sessions can switch to. Those
// kept on the hub (GET and PUT /api/providers) reach every AgentAPI that
// reports to it; the hub's settings manage them. The hub never answers a
// key, only whether a provider has one.

import type {HubWire} from "./hub";
import {hubLimits} from "./hub-schema";

// A provider as the hub answers it: without its key, and with the prices
// of its models (a hub before the prices answers none).
export type Provider = Omit<HubWire["ProviderView"], "model_prices"> & {model_prices?: ProviderModelPrice[]};

// The price the hub has for a provider's model: matched on OpenRouter by
// name (auto), picked (chosen), or none, as picked.
export type ProviderModelPrice = HubWire["ProviderModelPrice"];
export type ModelPrice = HubWire["ModelPrice"];
export type OpenRouterModel = HubWire["OpenRouterModel"];
// Where the hub's copy of OpenRouter's list stands.
export type PriceStatus = HubWire["PriceStatus"];

// A provider as PUT /api/providers takes it: without api_key the hub keeps
// the key only at unchanged endpoints, "" removes it.
export type ProviderChange = HubWire["ProviderChange"];

// The list's optimistic-concurrency token. The wire keeps it opaque: the
// client sends back exactly what the hub answered.
export type ProviderRevision = string | number;
export type ProviderSnapshot = {revision: ProviderRevision; providers: Provider[]; prices?: PriceStatus};

export type ProviderModel = HubWire["Model"];

// The most providers the hub keeps.
export const maxProviders = hubLimits.max_providers;

// ProviderDraft is what the editor edits. models is a text of model IDs,
// one per line or comma-separated. key empty keeps the key the provider
// has, unless removeKey.
export type ProviderDraft = {
  id: string;
  name: string;
  baseURL: string;
  anthropicBaseURL: string;
  models: string;
  key: string;
  removeKey: boolean;
};

// providerName is the name to show: the ID when it has none.
export function providerName(provider: {id: string; name?: string}): string {
  return provider.name?.trim() || provider.id;
}

// providerModelsText is what a list shows of a provider's models.
export function providerModelsText(provider: Provider): string {
  const count = provider.models?.length ?? 0;
  if (count === 0) return "lists its models";
  return count === 1 ? "1 model" : `${count} models`;
}

// parseProviders reads the providers of an answer: undefined when it isn't
// a list; entries that aren't providers are left out.
export function parseProviders(data: unknown): Provider[] | undefined {
  if (!Array.isArray(data)) return undefined;
  const providers: Provider[] = [];
  const ids = new Set<string>();
  for (const entry of data) {
    if (!entry || typeof entry !== "object") continue;
    const {id, name, base_url, anthropic_base_url, models, has_key, openrouter, model_prices} = entry as Partial<Record<keyof Provider, unknown>>;
    if (typeof id !== "string" || !id || ids.has(id) || typeof base_url !== "string") continue;
    const provider: Provider = {id, base_url, has_key: has_key === true};
    if (typeof name === "string" && name) provider.name = name;
    if (typeof anthropic_base_url === "string" && anthropic_base_url) provider.anthropic_base_url = anthropic_base_url;
    const kept = Array.isArray(models) ? models.flatMap(parseModel) : [];
    if (kept.length > 0) provider.models = kept;
    const choices = parseChoices(openrouter);
    if (choices) provider.openrouter = choices;
    const prices = Array.isArray(model_prices) ? model_prices.flatMap(parseModelPrice) : [];
    if (prices.length > 0) provider.model_prices = prices;
    ids.add(id);
    providers.push(provider);
  }
  return providers;
}

function parseModel(entry: unknown): ProviderModel[] {
  if (!entry || typeof entry !== "object") return [];
  const {id, name} = entry as {id?: unknown; name?: unknown};
  if (typeof id !== "string" || !id) return [];
  return [typeof name === "string" && name ? {id, name} : {id}];
}

// parseChoices reads a provider's price choices: model ID to an OpenRouter
// model ID, or "none".
function parseChoices(data: unknown): Record<string, string> | undefined {
  if (!data || typeof data !== "object" || Array.isArray(data)) return undefined;
  const entries = Object.entries(data).filter((entry): entry is [string, string] => typeof entry[1] === "string" && entry[0] !== "" && entry[1] !== "");
  return entries.length > 0 ? Object.fromEntries(entries) : undefined;
}

const choices: readonly string[] = ["auto", "chosen", "none"] satisfies ProviderModelPrice["choice"][];

function parseModelPrice(entry: unknown): ProviderModelPrice[] {
  if (!entry || typeof entry !== "object") return [];
  const {model, name, choice, openrouter, openrouter_name, price} = entry as Partial<Record<keyof ProviderModelPrice, unknown>>;
  if (typeof model !== "string" || !model || typeof choice !== "string" || !choices.includes(choice)) return [];
  const parsed: ProviderModelPrice = {model, choice: choice as ProviderModelPrice["choice"]};
  if (typeof name === "string" && name) parsed.name = name;
  if (typeof openrouter === "string" && openrouter) parsed.openrouter = openrouter;
  if (typeof openrouter_name === "string" && openrouter_name) parsed.openrouter_name = openrouter_name;
  const read = parsePrice(price);
  if (read) parsed.price = read;
  return [parsed];
}

// parsePrice reads a price per million tokens.
export function parsePrice(data: unknown): ModelPrice | undefined {
  if (!data || typeof data !== "object") return undefined;
  const {input, output, cache_read, cache_write, source} = data as Partial<Record<keyof ModelPrice, unknown>>;
  const number = (value: unknown) => (typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined);
  const [i, o] = [number(input), number(output)];
  if (i === undefined || o === undefined) return undefined;
  const price: ModelPrice = {input: i, output: o, cache_read: number(cache_read) ?? 0, cache_write: number(cache_write) ?? 0};
  if (typeof source === "string" && source) price.source = source;
  return price;
}

// perMillion is a price per million tokens, short: $16, $2.5, $0.19,
// $0.038, $0.
export function perMillion(usd: number): string {
  if (usd === 0) return "$0";
  const text = usd >= 1 ? usd.toFixed(2) : usd.toPrecision(2);
  return `$${text.includes(".") ? text.replace(/\.?0+$/, "") : text}`;
}

// priceText is a model's price as a line shows it: input and output per
// million tokens.
export function priceText(price: ModelPrice): string {
  return `${perMillion(price.input)} / ${perMillion(price.output)} per M tokens`;
}

// priceDetails is all of a model's price, for a title: cache reads and
// writes too, where it has them.
export function priceDetails(price: ModelPrice): string {
  const parts = [`input ${perMillion(price.input)}`, `output ${perMillion(price.output)}`];
  if (price.cache_read > 0) parts.push(`cache read ${perMillion(price.cache_read)}`);
  if (price.cache_write > 0) parts.push(`cache write ${perMillion(price.cache_write)}`);
  return `USD per million tokens: ${parts.join(", ")}`;
}

// openRouterName is how a line names an OpenRouter model: its name without
// the vendor OpenRouter puts before it ("MoonshotAI: Kimi K3" is Kimi K3),
// else its ID.
export function openRouterName(model: {openrouter?: string; openrouter_name?: string}): string {
  const name = model.openrouter_name?.trim();
  if (name) return name.replace(/^[^:]+:\s+/, "");
  return model.openrouter ?? "";
}

// priceSourceText says where a model's price comes from, or that it has
// none and why.
export function priceSourceText(model: ProviderModelPrice): string {
  switch (model.choice) {
    case "none":
      return "No price, as picked";
    case "chosen":
      return model.price ? `OpenRouter: ${openRouterName(model)}` : `OpenRouter: ${model.openrouter} isn't in the hub's list`;
    case "auto":
      return model.price ? `OpenRouter: ${openRouterName(model)}` : "No OpenRouter model of this name";
  }
}

// withPriceChoice is the change that leaves provider as it is but for the
// price of model: an OpenRouter model ID, "none", or undefined for
// automatic. It always sends the choices, so that the last one removed
// removes them.
export function withPriceChoice(provider: Provider, model: string, choice: string | undefined): ProviderChange {
  const next = {...provider.openrouter};
  if (choice === undefined) delete next[model];
  else next[model] = choice;
  return {...keptChange(provider), openrouter: next};
}

// draftOf is the editor's draft of a provider, or of a new one.
export function draftOf(provider?: Provider): ProviderDraft {
  return {
    id: provider?.id ?? "",
    name: provider?.name ?? "",
    baseURL: provider?.base_url ?? "",
    anthropicBaseURL: provider?.anthropic_base_url ?? "",
    models: (provider?.models ?? []).map((model) => model.id).join("\n"),
    key: "",
    removeKey: false,
  };
}

// parseModelIDs reads the models' text: one ID per line or comma-separated,
// each once.
export function parseModelIDs(text: string): string[] {
  const ids = text
    .split(/[\n,]/)
    .map((id) => id.trim())
    .filter(Boolean);
  return [...new Set(ids)];
}

// changeOf is the change that saves draft. A model that previous had keeps
// its name; the key is sent only when the draft sets or removes it.
export function changeOf(draft: ProviderDraft, previous?: Provider): ProviderChange {
  const change: ProviderChange = {id: draft.id.trim(), base_url: draft.baseURL.trim()};
  const name = draft.name.trim().replace(/\s+/g, " ");
  if (name) change.name = name;
  const anthropic = draft.anthropicBaseURL.trim();
  if (anthropic) change.anthropic_base_url = anthropic;
  const models = parseModelIDs(draft.models).map((id) => previous?.models?.find((model) => model.id === id) ?? {id});
  if (models.length > 0) change.models = models;
  if (draft.removeKey) change.api_key = "";
  else if (draft.key.trim()) change.api_key = draft.key.trim();
  return change;
}

// keptChange is the change that leaves provider as it is, key included.
export function keptChange(provider: Provider): ProviderChange {
  const change: ProviderChange & {has_key?: boolean; model_prices?: unknown} = {...provider};
  delete change.has_key;
  delete change.model_prices;
  return change;
}

const idPattern = /^[a-z0-9][a-z0-9_-]{0,62}$/;

// draftProblem says what the hub would refuse in draft (lib/provider
// Validate), or undefined; others are the IDs of the other providers.
export function draftProblem(draft: ProviderDraft, others: readonly string[]): string | undefined {
  const id = draft.id.trim();
  if (!id) return "The ID is empty.";
  if (!idPattern.test(id)) return "The ID takes lowercase letters, digits, - and _, starts with a letter or digit, and has at most 63.";
  if (id === "default") return "The ID “default” is the agent's own provider.";
  if (others.includes(id)) return `Another provider has the ID “${id}”.`;
  if (!httpURL(draft.baseURL.trim())) return "The base URL must be an http or https URL.";
  const anthropic = draft.anthropicBaseURL.trim();
  if (anthropic && !httpURL(anthropic)) return "The Anthropic base URL must be an http or https URL.";
  return undefined;
}

// httpURL reports whether text is an http or https URL with a host.
function httpURL(text: string): boolean {
  if (!/^https?:\/\/[^/?#]/i.test(text)) return false;
  try {
    return new URL(text).host !== "";
  } catch {
    return false;
  }
}

// providerEndpoint is a provider's base URL as a list shows it: the host
// and path, without the scheme or the trailing slash.
export function providerEndpoint(baseURL: string): string {
  try {
    const url = new URL(baseURL);
    return url.host + url.pathname.replace(/\/+$/, "");
  } catch {
    return baseURL;
  }
}

// providerMonogram is the one or two letters of a provider's tile: the
// initials of its first two words, else its first two letters.
export function providerMonogram(name: string): string {
  const words = name.match(/[\p{L}\p{N}]+/gu) ?? [];
  if (words.length === 0) return "?";
  const [first, second] = words as [string, ...string[]];
  const letters = second ? first.charAt(0) + second.charAt(0) : first.slice(0, 2);
  return letters.toUpperCase();
}

// providerHue is the hue of a provider's tile, the same for an ID
// wherever it shows.
export function providerHue(id: string): number {
  let hash = 0;
  for (const char of id) hash = (hash * 31 + char.codePointAt(0)!) >>> 0;
  return hash % 360;
}

// toggleModel adds the model id to the models' text, or takes it out.
export function toggleModel(text: string, id: string): string {
  const ids = parseModelIDs(text);
  return (ids.includes(id) ? ids.filter((each) => each !== id) : [...ids, id]).join("\n");
}

// checkFailureText says in words why a connection check failed, from the
// hub's reason: a refused key, a URL without the API, a provider out of
// reach; else the reason itself.
export function checkFailureText(reason: string): string {
  const status = /answered (\d{3})/.exec(reason)?.[1];
  if (status === "401" || status === "403") return "The provider refused the key.";
  if (status === "404") return "No API at this URL: check that it ends with /v1.";
  if (status) return `The provider answered ${status}.`;
  if (/no such host|connection refused|i\/o timeout|deadline exceeded|unreachable|Client\.Timeout/i.test(reason)) return "The hub couldn't reach the provider.";
  if (/read the models/i.test(reason)) return "The URL answered, but not with a list of models.";
  return reason;
}
